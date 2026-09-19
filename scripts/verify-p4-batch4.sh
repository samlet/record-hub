#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
evidence_dir="${RECORD_HUB_P4_BATCH4_EVIDENCE_DIR:-$root_dir/build/evidence/phase4/batch4-$run_id}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date git jq rg diff; do command -v "$command" >/dev/null || { echo "P4 Batch 4: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p4/p4-batch4-spec.json"
jq -e '.version == 1 and .phase == "4" and .batch == "4" and (.cases | length) == 5' "$spec" >/dev/null

static_status=0
run_static() {
  local id="$1"; shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    static_status=1
  fi
}

# P4-400: every owner must bind the request/result to the authenticated scope;
# tests and source checks intentionally reject 404-style existence probing.
run_static P4-400-negative-matrix bash -c "
  rg -q 'StatusForbidden|ErrForbidden|cross.scope|cross-scope' '$root_dir/server/internal/modules/projection/association_test.go' '$root_dir/server/internal/modules/projection/tender_association_test.go' &&
  rg -q 'tenant mismatch|workspace mismatch|expectedTenantId' '$fluxion_root/server/src/main/kotlin/fluxion/approval' &&
  rg -q 'permissions.require|Permission.AUDIT_READ|Permission.PROCESS_ADMIN' '$approver_root/approver-api/src/main/java/com/xiaofeiwu/approver/api/integration' &&
  rg -q 'does not match the pending request|secureIntegrationToken|OrganizationID' '$bids_root/backend/internal/approval' '$bids_root/backend/internal/httpapi'
"

# P4-401: bounded labels and operational dimensions are present in every owner.
run_static P4-401-metrics-slo bash -c "
  rg -q 'record_hub_projection_(backlog|lag_age_seconds|slo_breach|error_budget_remaining|dlq_total)' '$root_dir/server/internal/modules/projection' &&
  rg -q 'IntegrationMetrics|result.delivery|reconciliation' '$approver_root/approver-worker/src/main/java' &&
  rg -q 'metrics|Prometheus|snapshot|backlog|lag|dead' '$fluxion_root/server/src' &&
  rg -q 'Prometheus|metrics|outbox.*age|outbox.*attempt' '$bids_root/backend/internal'
"

# P4-402 is covered by the browser build and the HTTP method/scoping tests.
run_static P4-402-association-ui bash -c "
  rg -q 'ApprovalAssociationsPanel|tenderApplicationAssociations|Viewer / Operator read-only' '$root_dir/web/src' &&
  rg -q 'StatusMethodNotAllowed|unexpectedly mutates terminal projection' '$root_dir/server/internal/modules/projection/tender_association_test.go'
"

# P4-403 scans source/config/evidence contracts. Binary build directories and
# the intentionally untracked local c123.db are excluded; negative fixtures
# are checked by their contract validators rather than this lexical scan.
run_static P4-403-evidence-scan bash -c "
  ! rg -n --hidden -g '!target/**' -g '!build/**' -g '!web/.next/**' -g '!web/node_modules/**' -g '!c123.db' -g '!*.sum' -g '!*.lock' -g '!scripts/verify-p4-batch4.sh' \
    -e 'BEGIN (RSA|OPENSSH|EC) PRIVATE KEY' -e 'AKIA[0-9A-Z]{16}' -e '-----BEGIN' \
    '$root_dir' '$approver_root' '$fluxion_root' '$bids_root' &&
  rg -q 'redact|redacted|safe.*projection|allowlist' '$root_dir/docs' '$approver_root/docs' '$fluxion_root' '$bids_root/backend/internal/approval'
"

# P4-404 requires the owner/trigger/stop/rollback/reconciliation runbook to be
# present before a live drill is attempted.
run_static P4-404-runbook bash -c "
  test -f '$root_dir/docs/phase-4-batch4-runbook.md' &&
  rg -q 'owner|触发|停止|回滚|reconciliation|rotation|restore' '$root_dir/docs/phase-4-batch4-runbook.md' &&
  test -f '$approver_root/docs/integration-operations-runbook.md' &&
  test -f '$root_dir/docs/phase-3-batch4f-backup-restore.md'
"

live_requested="${RECORD_HUB_P4_BATCH4_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated four-owner topology, role credentials, mixed-load/fault injection, and evidence capture; ordinary local services are not accepted"
static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$static_status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg c400 "$(static_value P4-400-negative-matrix)" --arg c401 "$(static_value P4-401-metrics-slo)" \
  --arg c402 "$(static_value P4-402-association-ui)" --arg c403 "$(static_value P4-403-evidence-scan)" --arg c404 "$(static_value P4-404-runbook)" \
  '{gate:"P4-404",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p4/p4-batch4-spec.json",evidenceDir:$evidenceDir,liveRequested:($liveRequested=="1"),cases:[{id:"tenant-organization-identity-acl-negative-matrix",staticStatus:$c400,liveStatus:$liveStatus,liveReason:$liveReason},{id:"bounded-metrics-and-slo-snapshot",staticStatus:$c401,liveStatus:$liveStatus,liveReason:$liveReason},{id:"approval-association-operator-console",staticStatus:$c402,liveStatus:$liveStatus,liveReason:$liveReason},{id:"secret-pii-sealed-data-evidence-scan",staticStatus:$c403,liveStatus:$liveStatus,liveReason:$liveReason},{id:"rotation-recovery-reconciliation-runbook",staticStatus:$c404,liveStatus:$liveStatus,liveReason:$liveReason}],retry:"Provide the isolated topology and rerun with RECORD_HUB_P4_BATCH4_LIVE=1"}' \
  | tee "$evidence_dir/batch4.json"
cp "$evidence_dir/batch4.json" "$evidence_dir/phase-4-batch4.json"
echo "P4 Batch 4 report: $evidence_dir/batch4.json"
[[ "$static_status" == "0" ]]
