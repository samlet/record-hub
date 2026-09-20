#!/usr/bin/env bash
set -Eeuo pipefail

# P6-203: collect static fault-test markers and require an immutable live
# matrix before claiming connector readiness. No fault is injected here.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
spec="$root_dir/deploy/local/p6/p6-203-fault-matrix-spec.json"
output="${RECORD_HUB_P6_FAULT_MATRIX_OUTPUT:-$root_dir/docs/phase-6-fault-matrix.json}"
live_report="${RECORD_HUB_P6_FAULT_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-203 missing command: $command" >&2; exit 2; }
done
jq -e '.version == 1 and .phase == "6" and .task == "P6-203" and .liveTraffic == false and .failClosed == true and (.cases | length) == 6 and .liveEvidenceRequired == true and .missingAction == "BLOCKED_BY_LIVE_FAULT_EVIDENCE"' "$spec" >/dev/null

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, spec_path, output_path = map(pathlib.Path, sys.argv[1:7])
live_report = pathlib.Path(sys.argv[7]) if sys.argv[7] else None
spec = json.loads(spec_path.read_text())
roots = [record_hub, approver, fluxion, bids]
case_markers = {
    "duplicate": r"duplicate|idempotent|replay",
    "late-result": r"late[- ]result|out[- ]of[- ]order|stale result|source version",
    "owner-restart": r"restart|resum(e|ption)|recovery",
    "result-relay-outage": r"outage|relay|NATS|timeout",
    "dlq": r"dead.?letter|DLQ|dead_letter",
    "rollback": r"rollback|fallback|disable.*traffic",
}
excluded = {"target", "build", "node_modules", ".next", "dist", ".git"}
static = {}
for case, marker in case_markers.items():
    matched = False
    for root in roots:
        if not root.exists():
            continue
        for path in root.rglob("*"):
            if not path.is_file() or any(part in excluded for part in path.parts):
                continue
            if path.suffix not in {".go", ".java", ".kt", ".kts", ".md", ".sh", ".json", ".yaml", ".yml"}:
                continue
            try:
                if re.search(marker, path.read_text(errors="ignore"), re.IGNORECASE):
                    matched = True
                    break
            except OSError:
                pass
        if matched:
            break
    static[case] = "PASS" if matched else "UNVERIFIED"

live_status = "BLOCKED"
live_reason = "no immutable live fault report supplied"
live_cases = []
if live_report is not None and live_report.is_file():
    try:
        report = json.loads(live_report.read_text())
        live_cases = report.get("cases", [])
        if report.get("status") in {"PASS", "DONE"} and len(live_cases) == len(spec["cases"]) and all(item.get("liveStatus") == "PASS" for item in live_cases):
            live_status = "PASS"
            live_reason = "all six live fault cases are PASS"
        else:
            live_reason = "live report exists but one or more cases are not PASS"
    except Exception as error:
        live_reason = f"live report is invalid: {error}"

missing_static = [case for case, status in static.items() if status != "PASS"]
status = "PASS_STATIC" if live_status == "PASS" and not missing_static else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(record_hub), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-203",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "cases": spec["cases"],
    "static": static,
    "missingStatic": missing_static,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_report) if live_report is not None else "", "cases": live_cases},
    "requiredEvidenceFields": spec["requiredEvidenceFields"],
    "decision": "DO_NOT_ENABLE_CONNECTORS" if status != "PASS_STATIC" else "STATIC_MATRIX_READY_FOR_SEPARATE_RELEASE_GATE",
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Run each case in isolated native owner topology, attach redacted evidence and operator rollback audit, then rerun with RECORD_HUB_P6_FAULT_REPORT.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-203", "status": status, "liveStatus": live_status, "missingStatic": missing_static}, ensure_ascii=False))
PY
cat "$output"
