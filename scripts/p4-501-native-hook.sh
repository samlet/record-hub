#!/usr/bin/env bash
set -Eeuo pipefail

# Hook executed by the isolated P3 four-owner supervisor.  It deliberately
# does not mint credentials, mutate business rows, or infer a P4 PASS from
# process readiness.  Each P4 case must provide an immutable report whose
# status is PASS before it can be promoted by the native runner.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P3_EVIDENCE_DIR:?missing supervisor evidence directory}/p4-501"
mkdir -p "$evidence_dir/cases"

run_contract() {
  local name="$1"
  shift
  if "$@" >"$evidence_dir/cases/$name.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/cases/$name.contract.status"
  else
    printf 'FAIL\n' >"$evidence_dir/cases/$name.contract.status"
  fi
}

run_contract g1-identity go test ./server/internal/modules/identity ./tools/workload-issuer -count=1
run_contract g2-restore bash -c "test -x '$root_dir/scripts/verify-p3-backup-restore.sh'"
run_contract g3-capacity bash -c "test -x '$root_dir/scripts/verify-p3-capacity-baseline.sh'"
run_contract g4-rollback bash -c "test -x '$root_dir/scripts/verify-p3-upgrade-rollback.sh'"
run_contract g5-fault bash -c "test -x '$root_dir/scripts/verify-p3-process-restart-ack-loss.sh' && test -x '$root_dir/scripts/verify-p3-nats-outage-recovery.sh'"

case_status() {
  local id="$1" report_var="$2" report="${!report_var:-}"
  if [[ -n "$report" && -f "$report" ]] && jq -e '(.status == "PASS" or .status == "DONE") and ((.liveStatus // "PASS") == "PASS")' "$report" >/dev/null 2>&1; then
    printf 'PASS'
  else
    printf 'SKIPPED'
    if [[ -n "$report" && -f "$report" ]]; then
      cp "$report" "$evidence_dir/cases/$id-attached.json"
    fi
  fi
}

g1="$(case_status P4-G1 RECORD_HUB_P4_G1_REPORT)"
g2="$(case_status P4-G2 RECORD_HUB_P4_G2_REPORT)"
g3="$(case_status P4-G3 RECORD_HUB_P4_G3_REPORT)"
g4="$(case_status P4-G4 RECORD_HUB_P4_G4_REPORT)"
g5="$(case_status P4-G5 RECORD_HUB_P4_G5_REPORT)"

jq -S -n \
  --arg g1 "$g1" --arg g2 "$g2" --arg g3 "$g3" --arg g4 "$g4" --arg g5 "$g5" \
  --arg evidenceDir "$evidence_dir" \
  --arg g1Contract "$(tr -d '\n' <"$evidence_dir/cases/g1-identity.contract.status")" \
  --arg g2Contract "$(tr -d '\n' <"$evidence_dir/cases/g2-restore.contract.status")" \
  --arg g3Contract "$(tr -d '\n' <"$evidence_dir/cases/g3-capacity.contract.status")" \
  --arg g4Contract "$(tr -d '\n' <"$evidence_dir/cases/g4-rollback.contract.status")" \
  --arg g5Contract "$(tr -d '\n' <"$evidence_dir/cases/g5-fault.contract.status")" \
  '{gate:"P4-501",evidenceDir:$evidenceDir,cases:[
    {id:"P4-G1",name:"credential-and-jwks-rotation",contractStatus:$g1Contract,liveStatus:$g1,reason:(if $g1 == "PASS" then "attached immutable live report" else "no approved owner rotation/revocation report attached" end)},
    {id:"P4-G2",name:"restore-and-checkpoint-recovery",contractStatus:$g2Contract,liveStatus:$g2,reason:(if $g2 == "PASS" then "attached immutable live report" else "no isolated restore report attached" end)},
    {id:"P4-G3",name:"mixed-load-and-drain",contractStatus:$g3Contract,liveStatus:$g3,reason:(if $g3 == "PASS" then "attached immutable live report" else "no four-owner mixed-load report attached" end)},
    {id:"P4-G4",name:"rolling-upgrade-and-rollback",contractStatus:$g4Contract,liveStatus:$g4,reason:(if $g4 == "PASS" then "attached immutable live report" else "no old/new rolling rollback report attached" end)},
    {id:"P4-G5",name:"cross-owner-fault-matrix",contractStatus:$g5Contract,liveStatus:$g5,reason:(if $g5 == "PASS" then "attached immutable live report" else "no cross-owner fault matrix report attached" end)}
  ],status:(if ($g1 == "PASS" and $g2 == "PASS" and $g3 == "PASS" and $g4 == "PASS" and $g5 == "PASS") then "PASS" else "BLOCKED" end),decision:(if ($g1 == "PASS" and $g2 == "PASS" and $g3 == "PASS" and $g4 == "PASS" and $g5 == "PASS") then "P4_501_READY_FOR_REAUDIT" else "DO_NOT_CLEAR_P4_501" end)}' \
  >"$evidence_dir/cases.json"

echo "P4-501 native hook wrote $evidence_dir/cases.json"
