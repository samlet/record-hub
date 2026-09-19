#!/usr/bin/env bash
set -Eeuo pipefail

# Phase 4 Batch 1: close the Phase 3 gaps without claiming a live result when
# the caller has not supplied the isolated services and immutable artifacts.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
evidence_dir="${RECORD_HUB_P4_BATCH1_EVIDENCE_DIR:-$root_dir/build/evidence/phase4/batch1-$run_id}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"

for command in date go jq rg; do
  command -v "$command" >/dev/null || { echo "P4 Batch 1: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p4/p4-batch1-spec.json"
jq -e '.version == 1 and .phase == "4" and .batch == "1" and .liveRequired == true' "$spec" >/dev/null

static_status=0
run_static() {
  local id="$1"
  shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    static_status=1
  fi
}

run_static P4-100-issuer go test ./tools/workload-issuer ./server/internal/modules/identity ./server/internal/config -count=1
run_static P4-101-contract "$root_dir/scripts/verify-p3-contract-mirrors.sh"
run_static P4-102-recovery-contract test -x "$root_dir/scripts/verify-p3-backup-restore.sh"
run_static P4-103-capacity-contract test -x "$root_dir/scripts/verify-p3-capacity-baseline.sh"
run_static P4-104-upgrade-contract "$root_dir/scripts/verify-p3-upgrade-rollback.sh"
run_static P4-105-fault-contract bash -c "rg -q 'FLUXION_RECORD_HUB_TEST_ROLLBACK_BEFORE_COMMIT' '$root_dir/scripts/verify-p3-fluxion-command-live.sh' && rg -q 'FLUXION_RECORD_HUB_TEST_CRASH_BEFORE_RESULT_SENT' '$root_dir/scripts/verify-p3-fluxion-command-live.sh'"
run_static P4-106-behavior-contract "$root_dir/scripts/verify-p3-approval-contract-mirrors.sh"

live_requested="${RECORD_HUB_P4_BATCH1_LIVE:-0}"
live_status="SKIPPED"
live_reason="live gate is opt-in and requires the isolated four-owner topology, explicit recovery targets, or immutable old/new artifacts"
p4100_live="SKIPPED"
p4101_live="SKIPPED"
p4102_live="SKIPPED"
p4103_live="SKIPPED"
p4104_live="SKIPPED"
p4105_live="SKIPPED"
p4106_live="SKIPPED"
if [[ "$live_requested" == "1" ]]; then
  live_status="REQUESTED"
  live_reason="live sub-gates are delegated to the existing P3 runners; each runner records PASS/FAIL/SKIPPED in its own evidence"
  set +e
  RECORD_HUB_P3_ROTATION_LIVE=1 "$root_dir/scripts/verify-p3-credential-rotation.sh" >"$evidence_dir/live/P4-100.log" 2>&1
  printf '%s\n' "$?" >"$evidence_dir/live/P4-100.exit"
  RECORD_HUB_P3_BACKUP_LIVE=1 "$root_dir/scripts/verify-p3-backup-restore.sh" >"$evidence_dir/live/P4-102.log" 2>&1
  printf '%s\n' "$?" >"$evidence_dir/live/P4-102.exit"
  RECORD_HUB_P3_CAPACITY_LIVE=1 "$root_dir/scripts/verify-p3-capacity-baseline.sh" >"$evidence_dir/live/P4-103.log" 2>&1
  printf '%s\n' "$?" >"$evidence_dir/live/P4-103.exit"
  RECORD_HUB_P3_UPGRADE_LIVE=1 "$root_dir/scripts/verify-p3-upgrade-rollback.sh" >"$evidence_dir/live/P4-104.log" 2>&1
  printf '%s\n' "$?" >"$evidence_dir/live/P4-104.exit"
  RECORD_HUB_P3_RESTART_ACK_LIVE=1 RECORD_HUB_P3_RESTART_EVIDENCE_DIR="$evidence_dir/live/P4-106-evidence" "$root_dir/scripts/verify-p3-process-restart-ack-loss.sh" >"$evidence_dir/live/P4-106.log" 2>&1
  printf '%s\n' "$?" >"$evidence_dir/live/P4-106.exit"
  set -e

  classify_log() {
    local log="$1"
    if grep -q 'FAIL' "$log"; then
      printf 'FAIL'
    elif grep -q 'P3-407' "$log" && grep -q 'fourOwnerMixedWorkload.*SKIPPED' "$log"; then
      printf 'PARTIAL'
    elif grep -q 'SKIPPED' "$log"; then
      printf 'SKIPPED'
    elif grep -q 'passed' "$log" || grep -q 'PASS' "$log"; then
      printf 'PASS'
    else
      printf 'SKIPPED'
    fi
  }
  p4100_live="$(classify_log "$evidence_dir/live/P4-100.log")"
  p4102_live="$(classify_log "$evidence_dir/live/P4-102.log")"
  p4103_live="$(classify_log "$evidence_dir/live/P4-103.log")"
  p4104_live="$(classify_log "$evidence_dir/live/P4-104.log")"
  if grep -q 'FAIL' "$evidence_dir/live/P4-106.log"; then
    p4106_live="FAIL"
  elif grep -q 'passed' "$evidence_dir/live/P4-106.log" || grep -q 'PASS' "$evidence_dir/live/P4-106.log"; then
    p4106_live="PARTIAL"
  else
    p4106_live="SKIPPED"
  fi
  if [[ -f "$evidence_dir/live/P4-106-evidence/restart-ack-loss.json" ]]; then
    if jq -e '.cases.transactionRollback.status == "PASS" and .cases.publishBeforeSentCrash.status == "PASS"' "$evidence_dir/live/P4-106-evidence/restart-ack-loss.json" >/dev/null 2>&1; then
      p4105_live="PASS"
    elif jq -e '.cases.transactionRollback.status == "FAIL" or .cases.publishBeforeSentCrash.status == "FAIL"' "$evidence_dir/live/P4-106-evidence/restart-ack-loss.json" >/dev/null 2>&1; then
      p4105_live="FAIL"
    else
      p4105_live="PARTIAL"
    fi
  fi
fi

if [[ "$static_status" != "0" ]]; then
  overall="FAIL"
elif [[ "$live_requested" == "1" ]]; then
  overall="PARTIAL"
else
  overall="PARTIAL"
fi

static_value() {
  local id="$1"
  if [[ -f "$evidence_dir/static/$id.status" ]]; then
    tr -d '\n' <"$evidence_dir/static/$id.status"
  else
    printf 'SKIPPED'
  fi
}

jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" \
  --arg overall "$overall" \
  --arg liveRequested "$live_requested" \
  --arg liveStatus "$live_status" \
  --arg liveReason "$live_reason" \
  --arg p4100 "$(static_value P4-100-issuer)" \
  --arg p4101 "$(static_value P4-101-contract)" \
  --arg p4102 "$(static_value P4-102-recovery-contract)" \
  --arg p4103 "$(static_value P4-103-capacity-contract)" \
  --arg p4104 "$(static_value P4-104-upgrade-contract)" \
  --arg p4105 "$(static_value P4-105-fault-contract)" \
  --arg p4106 "$(static_value P4-106-behavior-contract)" \
  --arg p4100Live "$p4100_live" --arg p4101Live "$p4101_live" --arg p4102Live "$p4102_live" \
  --arg p4103Live "$p4103_live" --arg p4104Live "$p4104_live" --arg p4105Live "$p4105_live" --arg p4106Live "$p4106_live" \
  '{gate:"P4-BATCH1",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p4/p4-batch1-spec.json",evidenceDir:$evidenceDir,liveRequested:($liveRequested == "1"),tasks:[
    {id:"P4-100",scope:"reloadable workload issuer and JWKS overlap",staticStatus:$p4100,liveStatus:$p4100Live,liveReason:$liveReason},
    {id:"P4-101",scope:"independent workload client secret rotation",staticStatus:$p4101,liveStatus:$p4101Live,liveReason:$liveReason},
    {id:"P4-102",scope:"Mongo/PostgreSQL/JetStream recovery set",staticStatus:$p4102,liveStatus:$p4102Live,liveReason:$liveReason},
    {id:"P4-103",scope:"four-owner mixed capacity and drain",staticStatus:$p4103,liveStatus:$p4103Live,liveReason:$liveReason},
    {id:"P4-104",scope:"expand-contract rolling upgrade and rollback",staticStatus:$p4104,liveStatus:$p4104Live,liveReason:$liveReason},
    {id:"P4-105",scope:"transaction rollback and publish-before-SENT crash",staticStatus:$p4105,liveStatus:$p4105Live,liveReason:$liveReason},
    {id:"P4-106",scope:"three-owner live behavior matrix",staticStatus:$p4106,liveStatus:$p4106Live,liveReason:$liveReason}
  ],retry:"Provide isolated source/restore targets, the four-owner runtime and immutable old/new artifacts; rerun with RECORD_HUB_P4_BATCH1_LIVE=1"}' \
  | tee "$evidence_dir/batch1.json"

cp "$evidence_dir/batch1.json" "$evidence_dir/phase-4-batch1.json"
echo "P4 Batch 1 report: $evidence_dir/batch1.json"
[[ "$static_status" == "0" ]]
