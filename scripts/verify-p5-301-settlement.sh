#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
evidence_dir="${RECORD_HUB_P5_SETTLEMENT_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-301-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go shasum cmp; do command -v "$command" >/dev/null || { echo "P5-301: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-301-settlement-spec.json"
manifest="$root_dir/contracts/settlement/manifest.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-301" and .liveRequired == true and .schemaVersion == 1 and (.requiredInvariants | length) == 6' "$spec" >/dev/null
jq -e '.version == 1 and .contractFamily == "settlement-confirmation" and (.entries | length) == 3 and (.forbiddenProjectionFields | length) >= 5' "$manifest" >/dev/null

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

check schema-fixture-parity bash -c "
  cmp '$root_dir/contracts/settlement/confirmation/v1/settlement-confirmation-request.schema.json' '$approver_root/contracts/settlement/confirmation/v1/settlement-confirmation-request.schema.json' &&
  cmp '$root_dir/contracts/settlement/confirmation/v1/settlement-confirmation-result.schema.json' '$approver_root/contracts/settlement/confirmation/v1/settlement-confirmation-result.schema.json' &&
  test \"\$(shasum -a 256 '$root_dir/contracts/settlement/confirmation/v1/settlement-confirmation-request.schema.json' | cut -d' ' -f1)\" = \"\$(jq -r '.entries[] | select(.kind==\"request\") | .schemaSha256' '$manifest')\" &&
  test \"\$(shasum -a 256 '$root_dir/contracts/settlement/confirmation/v1/settlement-confirmation-result.schema.json' | cut -d' ' -f1)\" = \"\$(jq -r '.entries[] | select(.kind==\"result\") | .schemaSha256' '$manifest')\"
"

check approver-settlement-boundary bash -c "
  rg -q 'SettlementConfirmationRequestValidator|settlement.confirmation.requested@v1|snapshotHash' '$approver_root/approver-api/src/main/java' '$approver_root/approver-api/src/test/java' &&
  rg -q 'SettlementResultContractHandler|settlement.confirmation.result@v1|settlement.confirm@v1' '$approver_root/approver-application/src/main/java' '$approver_root/approver-application/src/test/java'
"

check safe-apply-schema bash -c "
  jq -e '.additionalProperties == false and .properties.action.additionalProperties == false' '$root_dir/contracts/settlement/confirmation/v1/apply-evidence.schema.json' >/dev/null &&
  ! rg -n -i 'grossAmount|netAmount|bankAccount|invoiceAttachment|sealedData' '$root_dir/contracts/settlement/confirmation/v1/apply-evidence.schema.json' &&
  rg -q 'forbiddenProjectionFields|sensitiveFieldsExcluded|no-money|不.*金额|不.*银行' '$manifest' '$root_dir/docs/phase-5-design.md'
"

check hash-scope-version-contract bash -c "
  rg -q 'snapshotHash|decisionVersion|expected.*version|idempot|late|reconcil' '$approver_root/approver-api/src/main/java' '$approver_root/approver-application/src/main/java' &&
  rg -q 'P5-301|Settlement|settlement.confirm' '$root_dir/docs/phase-5-requirements.md' '$root_dir/docs/phase-5-design.md'
"

check phase5-contract rg -q 'P5-INT-003|Settlement|Apply|敏感|金额|银行|附件|sealed' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md" "$root_dir/docs/phase-5-design.md"

live_requested="${RECORD_HUB_P5_SETTLEMENT_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires live Settlement owner Apply endpoint/workflow, Approver result relay, Record Hub safe association, and immutable evidence export"
required=(RECORD_HUB_P5_SETTLEMENT_OWNER_URL RECORD_HUB_P5_SETTLEMENT_TOPOLOGY_FILE RECORD_HUB_P5_SETTLEMENT_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="Settlement live pilot runner is not installed in this workspace; execute owner Apply/idempotency/late-result harness"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg parity "$(static_value schema-fixture-parity)" --arg approver "$(static_value approver-settlement-boundary)" \
  --arg safe "$(static_value safe-apply-schema)" --arg hash "$(static_value hash-scope-version-contract)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-301",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-301-settlement-spec.json",manifest:"contracts/settlement/manifest.json",evidenceDir:$evidenceDir,static:{schemaFixtureParity:$parity,approverSettlementBoundary:$approver,safeApplySchema:$safe,hashScopeVersionContract:$hash,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision Settlement owner Apply/relay topology and rerun with RECORD_HUB_P5_SETTLEMENT_LIVE=1"}' \
  | tee "$evidence_dir/p5-301.json"
echo "P5-301 report: $evidence_dir/p5-301.json"
[[ "$status" == "0" ]]
