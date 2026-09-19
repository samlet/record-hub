#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_GA_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-402-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-402: SKIPPED (missing command: $command)"; exit 0; }
done

template="$root_dir/deploy/local/p5/p5-402-ga-report-template.json"
rc_manifest="$root_dir/docs/phase-4-release-candidate-manifest.json"
jq -e '.schemaVersion == 1 and .phase == "5" and .task == "P5-402" and .status == "PENDING" and .decision == "DO_NOT_START_PRODUCTION_GA" and (.gates | length) == 7 and (all(.gates[]; (.id | test("^P5-G[1-7]$")) and .status == "PENDING")) and .prerequisites["p4-501"] == "SKIPPED" and .topology.status == "UNVERIFIED" and .capacity.status == "UNVERIFIED" and .recovery.rpo == "UNVERIFIED" and .recovery.rto == "UNVERIFIED" and (.signoff | length) >= 5' "$template" >/dev/null
jq -e '.schemaVersion == 1 and .gate == "P4-500" and (.artifacts.entries | length) == 6 and .live.status == "SKIPPED"' "$rc_manifest" >/dev/null

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

check controlled-template bash -c "
  jq -e '.status == \"PENDING\" and .decision == \"DO_NOT_START_PRODUCTION_GA\" and (.gates | length) == 7' '$template' >/dev/null &&
  rg -q 'PENDING|UNVERIFIED|DO_NOT_START_PRODUCTION_GA|P5-G1|P5-G7' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-requirements.md'
"

check rc-and-prerequisite bash -c "
  jq -e '.gate == \"P4-500\" and .artifacts.status == \"PASS\" and .live.status == \"SKIPPED\"' '$rc_manifest' >/dev/null &&
  rg -q 'P4-501.*SKIPPED|P4-501.*live|不得.*GA|不得.*生产' '$root_dir/docs/phase-4-batch5-closure.md' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-batch4-p5-401.md'
"

check ga-sections bash -c "
  rg -q 'topology|拓扑|capacity|容量|RPO|RTO|retention|rollback|risk|owner|签字|signoff' '$template' '$root_dir/docs/phase-5-acceptance-plan.md' &&
  rg -q 'P5-100|P5-200|P5-300|P5-400|P5-401' '$root_dir/docs/phase-5-task-breakdown.md'
"

check no-implicit-pass-and-commit-boundary bash -c "
  ! jq -e '(.status == \"PASS\") or any(.gates[]; .status == \"PASS\")' '$template' >/dev/null &&
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_GA_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires P4-501 all-live PASS, P5-G1..G7 evidence, immutable RC artifacts, canary window, risk owner review, and formal signoff"
required=(RECORD_HUB_P5_GA_EVIDENCE_ROOT RECORD_HUB_P5_GA_SIGNOFF_FILE RECORD_HUB_P5_GA_RC_MANIFEST)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="GA report live assembler is not installed in this workspace; do not change the controlled template until all gates and signoffs are independently verified"
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
  --arg template "$(static_value controlled-template)" \
  --arg prerequisite "$(static_value rc-and-prerequisite)" \
  --arg sections "$(static_value ga-sections)" \
  --arg boundary "$(static_value no-implicit-pass-and-commit-boundary)" \
  '{task:"P5-402",status:$overall,generatedAt:$generatedAt,template:"deploy/local/p5/p5-402-ga-report-template.json",rcManifest:"docs/phase-4-release-candidate-manifest.json",evidenceDir:$evidenceDir,static:{controlledTemplate:$template,rcAndPrerequisite:$prerequisite,gaSections:$sections,noImplicitPassAndCommitBoundary:$boundary},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Complete P4-501/P5-G1..G7 and formal signoff before replacing the controlled template"}' \
  | tee "$evidence_dir/p5-402.json"
echo "P5-402 report: $evidence_dir/p5-402.json"
[[ "$status" == "0" ]]
