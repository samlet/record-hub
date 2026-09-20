#!/usr/bin/env bash
set -Eeuo pipefail

# P6 workflow evidence collector. This deliberately does not rewrite the
# committed P6-400/P6-401 task reports: native evidence is collected first and
# remains release-blocked until the independent Phase 4/5 GA prerequisite is
# cleared by its own gate.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P6_WORKFLOW_EVIDENCE_ROOT:-$root_dir/build/evidence/phase6/workflow-live-$(date -u +%Y%m%dT%H%M%SZ)}"

for command in date git jq rg; do
  command -v "$command" >/dev/null || { echo "P6 workflow evidence missing command: $command" >&2; exit 2; }
done

mkdir -p "$evidence_dir"
RECORD_HUB_P3_EVIDENCE_DIR="$evidence_dir" \
RECORD_HUB_P3_WORKFLOW_E2E_REPLAY=1 \
RECORD_HUB_P3_WORKFLOW_E2E_LIVE=1 \
"$root_dir/scripts/verify-p3-workflow-e2e.sh"

workflow_report="$evidence_dir/workflow-e2e.json"
topology_manifest="$evidence_dir/manifest.json"
[[ -s "$workflow_report" && -s "$topology_manifest" ]] || {
  echo "P6 workflow evidence is missing the native workflow/topology report" >&2
  exit 1
}
! rg -n 'P3-WORKFLOW-SECRET-MARKER' "$evidence_dir/workflow-e2e" >/dev/null
jq -e '
  .gate == "P3-402" and
  .status == "PASS" and
  .liveStatus == "PASS" and
  .replay.status == "PASS" and
  .replay.temporal.sameSnapshot == true and
  .replay.temporal.sameHash == true and
  .replay.temporal.replayed == true and
  .replay.conductor.sameSnapshot == true and
  .replay.conductor.sameHash == true and
  .replay.conductor.replayed == true
' "$workflow_report" >/dev/null
jq -e '.gate == "P3-400/P3-401" and .status == "PASS" and (.commits | length == 4)' "$topology_manifest" >/dev/null

workflow_json="$(jq -c . "$workflow_report")"
topology_json="$(jq -c . "$topology_manifest")"
jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --arg evidenceDir "$evidence_dir" \
  --argjson workflow "$workflow_json" \
  --argjson topology "$topology_json" \
  '{schemaVersion:1,phase:"6",tasks:["P6-400","P6-401"],status:"PASS_NATIVE_EVIDENCE",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,topology:$topology,workflow:$workflow,releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"RETAIN_P6_TASK_BLOCKER_UNTIL_PHASE4_PHASE5_GA"}' \
  >"$evidence_dir/p6-workflow-evidence.json"

for task in P6-400 P6-401; do
  report_name="p6-400"
  [[ "$task" == "P6-401" ]] && report_name="p6-401"
  jq -n \
    --arg task "$task" \
    --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
    --arg evidenceDir "$evidence_dir" \
    --argjson workflow "$workflow_json" \
    --argjson topology "$topology_json" \
    '{schemaVersion:1,phase:"6",task:$task,status:"PASS",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,topology:$topology,workflow:$workflow,releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"EVIDENCE_READY_BUT_RETAIN_RELEASE_BLOCKER"}' \
    >"$evidence_dir/${report_name}-live-report.json"
done

cat "$evidence_dir/p6-workflow-evidence.json"
echo "P6-400/P6-401 native workflow evidence passed; committed task reports remain blocked by the independent P4/P5 GA gate"
