#!/usr/bin/env bash
set -Eeuo pipefail

# P6-CLOSURE: aggregate all Phase 6 task reports and the task breakdown. This
# is a static consistency gate; it never upgrades a blocked task to PASS.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-closure-spec.json"
breakdown="$root_dir/docs/phase-6-task-breakdown.md"
output="${RECORD_HUB_P6_CLOSURE_OUTPUT:-$root_dir/docs/phase-6-closure.json}"

for command in date git jq python3; do
  command -v "$command" >/dev/null || { echo "P6-CLOSURE missing command: $command" >&2; exit 2; }
done

jq -e '.version == 1 and .phase == "6" and .task == "P6-CLOSURE" and .liveTraffic == false and .failClosed == true and .decision == "DO_NOT_START_LIVE_CONNECTORS" and .missingAction == "BLOCKED_BY_GATE_MATRIX"' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$breakdown" "$output" <<'PY'
import json
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, breakdown_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())
breakdown = breakdown_path.read_text()
rows = []
errors = []

def table_status(task):
    match = re.search(rf"^\|\s*{re.escape(task)}\s*\|(?P<body>[^\n]+)$", breakdown, re.MULTILINE)
    if not match:
        return None
    cells = [cell.strip() for cell in match.group(0).strip().strip("|").split("|")]
    return cells[-1] if cells else None

p6_000_status = table_status("P6-000")
if p6_000_status != spec["p6-000"]["breakdownStatus"]:
    errors.append({"task": "P6-000", "kind": "breakdown-status", "expected": spec["p6-000"]["breakdownStatus"], "actual": p6_000_status})

for task, expected in spec["reports"].items():
    breakdown_status = table_status(task)
    if breakdown_status != expected["breakdownStatus"]:
        errors.append({"task": task, "kind": "breakdown-status", "expected": expected["breakdownStatus"], "actual": breakdown_status})
    report_path = root / expected["file"]
    if not report_path.is_file():
        errors.append({"task": task, "kind": "missing-report", "file": expected["file"]})
        continue
    try:
        report = json.loads(report_path.read_text())
    except Exception as error:
        errors.append({"task": task, "kind": "invalid-report", "error": str(error)})
        continue
    if report.get("task") != task:
        errors.append({"task": task, "kind": "report-task", "actual": report.get("task")})
    if report.get("status") != expected["reportStatus"]:
        errors.append({"task": task, "kind": "report-status", "expected": expected["reportStatus"], "actual": report.get("status")})
    rows.append({"task": task, "breakdownStatus": breakdown_status, "reportStatus": report.get("status"), "file": expected["file"]})

counts = {}
for row in rows:
    counts[row["breakdownStatus"]] = counts.get(row["breakdownStatus"], 0) + 1
blocked = [row for row in rows if row["breakdownStatus"] != "DONE"]
status = "PASS_STATIC_WITH_BLOCKERS" if not errors and blocked else ("PASS_STATIC" if not errors else "FAIL")
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-CLOSURE",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "sourceOfTruth": spec["sourceOfTruth"],
    "counts": counts,
    "tasks": rows,
    "blockedTasks": [row["task"] for row in blocked],
    "errors": errors,
    "decision": spec["decision"],
    "next": [
        "Clear the Phase 5/P6-000 prerequisite before live connector traffic.",
        "Implement the P6-500/P6-501 control-plane gaps and SDK/identity gaps.",
        "Provision native connector, workflow, event, capacity, restore and rollback evidence runners.",
    ],
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-CLOSURE", "status": status, "blocked": len(blocked), "errors": len(errors)}, ensure_ascii=False))
if errors:
    raise SystemExit(1)
PY

cat "$output"
