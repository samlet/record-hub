#!/usr/bin/env bash
set -Eeuo pipefail

# P6-301: validate Fluxion approval contract and Temporal receipt markers only.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
spec="$root_dir/deploy/local/p6/p6-301-fluxion-approval-spec.json"
output="${RECORD_HUB_P6_FLUXION_APPROVAL_OUTPUT:-$root_dir/docs/phase-6-fluxion-approval.json}"
live_report="${RECORD_HUB_P6_FLUXION_LIVE_REPORT:-}"
for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-301 missing command: $command" >&2; exit 2; }
done
jq -e '.version == 1 and .phase == "6" and .task == "P6-301" and .liveTraffic == false and .failClosed == true and .rollout.fallback == "LOCAL_APPROVER" and .temporalReceipt.updateMode == "IDEMPOTENT_WORKFLOW_UPDATE" and .missingAction == "BLOCKED_BY_LIVE_CONNECTOR"' "$spec" >/dev/null

python3 - "$root_dir" "$fluxion_root" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, fluxion, spec_path, output_path = map(pathlib.Path, sys.argv[1:5])
live_path = pathlib.Path(sys.argv[5]) if sys.argv[5] else None
spec = json.loads(spec_path.read_text())

def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

checks = {
    "requestContract": (fluxion / "server/src/main/resources/record-hub/approvals/dispatch-approval-request-v1.schema.json").is_file(),
    "resultContract": (fluxion / "server/src/main/resources/record-hub/approvals/dispatch-approval-result-v1.schema.json").is_file(),
    "outbox": has("ExternalApproval.*Outbox|outbox", fluxion / "server/src/main/kotlin/fluxion/approval"),
    "temporalUpdate": has("TemporalUpdater|update", fluxion / "server/src/main/kotlin/fluxion/approval"),
    "localFallback": has("confidenceThreshold|LOCAL|fallback", fluxion / "server/src/main/kotlin/fluxion"),
}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable Fluxion native connector report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status, live_reason = "PASS", "Fluxion native report is PASS"
        else:
            live_reason = "Fluxion report exists but is not live PASS"
    except Exception as error:
        live_reason = f"Fluxion report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-301",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip(),
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "source": spec["source"],
    "safeMetadata": spec["safeMetadata"],
    "forbidden": spec["forbidden"],
    "rollout": spec["rollout"],
    "temporalReceipt": spec["temporalReceipt"],
    "decision": "DO_NOT_ENABLE_FLUXION_APPROVAL" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE",
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Run tenant-canary native Fluxion approval with Temporal update receipt, local fallback and rollback evidence.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-301", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY
cat "$output"
