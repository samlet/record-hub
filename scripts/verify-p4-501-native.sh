#!/usr/bin/env bash
set -Eeuo pipefail

# P4-501 native evidence runner.  It composes the isolated P3 four-owner
# supervisor and a fail-closed case manifest.  Readiness alone never clears
# P4-501: every G1..G5 row needs an attached immutable live report.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_root="${RECORD_HUB_P4_EVIDENCE_ROOT:-$root_dir/build/evidence/phase4/p4-501-native}"
evidence_dir="$evidence_root/run-$(date -u +%Y%m%dT%H%M%SZ)-$$"
topology_file="${RECORD_HUB_P4_TOPOLOGY_FILE:-}"
credentials_file="${RECORD_HUB_P4_OPERATOR_CREDENTIALS_FILE:-}"
old_artifacts="${RECORD_HUB_P4_OLD_ARTIFACT_ROOT:-}"
new_artifacts="${RECORD_HUB_P4_NEW_ARTIFACT_ROOT:-}"
mkdir -p "$evidence_dir"

required=(RECORD_HUB_P4_TOPOLOGY_FILE RECORD_HUB_P4_EVIDENCE_ROOT RECORD_HUB_P4_OPERATOR_CREDENTIALS_FILE RECORD_HUB_P4_OLD_ARTIFACT_ROOT RECORD_HUB_P4_NEW_ARTIFACT_ROOT)
missing=()
for variable in "${required[@]}"; do [[ -n "${!variable:-}" ]] || missing+=("$variable"); done

write_blocked() {
  local reason="$1"
  jq -S -n --arg reason "$reason" --arg evidenceDir "$evidence_dir" \
    --argjson missing "$(printf '%s\n' "${missing[@]:-}" | sed '/^$/d' | jq -R . | jq -s .)" \
    '{gate:"P4-501",status:"BLOCKED",liveRequested:true,evidenceDir:$evidenceDir,topology:{status:"SKIPPED"},cases:[
      {id:"P4-G1",liveStatus:"SKIPPED"},{id:"P4-G2",liveStatus:"SKIPPED"},{id:"P4-G3",liveStatus:"SKIPPED"},{id:"P4-G4",liveStatus:"SKIPPED"},{id:"P4-G5",liveStatus:"SKIPPED"}],
      reason:$reason,missingPrerequisites:$missing,retry:"Provide the five explicit P4-501 inputs and rerun make p4-501-native"}' \
    | tee "$evidence_dir/p4-501.json"
  printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/missing-prerequisites.txt"
}

