#!/usr/bin/env bash
set -Eeuo pipefail

# P6-300: validate the Approver safe projection contract without consuming
# owner events or writing a projection record.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
spec="$root_dir/deploy/local/p6/p6-300-approver-projection-spec.json"
output="${RECORD_HUB_P6_APPROVER_PROJECTION_OUTPUT:-$root_dir/docs/phase-6-approver-projection.json}"
live_report="${RECORD_HUB_P6_APPROVER_LIVE_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-300 missing command: $command" >&2; exit 2; }
done
jq -e '.version == 1 and .phase == "6" and .task == "P6-300" and .liveTraffic == false and .failClosed == true and .scope.crossTenantAction == "REJECT" and .reconciliation.gapAction == "FINDING_AND_REPLAY" and .missingAction == "BLOCKED_BY_LIVE_CONNECTOR"' "$spec" >/dev/null

python3 - "$root_dir" "$approver_root" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, approver, spec_path, output_path = map(pathlib.Path, sys.argv[1:5])
live_path = pathlib.Path(sys.argv[5]) if sys.argv[5] else None
spec = json.loads(spec_path.read_text())
checks = {}
checks["recordHubSchema"] = (root / "contracts/summaries/application-summary-v1.schema.json").is_file()
checks["approverContract"] = (approver / "approver-contract/src/main/resources/record-hub/summaries/application-summary-v1.schema.json").is_file()
def has(pattern, base):
    result = subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return result.returncode == 0
checks["approverOutbox"] = has("RecordHubSummaryOutboxDispatcher|summary-changed", approver / "approver-api/src/main/java")
checks["recordHubReconciliation"] = has("AssociationGap|AssociationConflict|source version|gap", root / "server/internal/modules/projection")
checks["redactionBoundary"] = has("P6-SEC-003|PII|sealed", root / "docs/phase-6-requirements.md") and bool(spec["forbidden"])
static_missing = [name for name, value in checks.items() if not value]
live_status = "BLOCKED"
live_reason = "no immutable Approver native connector report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status = "PASS"
            live_reason = "Approver native projection report is PASS"
        else:
            live_reason = "Approver report exists but is not live PASS"
    except Exception as error:
        live_reason = f"Approver report is invalid: {error}"
status = "PASS_STATIC" if not static_missing and live_status == "PASS" else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-300",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": static_missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "source": spec["source"],
    "scope": spec["scope"],
    "allowlist": spec["allowlist"],
    "forbidden": spec["forbidden"],
    "reconciliation": spec["reconciliation"],
    "decision": "DO_NOT_ENABLE_APPROVER_PROJECTION" if status != "PASS_STATIC" else "STATIC_PROJECTION_READY_FOR_RELEASE_GATE",
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Run an isolated Approver Application/Process native projection, attach redacted live evidence, and rerun with RECORD_HUB_P6_APPROVER_LIVE_REPORT.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-300", "status": status, "liveStatus": live_status, "missingStatic": static_missing}, ensure_ascii=False))
PY
cat "$output"
