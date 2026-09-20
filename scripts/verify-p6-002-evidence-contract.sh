#!/usr/bin/env bash
set -Eeuo pipefail

# P6-002: validate the evidence envelope needed by later connector/schema
# batches. This is a static contract gate and never enables traffic.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="${RECORD_HUB_P6_EVIDENCE_CONTRACT_OUTPUT:-$root_dir/docs/phase-6-evidence-contract.json}"
for command in date git jq rg; do
  command -v "$command" >/dev/null || { echo "P6-002 missing command: $command" >&2; exit 2; }
done
spec="$root_dir/deploy/local/p6/p6-002-evidence-contract-spec.json"
jq -e '.version == 1 and .phase == "6" and .task == "P6-002" and .liveTraffic == false and .failClosed == true and (.requiredFields | length) >= 13 and (.forbiddenStatuses | sort) == ["PARTIAL", "PENDING", "SKIPPED", "UNVERIFIED"]' "$spec" >/dev/null

check() {
  local id="$1"; shift
  "$@" >/dev/null
  printf '%s\n' "$id"
}
check evidence-schema test -s "$root_dir/contracts/evidence/phase4-evidence-manifest-v1.schema.json"
check evidence-redaction rg -q 'redact|safe|sensitive|PII|sealed' "$root_dir/contracts/evidence/phase4-evidence-manifest-v1.schema.json" "$root_dir/docs/phase-6-requirements.md" "$spec"
check failure-boundary rg -q 'failureMatrix|rollback|operatorAudit|P6-OPS-003' "$root_dir/docs/phase-6-acceptance-plan.md" "$root_dir/docs/phase-6-task-breakdown.md" "$spec"
check no-traffic-boundary rg -q 'liveTraffic|NOT_GRANTED|不启动|not.*enable' "$root_dir/docs/phase-6-design.md" "$root_dir/docs/phase-6-requirements.md" "$root_dir/docs/phase-6-task-breakdown.md" "$spec"
check owner-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}' rev-parse --verify HEAD >/dev/null &&
  git -C '${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}' rev-parse --verify HEAD >/dev/null &&
  git -C '${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}' rev-parse --verify HEAD >/dev/null
"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson requiredFields "$(jq -c '.requiredFields' "$spec")" \
  '{schemaVersion:1,phase:"6",task:"P6-002",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,requiredFields:$requiredFields,forbiddenPayloads:["token","privateKey","PII","sealedBid","quote","bankAccount","invoiceAttachment","fullWorkflowSnapshot"],gates:{redaction:"REQUIRED",scopeMatrix:"REQUIRED",failureMatrix:"REQUIRED",rollback:"REQUIRED",operatorAudit:"REQUIRED",topology:"REQUIRED"},prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Attach one immutable envelope per P6 task; do not infer live PASS from this static contract."}' \
  >"$output"
cat "$output"
