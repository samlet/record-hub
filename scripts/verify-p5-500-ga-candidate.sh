#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_GA_CANDIDATE_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-500-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-500: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-500-ga-candidate-spec.json"
template="$root_dir/deploy/local/p5/p5-402-ga-report-template.json"
rc_manifest="$root_dir/docs/phase-4-release-candidate-manifest.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-500" and .liveRequired == true and .failClosed == true and .currentDecision == "DO_NOT_CREATE_GA_CANDIDATE" and (.candidateMustHave | length) >= 10 and (.forbiddenInheritedStatuses | sort) == ["PARTIAL", "PENDING", "SKIPPED", "UNVERIFIED"] and (.manifestRequired | length) >= 10' "$spec" >/dev/null

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

check mandatory-gate-contract bash -c "
  rg -q 'P5-G1.*P5-G7|全部.*PASS|无.*SKIPPED|P4-501' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-batch4-closure.md' '$root_dir/docs/phase-5-batch4-p5-402.md' &&
  jq -e '(.gates | length) == 7 and all(.gates[]; .status != \"PASS\") and .decision == \"DO_NOT_START_PRODUCTION_GA\"' '$template' >/dev/null
"

check no-candidate-created bash -c "
  ! test -e '$root_dir/docs/phase-5-ga-candidate-manifest.json' &&
  ! test -e '$root_dir/docs/phase-5-ga-candidate.json' &&
  jq -e '.live.status == \"SKIPPED\"' '$rc_manifest' >/dev/null
"

check manifest-fields-and-boundary bash -c "
  rg -q 'four-owner|artifact|SHA-256|contract|migration|config|topology|capacity|RPO|RTO|retention|rollback|risk|signoff' '$spec' '$root_dir/docs/phase-5-acceptance-plan.md' &&
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_GA_CANDIDATE_LIVE:-0}"
live_status="SKIPPED"
live_reason="blocked until P4-501 and P5-G1..G7 are independently PASS; this preflight never creates a candidate manifest"
required=(RECORD_HUB_P5_GA_CANDIDATE_GATE_REPORT RECORD_HUB_P5_GA_CANDIDATE_ARTIFACT_ROOT RECORD_HUB_P5_GA_CANDIDATE_SIGNOFF_FILE)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="candidate assembler is not installed in this workspace; do not create a manifest until the gate report proves no forbidden status"
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
  --arg mandatory "$(static_value mandatory-gate-contract)" \
  --arg candidate "$(static_value no-candidate-created)" \
  --arg manifest "$(static_value manifest-fields-and-boundary)" \
  '{task:"P5-500",status:$overall,decision:"DO_NOT_CREATE_GA_CANDIDATE",generatedAt:$generatedAt,spec:"deploy/local/p5/p5-500-ga-candidate-spec.json",evidenceDir:$evidenceDir,static:{mandatoryGateContract:$mandatory,noCandidateCreated:$candidate,manifestFieldsAndBoundary:$manifest},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Complete P4-501/P5-G1..G7 and formal signoff; rerun this fail-closed preflight before creating a GA candidate"}' \
  | tee "$evidence_dir/p5-500.json"
echo "P5-500 report: $evidence_dir/p5-500.json"
[[ "$status" == "0" ]]
