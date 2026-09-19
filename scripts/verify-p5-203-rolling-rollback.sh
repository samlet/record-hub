#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_ROLLBACK_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-203-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-203: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-203-rolling-rollback-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-203" and .liveRequired == true and (.sequence | length) == 8 and (.invariants | length) == 5' "$spec" >/dev/null

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

check release-artifacts bash -c "
  test -f '$root_dir/docs/phase-4-release-candidate-manifest.json' &&
  rg -q 'artifact|sha256|commit|digest|old/new|immutable' '$root_dir/docs/phase-4-release-candidate-manifest.json' '$root_dir/docs/phase-4-batch5-p4-500.md' '$root_dir/docs/phase-5-design.md'
"

check expand-contract-drain bash -c "
  rg -q 'additive DB migration|expand/contract|兼容 reader|feature flag|drain|在途' '$root_dir/docs/phase-3-acceptance-plan.md' '$root_dir/docs/phase-3-batch4h-upgrade-rollback.md' '$root_dir/docs/phase-4-design.md' '$root_dir/docs/phase-4-batch4-runbook.md'
"

check no-double-write-boundary bash -c "
  rg -q 'idempotency|Idempotency|CAS|revision|unique' '$root_dir/server/internal/modules/commands' '$root_dir/server/internal/modules/projection' '$root_dir/server/internal/modules/records' &&
  rg -q 'no-double-write|重复.*副作用|double.*side' '$root_dir/docs/phase-4-batch5-p4-504.md' '$root_dir/docs/phase-4-batch4-runbook.md' '$root_dir/docs/phase-5-design.md'
"

check rollback-runbook bash -c "
  rg -q '停止新 publisher|切回旧 artifact|恢复旧 feature flag|不.*删除 Inbox/Outbox|reconciliation' '$root_dir/docs/phase-4-batch4-runbook.md' &&
  test -x '$root_dir/scripts/verify-p3-upgrade-rollback.sh'
"

check phase5-contract rg -q 'P5-PLAT-004|P5-OPS-002|rollback|feature flag|old worker|兼容' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md" "$root_dir/docs/phase-5-design.md"

live_requested="${RECORD_HUB_P5_ROLLBACK_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires immutable old/new artifacts, isolated four-owner upgrade targets, operator credentials, and rollback evidence export"
required=(RECORD_HUB_P5_OLD_ARTIFACT_ROOT RECORD_HUB_P5_NEW_ARTIFACT_ROOT RECORD_HUB_P5_ROLLBACK_TOPOLOGY_FILE RECORD_HUB_P5_ROLLBACK_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="rolling upgrade runner is not installed in this workspace; execute the approved expand/contract and rollback harness and attach evidence"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg artifacts "$(static_value release-artifacts)" --arg expand "$(static_value expand-contract-drain)" \
  --arg doubleWrite "$(static_value no-double-write-boundary)" --arg runbook "$(static_value rollback-runbook)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-203",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-203-rolling-rollback-spec.json",evidenceDir:$evidenceDir,static:{releaseArtifacts:$artifacts,expandContractDrain:$expand,noDoubleWriteBoundary:$doubleWrite,rollbackRunbook:$runbook,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision immutable old/new artifacts and isolated upgrade targets, then rerun with RECORD_HUB_P5_ROLLBACK_LIVE=1"}' \
  | tee "$evidence_dir/p5-203.json"
echo "P5-203 report: $evidence_dir/p5-203.json"
[[ "$status" == "0" ]]
