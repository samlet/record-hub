#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_ROLLOUT_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-501-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-501: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-501-rollout-spec.json"
candidate_spec="$root_dir/deploy/local/p5/p5-500-ga-candidate-spec.json"
template="$root_dir/deploy/local/p5/p5-402-ga-report-template.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-501" and .liveRequired == true and .prerequisite == "P5-500-candidate-pass" and .rollout.strategy == "staged-expansion" and .rollout.initialScope == "one-approved-tenant-workspace" and .rollout.rollbackWindow == "explicit-and-audited" and (.stopConditions | length) >= 6 and (.forbidden | length) >= 4 and .currentDecision == "DO_NOT_ROLLOUT"' "$spec" >/dev/null

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

check candidate-prerequisite bash -c "
  jq -e '.failClosed == true and .currentDecision == \"DO_NOT_CREATE_GA_CANDIDATE\"' '$candidate_spec' >/dev/null &&
  jq -e '.status == \"PENDING\" and .decision == \"DO_NOT_START_PRODUCTION_GA\" and all(.gates[]; .status != \"PASS\")' '$template' >/dev/null &&
  rg -q 'P5-500|candidate|不得.*rollout|不得.*生产' '$root_dir/docs/phase-5-batch5-p5-500.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check staged-expansion-and-audit bash -c "
  rg -q 'staged|灰度|one.*tenant|一个.*tenant|observation|观察窗口|audit|审计|rollback|回滚' '$spec' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-5-batch4-p5-401.md' '$root_dir/docs/phase-4-batch4-runbook.md' &&
  rg -q 'P5-G7|rollback|signoff|签字|artifact' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-batch4-p5-402.md'
"

check stop-and-legacy-safety bash -c "
  rg -q '停止 publisher|切回旧 artifact|保留旧 worker|KEEP|DO_NOT_ROLLOUT' '$root_dir/docs/phase-4-batch4-runbook.md' '$root_dir/docs/phase-5-batch4-p5-403.md' '$spec' &&
  ! rg -n -g '!verify-p5-501-rollout.sh' 'kubectl apply|helm upgrade|docker compose.*prod|rollout.*all.*tenant' '$root_dir/scripts' '$root_dir/deploy/local/p5'
"

check owner-commit-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_ROLLOUT_LIVE:-0}"
live_status="SKIPPED"
live_reason="blocked until P5-500 candidate PASS and all gates/signoffs; this preflight never starts production rollout"
required=(RECORD_HUB_P5_ROLLOUT_CANDIDATE_FILE RECORD_HUB_P5_ROLLOUT_TOPOLOGY_FILE RECORD_HUB_P5_ROLLOUT_AUDIT_ROOT RECORD_HUB_P5_ROLLOUT_SIGNOFF_FILE)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="rollout runner is not installed in this workspace; verify staged expansion and rollback window manually under release owner control"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() {
  [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'
}
overall="BLOCKED"
[[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg candidate "$(static_value candidate-prerequisite)" \
  --arg staged "$(static_value staged-expansion-and-audit)" \
  --arg safety "$(static_value stop-and-legacy-safety)" \
  --arg commits "$(static_value owner-commit-boundary)" \
  '{task:"P5-501",status:$overall,decision:"DO_NOT_ROLLOUT",generatedAt:$generatedAt,spec:"deploy/local/p5/p5-501-rollout-spec.json",evidenceDir:$evidenceDir,static:{candidatePrerequisite:$candidate,stagedExpansionAndAudit:$staged,stopAndLegacySafety:$safety,ownerCommitBoundary:$commits},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Complete P5-500 candidate and all gates/signoffs; rerun before any staged production expansion"}' \
  | tee "$evidence_dir/p5-501.json"
echo "P5-501 report: $evidence_dir/p5-501.json"
[[ "$status" == "0" ]]
