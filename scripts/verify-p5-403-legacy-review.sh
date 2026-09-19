#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_LEGACY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-403-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-403: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-403-legacy-review-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-403" and .liveRequired == true and .decision == "KEEP" and (.inventory | length) == 4 and (.keepUntil | length) >= 4 and (.removalRequirements | length) >= 5 and (.forbiddenActions | length) >= 4' "$spec" >/dev/null

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

check existing-keep-decision bash -c "
  rg -q 'KEEP|保留旧 worker|旧 worker|旧 schema reader|local human-task fallback|rollback artifact' '$root_dir/docs/phase-4-batch5-p4-504.md' '$root_dir/docs/phase-4-batch5-closure.md' '$root_dir/docs/phase-5-design.md' &&
  rg -q '独立评审|未批准|不得删除|保留' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-requirements.md'
"

check drain-before-removal bash -c "
  rg -q 'in-flight|drain|撤销凭据|在途|rollback|回滚' '$root_dir/docs/phase-4-batch2-p4-200.md' '$root_dir/docs/phase-4-batch2-p4-203.md' '$root_dir/docs/phase-4-batch4-runbook.md' &&
  rg -q 'P4-501|P5-G1|P5-G7|observation|观察窗口|signoff|签字' '$root_dir/docs/phase-5-batch4-p5-401.md' '$root_dir/docs/phase-5-batch4-p5-402.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check removal-gates-and-owner-boundary bash -c "
  jq -e '(.removalRequirements | index(\"replacement-contract-parity\")) and (.removalRequirements | index(\"no-inflight-request\")) and (.removalRequirements | index(\"rollback-drill-success\")) and (.removalRequirements | index(\"independent-owner-approval\"))' '$spec' >/dev/null &&
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

check no-destructive-removal bash -c "
  ! rg -n -g '!p5-403-legacy-review-spec.json' 'delete.*old worker|remove.*local fallback|revoke.*before.*drain|discard.*rollback' '$root_dir/docs' '$root_dir/deploy/local/p5' &&
  rg -q 'DO_NOT_START_PRODUCTION_GA|KEEP|保留' '$root_dir/deploy/local/p5/p5-402-ga-report-template.json' '$spec'
"

live_requested="${RECORD_HUB_P5_LEGACY_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires independent four-owner legacy inventory, in-flight drain evidence, rollback drill, retention/reconciliation window, and signed removal review"
required=(RECORD_HUB_P5_LEGACY_INVENTORY_FILE RECORD_HUB_P5_LEGACY_EVIDENCE_ROOT RECORD_HUB_P5_LEGACY_SIGNOFF_FILE)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="legacy removal reviewer is not installed in this workspace; retain all fallback/rollback paths until the signed review"
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
  --arg keep "$(static_value existing-keep-decision)" \
  --arg drain "$(static_value drain-before-removal)" \
  --arg gates "$(static_value removal-gates-and-owner-boundary)" \
  --arg safe "$(static_value no-destructive-removal)" \
  '{task:"P5-403",status:$overall,decision:"KEEP",generatedAt:$generatedAt,spec:"deploy/local/p5/p5-403-legacy-review-spec.json",evidenceDir:$evidenceDir,static:{existingKeepDecision:$keep,drainBeforeRemoval:$drain,removalGatesAndOwnerBoundary:$gates,noDestructiveRemoval:$safe},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Complete signed independent review after P4-501/P5-G1..G7 and observation window; keep all legacy paths meanwhile"}' \
  | tee "$evidence_dir/p5-403.json"
echo "P5-403 report: $evidence_dir/p5-403.json"
[[ "$status" == "0" ]]
