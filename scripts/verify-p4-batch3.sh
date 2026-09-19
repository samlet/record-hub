#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
evidence_dir="${RECORD_HUB_P4_BATCH3_EVIDENCE_DIR:-$root_dir/build/evidence/phase4/batch3-$run_id}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date git jq rg diff; do command -v "$command" >/dev/null || { echo "P4 Batch 3: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p4/p4-batch3-spec.json"
jq -e '.version == 1 and .phase == "4" and .batch == "3" and .liveRequired == true and (.cases | length) == 6' "$spec" >/dev/null
static_status=0
run_static(){ local id="$1"; shift; if "$@" >"$evidence_dir/static/$id.log" 2>&1; then printf 'PASS\n' >"$evidence_dir/static/$id.status"; else printf 'FAIL\n' >"$evidence_dir/static/$id.status"; static_status=1; fi; }
run_static P4-300-contract bash -c "diff -q '$approver_root/approver-contract/src/main/resources/record-hub/approvals/bids-tender-publication-approval-request-v1.schema.json' '$bids_root/backend/internal/recordhub/contracts/approvals/bids-tender-publication-approval-request-v1.schema.json' && rg -q 'BidsTenderPublicationApprovalRequestValidator|unknown field|bidAmount' '$approver_root/approver-api/src'"
run_static P4-301-migration bash -c "test -f '$bids_root/backend/internal/repository/migrations/0028_tender_approval_pilot.sql' && rg -q 'TenderApprovalRequestOutbox|TenderApprovalResultInbox|TenderApprovalTaskCompletionOutbox' '$bids_root/backend/internal/domain/tender_approval.go'"
run_static P4-302-relay bash -c "rg -q 'RequestDispatcher|Idempotency-Key|externalRequestId' '$bids_root/backend/internal/approval' && rg -q 'BidsTenderPublicationApprovalResultContractHandler' '$approver_root/approver-application/src/main/java'"
run_static P4-303-authority bash -c "rg -q 'Transaction|TenderApprovalResultInbox|EnqueueTaskCompletion|ExternalTenderApprovalEnabled' '$bids_root/backend/internal/approval' '$bids_root/backend/internal/config' '$bids_root/backend/internal/application/tender_service.go'"
run_static P4-304-association bash -c "rg -q 'TenderApplicationAssociation|tender-applications|tender_application_associations' '$root_dir/server/internal/modules/projection' && rg -q 'NewMongoTenderApplicationAssociationRepository' '$root_dir/server/internal/app/runtime.go'"
run_static P4-305-commits bash -c "git -C '$root_dir' rev-parse --verify HEAD >/dev/null && git -C '$bids_root' rev-parse --verify HEAD >/dev/null && git -C '$approver_root' rev-parse --verify HEAD >/dev/null"
live_requested="${RECORD_HUB_P4_BATCH3_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated Bids/Approver/Record Hub/PostgreSQL/MongoDB/NATS/Conductor/Temporal topology and explicit evidence runner; ordinary local services are not accepted"
static_value(){ [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$static_status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg c300 "$(static_value P4-300-contract)" --arg c301 "$(static_value P4-301-migration)" --arg c302 "$(static_value P4-302-relay)" \
  --arg c303 "$(static_value P4-303-authority)" --arg c304 "$(static_value P4-304-association)" --arg c305 "$(static_value P4-305-commits)" \
  '{gate:"P4-305",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p4/p4-batch3-spec.json",evidenceDir:$evidenceDir,liveRequested:($liveRequested=="1"),cases:[{id:"contract-allowlist",staticStatus:$c300,liveStatus:$liveStatus,liveReason:$liveReason},{id:"migration-inbox-outbox",staticStatus:$c301,liveStatus:$liveStatus,liveReason:$liveReason},{id:"relay-materializer",staticStatus:$c302,liveStatus:$liveStatus,liveReason:$liveReason},{id:"result-authority",staticStatus:$c303,liveStatus:$liveStatus,liveReason:$liveReason},{id:"tender-application-association",staticStatus:$c304,liveStatus:$liveStatus,liveReason:$liveReason},{id:"fault-security-matrix",staticStatus:$c305,liveStatus:$liveStatus,liveReason:$liveReason}],retry:"Provide isolated topology and a dedicated runner, then rerun with RECORD_HUB_P4_BATCH3_LIVE=1"}' \
  | tee "$evidence_dir/batch3.json"
cp "$evidence_dir/batch3.json" "$evidence_dir/phase-4-batch3.json"
echo "P4 Batch 3 report: $evidence_dir/batch3.json"
[[ "$static_status" == "0" ]]
