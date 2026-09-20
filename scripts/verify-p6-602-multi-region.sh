#!/usr/bin/env bash
set -Eeuo pipefail

# P6-602: validate the multi-region decision record and optional evidence. It
# never changes topology or claims active-active readiness.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-602-multi-region-spec.json"
output="${RECORD_HUB_P6_MULTI_REGION_OUTPUT:-$root_dir/docs/phase-6-multi-region.json}"
live_report="${RECORD_HUB_P6_MULTI_REGION_LIVE_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-602 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-602" and
  .liveTraffic == false and .decision.default == "SINGLE_REGION_HA" and
  .decision.goNoGo == "NO_GO_UNTIL_EVIDENCE" and
  .decision.zeroRpoClaim == "FORBIDDEN_WITHOUT_LIVE_PROOF" and
  .targets.measurementRequired == true and
  .targets.unverifiedAction == "DO_NOT_COMMIT_TO_TARGET" and
  .boundaries.crossRegionXa == "NOT_ASSUMED" and
  .missingAction == "BLOCKED_BY_LIVE_MULTI_REGION_EVIDENCE"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:4])
live_path = pathlib.Path(sys.argv[4]) if sys.argv[4] else None
spec = json.loads(spec_path.read_text())

def has(pattern, *paths):
    result = subprocess.run(["rg", "-q", pattern, *map(str, paths)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return result.returncode == 0

checks = {
    "singleRegionDefault": has(r"single.?region|多地域.*不|active-active.*不|SINGLE_REGION_HA", root / "docs/phase-5-design.md", root / "docs/phase-6-design.md", spec_path),
    "rpoRtoContract": has(r"RPO|RTO|15.*分钟|60.*分钟|rpoMinutes|rtoMinutes", root / "docs/phase-4-requirements.md", root / "docs/phase-5-requirements.md", root / "docs/phase-5-acceptance-plan.md", spec_path),
    "capacityEvidenceBoundary": has(r"P6-600|mixed.?load|capacity|SLO", root / "docs/phase-6-task-breakdown.md", root / "docs/phase-6-mixed-load.json", root / "docs/phase-6-acceptance-plan.md"),
    "mongoNatsBoundary": has(r"Mongo|NATS|JetStream|replica|PITR", root / "docs/phase-5-design.md", root / "docs/phase-5-batch2-p5-200.md", root / "docs/phase-5-batch2-p5-201.md"),
    "workflowOwnerBoundary": has(r"Temporal|Conductor|owner|所有权", root / "docs/phase-6-design.md", root / "docs/phase-6-requirements.md"),
    "operatorRunbook": has(r"stop|drain|rollback|reconciliation", root / "docs/phase-6-operator-runbook.json", root / "docs/phase-4-batch4-runbook.md"),
    "noImplicitPass": has(r"NO_GO_UNTIL_EVIDENCE|UNVERIFIED|DO_NOT_COMMIT", spec_path, root / "docs/phase-6-acceptance-plan.md"),
}
missing = [name for name, value in checks.items() if not value]
live_status = "BLOCKED"
live_reason = "no immutable multi-region capacity/RPO/RTO/cost/signoff report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status = "PASS"
            live_reason = "multi-region decision evidence is PASS"
        else:
            live_reason = "multi-region report exists but is not live PASS"
    except Exception as error:
        live_reason = f"multi-region report is invalid: {error}"

status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-602",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "decision": spec["decision"],
    "requiredEvidence": spec["requiredEvidence"],
    "targets": spec["targets"],
    "boundaries": spec["boundaries"],
    "goNoGo": "NO_GO" if status != "PASS_STATIC" else "REVIEW_REQUIRED",
    "prerequisite": spec["prerequisite"],
    "next": "Measure mixed-load capacity, RPO/RTO, failover, cost and operator recovery in an approved topology, attach owner signoff, then revisit the go/no-go decision.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-602", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
