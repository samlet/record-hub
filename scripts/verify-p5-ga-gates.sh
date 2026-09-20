#!/usr/bin/env bash
set -Eeuo pipefail

# Aggregate the seven Phase 5 GA gates from immutable task reports.  This is
# intentionally fail-closed: static contract PASS never upgrades a skipped
# live gate, and no candidate/rollout artifact is created here.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="${RECORD_HUB_P5_GATES_OUTPUT:-$root_dir/docs/phase-5-ga-gates.json}"
evidence_root="${RECORD_HUB_P5_GATES_EVIDENCE_ROOT:-$root_dir/build/evidence/phase5}"
p4_report="${RECORD_HUB_P5_GATES_P4_REPORT:-}"
mkdir -p "$(dirname "$output")"

for command in date find git jq python3; do
  command -v "$command" >/dev/null || { echo "P5 GA gates missing command: $command" >&2; exit 2; }
done

if [[ -z "$p4_report" ]]; then
  p4_report="$(find "$root_dir/build/evidence/phase4" -type f -name 'p4-501.json' -print 2>/dev/null | sort -r | head -1 || true)"
fi

python3 - "$root_dir" "$output" "$p4_report" "$evidence_root" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, output, p4_report, evidence_root = map(pathlib.Path, sys.argv[1:])
gate_tasks = {
    "P5-G1": ["p5-100", "p5-101"],
    "P5-G2": ["p5-101", "p5-102"],
    "P5-G3": ["p5-200", "p5-201"],
    "P5-G4": ["p5-202", "p5-203"],
    "P5-G5": ["p5-300", "p5-301", "p5-302", "p5-303"],
    "P5-G6": ["p5-400"],
    "P5-G7": ["p5-401", "p5-402", "p5-403"],
}

def latest(task):
    paths = sorted(evidence_root.glob(f"**/{task}.json"))
    return paths[-1] if paths else None

def read(path):
    if not path or not path.is_file():
        return None
    try:
        return json.loads(path.read_text())
    except Exception:
        return None

gates = []
for gate, tasks in gate_tasks.items():
    rows = []
    for task in tasks:
        path = latest(task)
        report = read(path)
        static = report and report.get("static", {})
        static_pass = bool(static) and all(value == "PASS" for value in static.values())
        live = report.get("liveStatus", "SKIPPED") if report else "MISSING"
        rows.append({"task": task, "report": str(path.relative_to(root)) if path and path.is_relative_to(root) else str(path or ""), "staticStatus": "PASS" if static_pass else "FAIL", "liveStatus": live})
    static_status = "PASS" if rows and all(row["staticStatus"] == "PASS" for row in rows) else "FAIL"
    live_status = "PASS" if rows and all(row["liveStatus"] == "PASS" for row in rows) else ("MISSING" if any(row["liveStatus"] == "MISSING" for row in rows) else "SKIPPED")
    gates.append({"id": gate, "staticStatus": static_status, "liveStatus": live_status, "tasks": rows})

p4 = read(pathlib.Path(p4_report)) if p4_report else None
p4_status = p4.get("status", "MISSING") if p4 else "MISSING"
all_static = all(gate["staticStatus"] == "PASS" for gate in gates)
all_live = all(gate["liveStatus"] == "PASS" for gate in gates)
status = "PASS" if all_static and all_live and p4_status == "PASS" else "BLOCKED_BY_P4_501"
result = {
    "schemaVersion": 1,
    "phase": "5",
    "task": "P5-GATES",
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip(),
    "p4_501": {"status": p4_status, "report": str(pathlib.Path(p4_report).relative_to(root)) if p4_report and pathlib.Path(p4_report).is_relative_to(root) else p4_report},
    "gates": gates,
    "status": status,
    "decision": "READY_FOR_GA_REVIEW" if status == "PASS" else "DO_NOT_START_PRODUCTION_GA",
    "next": "Complete P4-501 G1..G5 live PASS and rerun all Phase 5 gate reports; no SKIPPED/PARTIAL status may be inherited.",
}
pathlib.Path(output).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P5-GATES", "status": status, "p4_501": p4_status, "staticPass": all_static, "livePass": all_live}, ensure_ascii=False))
PY

cat "$output"