if (( ${#missing[@]} > 0 )); then
  write_blocked "explicit topology, credential and old/new artifact inputs are required"
  exit 0
fi

for command in date git jq curl go mongod mongosh nats-server nc openssl psql temporal conductor mvn; do
  command -v "$command" >/dev/null || { write_blocked "missing native command: $command"; exit 0; }
done
[[ -f "$topology_file" ]] || { write_blocked "topology file does not exist"; exit 0; }
[[ -f "$credentials_file" ]] || { write_blocked "operator credential contract does not exist"; exit 0; }
[[ -d "$old_artifacts" && -d "$new_artifacts" && "$old_artifacts" != "$new_artifacts" ]] || { write_blocked "old/new artifact roots must be distinct existing directories"; exit 0; }

if ! jq -e '.version == 1 and .mode == "native-isolated" and (.owners | length) == 4 and (.ports | type == "object")' "$topology_file" >/dev/null 2>&1; then
  write_blocked "topology file is not a native-isolated four-owner contract"
  exit 0
fi
if ! jq -e '.version == 1 and (.owners | length) == 4 and (.mode != "plaintext")' "$credentials_file" >/dev/null 2>&1; then
  write_blocked "operator credential file must declare four scoped owners without plaintext mode"
  exit 0
fi
if [[ "$(find "$old_artifacts" -type f | wc -l | tr -d ' ')" -lt 6 || "$(find "$new_artifacts" -type f | wc -l | tr -d ' ')" -lt 6 ]]; then
  write_blocked "old/new artifact roots must each contain the six immutable owner artifacts"
  exit 0
fi

port() { jq -er ".ports.$1" "$topology_file"; }
export RECORD_HUB_P3_MONGO_PORT="$(port mongo)"
export RECORD_HUB_P3_NATS_PORT="$(port nats)"
export RECORD_HUB_P3_NATS_MONITOR_PORT="$(port natsMonitor)"
export RECORD_HUB_P3_DEX_PORT="$(port dex)"
export RECORD_HUB_P3_DEX_TELEMETRY_PORT="$(port dexTelemetry)"
export RECORD_HUB_P3_WORKLOAD_PORT="$(port workload)"
export RECORD_HUB_P3_TEMPORAL_PORT="$(port temporal)"
export RECORD_HUB_P3_TEMPORAL_UI_PORT="$(port temporalUI)"
export RECORD_HUB_P3_CONDUCTOR_PORT="$(port conductor)"
export RECORD_HUB_P3_RECORD_HUB_PORT="$(port recordHub)"
export RECORD_HUB_P3_APPROVER_API_PORT="$(port approverApi)"
export RECORD_HUB_P3_APPROVER_WORKER_PORT="$(port approverWorker)"
export RECORD_HUB_P3_FLUXION_PORT="$(port fluxion)"
export RECORD_HUB_P3_BIDS_API_PORT="$(port bidsApi)"
export RECORD_HUB_P3_FOUR_OWNER_LIVE=1
export RECORD_HUB_P3_READY_HOOK="$root_dir/scripts/p4-501-native-hook.sh"
export RECORD_HUB_P3_EVIDENCE_DIR="$evidence_dir/topology"

set +e
"$root_dir/scripts/verify-p3-four-owner-topology.sh" >"$evidence_dir/topology.log" 2>&1
topology_exit=$?
set -e

manifest="$evidence_dir/topology/manifest.json"
cases="$evidence_dir/topology/p4-501/cases.json"
if [[ "$topology_exit" != "0" || ! -f "$manifest" ]]; then
  jq -S -n --arg evidenceDir "$evidence_dir" --arg log "$evidence_dir/topology.log" \
    '{gate:"P4-501",status:"FAIL",liveRequested:true,evidenceDir:$evidenceDir,topology:{status:"FAIL",log:$log},cases:[{id:"P4-G1",liveStatus:"SKIPPED"},{id:"P4-G2",liveStatus:"SKIPPED"},{id:"P4-G3",liveStatus:"SKIPPED"},{id:"P4-G4",liveStatus:"SKIPPED"},{id:"P4-G5",liveStatus:"SKIPPED"}],decision:"DO_NOT_CLEAR_P4_501"}' \
    | tee "$evidence_dir/p4-501.json"
  exit 1
fi

topology_status="$(jq -r '.status' "$manifest")"
if [[ ! -f "$cases" ]]; then
  jq -S -n --arg evidenceDir "$evidence_dir" --arg topology "$topology_status" \
    '{gate:"P4-501",status:"BLOCKED",liveRequested:true,evidenceDir:$evidenceDir,topology:{status:$topology},cases:[{id:"P4-G1",liveStatus:"SKIPPED"},{id:"P4-G2",liveStatus:"SKIPPED"},{id:"P4-G3",liveStatus:"SKIPPED"},{id:"P4-G4",liveStatus:"SKIPPED"},{id:"P4-G5",liveStatus:"SKIPPED"}],decision:"DO_NOT_CLEAR_P4_501",reason:"supervisor readiness completed without a case manifest"}' \
    | tee "$evidence_dir/p4-501.json"
  exit 0
fi

jq -S --arg topology "$topology_status" --arg evidenceDir "$evidence_dir" \
  '. + {evidenceDir:$evidenceDir,liveRequested:true,topology:{status:$topology,manifest:(($evidenceDir)+"/topology/manifest.json")},decision:(if .status == "PASS" then "P4_501_READY_FOR_REAUDIT" else "DO_NOT_CLEAR_P4_501" end)}' \
  "$cases" | tee "$evidence_dir/p4-501.json"
if jq -e '.status == "PASS" and .topology.status == "PASS"' "$evidence_dir/p4-501.json" >/dev/null 2>&1; then
  exit 0
fi
exit 0
