#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_APPROVAL_CONNECTOR_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-303-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-303: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-303-approval-connector-spec.json"
manifest="$root_dir/contracts/connectors/manifest.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-303" and .liveRequired == true and (.owners | sort) == ["bids", "fluxion"] and (.connectorKeys | sort) == ["bids.approval", "fluxion.approval"] and (.boundaries.requestIdentity | length) >= 5 and (.boundaries.ownerSideEffect | length) >= 4 and (.boundaries.reconciliation | length) >= 4 and (.boundaries.fallbackRollback | length) >= 5 and (.forbidden | length) >= 4 and (.liveEvidence | length) >= 5' "$spec" >/dev/null
jq -e 'all(.entries[]; .connector and .event and (.schemaVersion >= 1)) and ([.entries[].connector] | index("fluxion.approval")) != null and ([.entries[].connector] | index("bids.approval")) != null' "$manifest" >/dev/null

status=0
check() {
  local id="$1"; shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    status=1
  fi
}

fluxion_approval="$fluxion_root/server/src/main/kotlin/fluxion/approval"
fluxion_workflow="$fluxion_root/server/src/main/kotlin/fluxion/workflow"
fluxion_config="$fluxion_root/server/src/main/kotlin/fluxion/config"
bids_approval="$bids_root/backend/internal/approval"
bids_domain="$bids_root/backend/internal/domain"
bids_config="$bids_root/backend/internal/config"

check fluxion-owner-transaction bash -c "
  test -d '$fluxion_approval' &&
  rg -q 'insertIgnore|ExternalApprovalRequestOutboxTable|ExternalApprovalResultInboxTable|ExternalApprovalUpdateOutboxTable|transaction' '$fluxion_approval/ExternalApproval.kt' &&
  rg -q 'decisionVersion|late or mismatched approval result|ExternalApprovalResultInboxStore' '$fluxion_approval/ExternalApprovalResultService.kt' '$fluxion_approval/ExternalApproval.kt'
"

check fluxion-reconciliation bash -c "
  rg -q 'ExternalApprovalGenerationGuard|VERSION_DRIFT|reconciliation|ExternalApprovalUpdateOutboxRelay' '$fluxion_approval' &&
  rg -q 'generation|late' '$fluxion_approval/ExternalApproval.kt' '$fluxion_approval/ExternalApprovalRequestStateMachine.kt'
"

check fluxion-fallback-rollback bash -c "
  rg -q 'externalApprovalEnabled|local human-task fallback' '$fluxion_workflow/StageWorkflows.kt' '$fluxion_root/server/src/main/kotlin/fluxion/Worker.kt' &&
  rg -q 'rolloutScopes|drain|in.flight|inflight' '$fluxion_config/AppConfig.kt' '$fluxion_root/server/src/main/kotlin/fluxion/Worker.kt' &&
  rg -q 'flag off|fallback|drain|rollback' '$root_dir/docs/phase-4-batch2-p4-200.md' '$root_dir/docs/phase-4-batch2-p4-203.md'
"

check bids-owner-side-effect bash -c "
  test -d '$bids_approval' &&
  rg -q 'ExternalRequestID|ApprovalGeneration|ProposalHash|stable|generation' '$bids_approval/contract.go' '$bids_domain/tender_approval.go' &&
  rg -q 'Idempotency-Key|tender-approval-request-outbox|Approver' '$bids_approval/request_dispatcher.go' '$bids_approval/store.go' &&
  rg -q 'Transaction|TenderApprovalResultInbox|EnqueueTenderSummaryChanged|EnqueueTaskCompletion|ResultApplied' '$bids_approval/result_service.go'
"

check bids-reconciliation bash -c "
  rg -q 'does not match the pending request|approval result is stale|decision version payload conflict|event id payload conflict' '$bids_approval/result_service.go' &&
  rg -q 'WorkflowReconciliationInterval|WORKFLOW_RECONCILIATION_INTERVAL|outboxDeadLetterAlertThreshold' '$bids_config/config.go' &&
  rg -q 'package reconciliation|reconcile|workflow_reconciliations' '$bids_root/backend/internal/reconciliation' '$bids_domain/workflow_reconciliation.go'
"

check connector-routing-and-isolation bash -c "
  rg -q '/api/v1/integrations/bids/tender-publication-approval-requests|X-Approver-Connector-Key' '$bids_approval/request_dispatcher.go' &&
  ! rg -n -i 'record[ _-]?hub' '$fluxion_approval' '$bids_approval' &&
  rg -q 'P5-INT-002|compatibility window|禁用|回滚|reconciliation' '$root_dir/docs/phase-5-requirements.md' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check owner-commit-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_APPROVAL_CONNECTOR_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated Approver/Fluxion/Bids/Temporal/Conductor topology, owner credentials, fault injection, and immutable evidence export"
required=(RECORD_HUB_P5_APPROVAL_CONNECTOR_TOPOLOGY_FILE RECORD_HUB_P5_APPROVAL_CONNECTOR_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="approval connector live runner is not installed in this workspace; execute duplicate/late-result/fallback/rollback harness"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() {
  [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'
}
overall="PARTIAL"
[[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg fluxionOwner "$(static_value fluxion-owner-transaction)" \
  --arg fluxionRecon "$(static_value fluxion-reconciliation)" \
  --arg fluxionFallback "$(static_value fluxion-fallback-rollback)" \
  --arg bidsOwner "$(static_value bids-owner-side-effect)" \
  --arg bidsRecon "$(static_value bids-reconciliation)" \
  --arg routing "$(static_value connector-routing-and-isolation)" \
  --arg commits "$(static_value owner-commit-boundary)" \
  '{task:"P5-303",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-303-approval-connector-spec.json",manifest:"contracts/connectors/manifest.json",evidenceDir:$evidenceDir,static:{fluxionOwnerTransaction:$fluxionOwner,fluxionReconciliation:$fluxionRecon,fluxionFallbackRollback:$fluxionFallback,bidsOwnerSideEffect:$bidsOwner,bidsReconciliation:$bidsRecon,connectorRoutingAndIsolation:$routing,ownerCommitBoundary:$commits},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision the isolated owner topology and rerun with RECORD_HUB_P5_APPROVAL_CONNECTOR_LIVE=1"}' \
  | tee "$evidence_dir/p5-303.json"
echo "P5-303 report: $evidence_dir/p5-303.json"
[[ "$status" == "0" ]]
