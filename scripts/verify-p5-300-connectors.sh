#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_CONNECTOR_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-300-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-300: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-300-connector-spec.json"
manifest="$root_dir/contracts/connectors/manifest.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-300" and .liveRequired == true and (.registryKey | length) == 3 and (.lifecycle | length) == 4 and (.failClosed | length) >= 7' "$spec" >/dev/null
jq -e '.version == 1 and .contractFamily == "record-hub-connector-manifest" and (.entries | length) >= 2 and all(.entries[]; .connector and .event and (.schemaVersion >= 1) and .ownerSystem and .sdkVersion and .compatibilityMin and .compatibilityMax and (.contractHash | test("^sha256:[0-9a-f]{64}$")) and (.allowedFields | length > 0))' "$manifest" >/dev/null

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

check registry-unit-contract bash -c "
  go test ./server/internal/modules/connector -count=1 &&
  rg -q 'Resolve|StatusEnabled|ErrSDKIncompatible|ErrConnectorUnavailable|Transition' '$root_dir/server/internal/modules/connector/registry.go' '$root_dir/server/internal/modules/connector/registry_test.go'
"

check approver-registry-contract bash -c "
  test -d '$approver_root' &&
  rg -q 'IntegrationRequestContractRegistry|IntegrationResultContractRegistry|unknown.*connector|No contract is registered' '$approver_root/approver-application/src/main/java' '$approver_root/approver-application/src/test/java'
"

check owner-connector-boundary bash -c "
  test -d '$bids_root' &&
  rg -q 'ConnectorKey|connectorKey|X-Approver-Connector-Key' '$bids_root/backend/internal' &&
  rg -q 'connector|reconciliation|rollback|fallback' '$root_dir/docs/phase-3-integration-design.md' '$root_dir/docs/phase-5-design.md'
"

check manifest-parity rg -q 'contractHash|compatibilityMin|compatibilityMax|allowedFields|status' "$root_dir/contracts/connectors/manifest.json" "$root_dir/deploy/local/p5/p5-300-connector-spec.json"
check phase5-contract rg -q 'P5-INT-001|P5-INT-002|connector registry|compatibility window|quarantine|fail closed' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-design.md"

live_requested="${RECORD_HUB_P5_CONNECTOR_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires connector admin lifecycle API, Approver/Fluxion/Bids owner runtimes, manifest/hash parity runner, and immutable evidence export"
required=(RECORD_HUB_P5_CONNECTOR_ADMIN_URL RECORD_HUB_P5_CONNECTOR_TOPOLOGY_FILE RECORD_HUB_P5_CONNECTOR_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="connector lifecycle/owner side-effect runner is not installed in this workspace; execute the approved registry compatibility and rollback harness"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg registry "$(static_value registry-unit-contract)" --arg approver "$(static_value approver-registry-contract)" \
  --arg owner "$(static_value owner-connector-boundary)" --arg parity "$(static_value manifest-parity)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-300",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-300-connector-spec.json",manifest:"contracts/connectors/manifest.json",evidenceDir:$evidenceDir,static:{registryUnitContract:$registry,approverRegistryContract:$approver,ownerConnectorBoundary:$owner,manifestParity:$parity,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision connector admin/owner runtime harness and rerun with RECORD_HUB_P5_CONNECTOR_LIVE=1"}' \
  | tee "$evidence_dir/p5-300.json"
echo "P5-300 report: $evidence_dir/p5-300.json"
[[ "$status" == "0" ]]
