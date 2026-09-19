#!/usr/bin/env bash
set -Eeuo pipefail

# P3-409: validate that the acceptance report records every Batch 4 gate with
# an explicit status and preserves the task-breakdown status as the source of
# truth. This gate validates documentation completeness, not Beta readiness.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
report="$root_dir/docs/phase-3-acceptance-report.md"
breakdown="$root_dir/docs/phase-3-task-breakdown.md"

python3 - "$report" "$breakdown" <<'PY'
import json
import pathlib
import re
import sys

report_path, breakdown_path = map(pathlib.Path, sys.argv[1:])
report = report_path.read_text()
breakdown = breakdown_path.read_text()
expected = {
    "P3-400": "DONE",
    "P3-401": "DONE",
    "P3-402": "DONE",
    "P3-403": "DONE",
    "P3-404": "DONE",
    "P3-405": "SKIPPED",
    "P3-406": "SKIPPED",
    "P3-407": "PARTIAL",
    "P3-408": "SKIPPED",
    "P3-409": "DONE",
}
for gate, status in expected.items():
    if not re.search(rf"\|\s*{gate}\s*\|\s*{status}\s*\|", report):
        raise SystemExit(f"acceptance report is missing {gate}={status}")
    if not re.search(rf"\|\s*{gate}\s*\|[^\n]*\|\s*{status}\s*\|", breakdown):
        raise SystemExit(f"task breakdown is missing {gate}={status}")
for heading in ("## 1. 结论", "## 2. Gate 结果与提交基线", "## 3. RPO/RTO 与容量解释", "## 4. 遗留风险、owner 与重试条件", "## 5. 下一步准入判定"):
    if heading not in report:
        raise SystemExit(f"acceptance report is missing section: {heading}")
if "SKIPPED" not in report or "PARTIAL" not in report or "重试条件" not in report:
    raise SystemExit("acceptance report must preserve skipped/partial scope and retry conditions")
print(json.dumps({"gate": "P3-409", "status": "PASS", "report": str(report_path), "gates": expected}, ensure_ascii=False, sort_keys=True))
PY
