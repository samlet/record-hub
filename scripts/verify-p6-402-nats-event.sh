#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-402-nats-event-spec.json"
output="${RECORD_HUB_P6_NATS_EVENT_OUTPUT:-$root_dir/docs/phase-6-nats-event.json}"
live_report="${RECORD_HUB_P6_NATS_LIVE_REPORT:-}"
for command in date git jq rg python3; do command -v "$command" >/dev/null || { echo "P6-402 missing command: $command" >&2; exit 2; }; done
jq -e '.version == 1 and .phase == "6" and .task == "P6-402" and .liveTraffic == false and .failClosed == true and .subject.registryRequired == true and .subject.arbitraryPublish == "DENY" and .durable.ackMode == "double-ack" and .missingAction == "BLOCKED_BY_LIVE_EVENT_EVIDENCE"' "$spec" >/dev/null
python3 - "$root_dir" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:4])
live_path = pathlib.Path(sys.argv[4]) if sys.argv[4] else None
spec = json.loads(spec_path.read_text())

def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

checks = {
    "natsRunner": has("PullRunner|DoubleAck|JetStream", root / "server/internal/modules/projection"),
    "dlq": has("DeadLetter|dead.?letter|dlq", root / "server/internal/modules/projection"),
    "replay": has("archive|replay|MaxArchivedReplayEvents", root / "server/internal/modules/projection"),
    "acl": has("permissions|allow|subject", root / "deploy/local/nats"),
    "backpressure": has("backpressure|in.?flight|MaxDeliver|retry", root / "server/internal/modules/projection"),
}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable NATS outage/replay report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status, live_reason = "PASS", "NATS native report is PASS"
        else:
            live_reason = "NATS report exists but is not live PASS"
    except Exception as error:
        live_reason = f"NATS report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-402",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip(),
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "subject": spec["subject"],
    "durable": spec["durable"],
    "dlq": spec["dlq"],
    "acl": spec["acl"],
    "backpressure": spec["backpressure"],
    "decision": "DO_NOT_OPEN_NATS_EVENT_TRAFFIC" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE",
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Run native NATS outage, durable replay, DLQ redaction and backpressure evidence.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-402", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY
cat "$output"
