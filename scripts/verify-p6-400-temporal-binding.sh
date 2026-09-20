#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
spec="$root_dir/deploy/local/p6/p6-400-temporal-binding-spec.json"
output="${RECORD_HUB_P6_TEMPORAL_BINDING_OUTPUT:-$root_dir/docs/phase-6-temporal-binding.json}"
live_report="${RECORD_HUB_P6_TEMPORAL_LIVE_REPORT:-}"
for command in date git jq rg python3; do command -v "$command" >/dev/null || { echo "P6-400 missing command: $command" >&2; exit 2; }; done
jq -e '.version == 1 and .phase == "6" and .task == "P6-400" and .liveTraffic == false and .failClosed == true and .activity.workflowDeterminism == "NO_DIRECT_IO" and .missingAction == "BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE"' "$spec" >/dev/null
python3 - "$root_dir" "$fluxion_root" "$spec" "$output" "$live_report" <<'PY'
import json, pathlib, subprocess, sys
from datetime import datetime, timezone
root, fluxion, spec_path, output_path = map(pathlib.Path, sys.argv[1:5])
live_path = pathlib.Path(sys.argv[5]) if sys.argv[5] else None
spec = json.loads(spec_path.read_text())
def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
checks = {
    "bindingService": has("CreateSnapshot|snapshotHash|OperationID", root / "server/internal/modules/binding"),
    "goSdk": has("SnapshotRequest|Idempotency-Key|SnapshotHash", root / "sdk/go/recordhub"),
    "javaSdk": has("SnapshotRequest|snapshotHash|operationId", root / "sdk/java/src/main/java"),
    "temporalActivity": has("ProjectDiagnosticBindingActivity|ActivityOptions|newActivityStub", fluxion / "server/src/main/kotlin"),
    "replaySafety": has("history|replay|determin|snapshot", fluxion / "server/src/test/kotlin/fluxion/workflow"),
}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable Temporal history/replay report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS": live_status, live_reason = "PASS", "Temporal native report is PASS"
        else: live_reason = "Temporal report exists but is not live PASS"
    except Exception as error: live_reason = f"Temporal report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {"schemaVersion":1,"phase":"6","task":"P6-400","status":status,"generatedAt":datetime.now(timezone.utc).isoformat().replace("+00:00","Z"),"liveTraffic":False,"sourceCommit":subprocess.check_output(["git","-C",str(root),"rev-parse","HEAD"],text=True).strip(),"static":checks,"missingStatic":missing,"live":{"status":live_status,"reason":live_reason,"report":str(live_path) if live_path else ""},"historyAllowed":spec["historyAllowed"],"historyForbidden":spec["historyForbidden"],"transactionModes":spec["transactionModes"],"activity":spec["activity"],"decision":"DO_NOT_RELEASE_TEMPORAL_ADAPTER" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE","prerequisite":{"phase5":"INDEPENDENT_GATE","connectorEnablement":"NOT_GRANTED"},"next":"Run native Temporal workflow history/replay with timeout, retry, idempotency and hash assertion evidence."}
pathlib.Path(output_path).write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"task":"P6-400","status":status,"liveStatus":live_status,"missingStatic":missing},ensure_ascii=False))
PY
cat "$output"
