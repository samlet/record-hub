#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-403-event-table-spec.json"
output="${RECORD_HUB_P6_EVENT_TABLE_OUTPUT:-$root_dir/docs/phase-6-event-table.json}"
live_report="${RECORD_HUB_P6_EVENT_TABLE_LIVE_REPORT:-}"
for command in date git jq rg python3; do command -v "$command" >/dev/null || { echo "P6-403 missing command: $command" >&2; exit 2; }; done
jq -e '.version == 1 and .phase == "6" and .task == "P6-403" and .liveTraffic == false and .failClosed == true and .mapping.registryRequired == true and .state.gapAction == "FINDING_AND_REPLAY" and .rebuild.mode == "STAGED_REPLAY_THEN_POINTER_SWITCH" and .missingAction == "BLOCKED_BY_LIVE_EVENT_EVIDENCE"' "$spec" >/dev/null
python3 - "$root_dir" "$spec" "$output" "$live_report" <<'PY'
import json, pathlib, subprocess, sys
from datetime import datetime, timezone
root, spec_path, output_path = map(pathlib.Path, sys.argv[1:4])
live_path = pathlib.Path(sys.argv[4]) if sys.argv[4] else None
spec = json.loads(spec_path.read_text())
def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
projection = root / "server/internal/modules/projection"
checks = {"mappingRegistry": has("MappingGenerationRegistry|MappingRegistration", projection), "sourcePointer": has("SourceVersion|LastEventID|PayloadHash", projection), "gapConflict": has("Gap|Conflict|quarantine", projection), "rebuild": has("Staged|staging|pointer|replay", projection), "audit": has("audit|Operator", projection)}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable event replay/rebuild report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS": live_status, live_reason = "PASS", "event/rebuild native report is PASS"
        else: live_reason = "event/rebuild report exists but is not live PASS"
    except Exception as error: live_reason = f"event/rebuild report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {"schemaVersion":1,"phase":"6","task":"P6-403","status":status,"generatedAt":datetime.now(timezone.utc).isoformat().replace("+00:00","Z"),"liveTraffic":False,"sourceCommit":subprocess.check_output(["git","-C",str(root),"rev-parse","HEAD"],text=True).strip(),"static":checks,"missingStatic":missing,"live":{"status":live_status,"reason":live_reason,"report":str(live_path) if live_path else ""},"mapping":spec["mapping"],"state":spec["state"],"rebuild":spec["rebuild"],"decision":"DO_NOT_ENABLE_EVENT_TABLE_MAPPING" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE","prerequisite":{"phase5":"INDEPENDENT_GATE","connectorEnablement":"NOT_GRANTED"},"next":"Run native event gap/conflict/hash replay and staged rebuild pointer switch with operator audit."}
pathlib.Path(output_path).write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"task":"P6-403","status":status,"liveStatus":live_status,"missingStatic":missing},ensure_ascii=False))
PY
cat "$output"
