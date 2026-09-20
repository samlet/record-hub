#!/usr/bin/env bash
set -Eeuo pipefail

# P6-302: validate Bids approval/Conductor markers without completing a task.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
spec="$root_dir/deploy/local/p6/p6-302-bids-approval-spec.json"
output="${RECORD_HUB_P6_BIDS_APPROVAL_OUTPUT:-$root_dir/docs/phase-6-bids-approval.json}"
live_report="${RECORD_HUB_P6_BIDS_LIVE_REPORT:-}"
for command in date git jq rg python3; do command -v "$command" >/dev/null || { echo "P6-302 missing command: $command" >&2; exit 2; }; done
jq -e '.version == 1 and .phase == "6" and .task == "P6-302" and .liveTraffic == false and .failClosed == true and .conductor.publishPrecondition == "APPROVED_ONLY" and .missingAction == "BLOCKED_BY_LIVE_CONNECTOR"' "$spec" >/dev/null

python3 - "$root_dir" "$bids_root" "$spec" "$output" "$live_report" <<'PY'
import json, pathlib, subprocess, sys
from datetime import datetime, timezone
root, bids, spec_path, output_path = map(pathlib.Path, sys.argv[1:5])
live_path = pathlib.Path(sys.argv[5]) if sys.argv[5] else None
spec = json.loads(spec_path.read_text())
def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
checks = {
    "approvalContract": (bids / "backend/internal/recordhub/contracts/approvals/bids-tender-publication-approval-request-v1.schema.json").is_file(),
    "resultService": has("Conductor|approval result|ApprovalGeneration", bids / "backend/internal/approval"),
    "conductorTasks": has("TenderApprovalTaskRef|tender_approval|publish_tender", bids / "backend/internal/conductor"),
    "outbox": has("outbox|Outbox", bids / "backend/internal/approval"),
    "redaction": has("sensitive-field|sealed|redacted", bids / "backend/internal/recordhub/contracts/approvals") or bool(spec["forbidden"]),
}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable Bids native Conductor report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS": live_status, live_reason = "PASS", "Bids native report is PASS"
        else: live_reason = "Bids report exists but is not live PASS"
    except Exception as error: live_reason = f"Bids report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {"schemaVersion":1,"phase":"6","task":"P6-302","status":status,"generatedAt":datetime.now(timezone.utc).isoformat().replace("+00:00","Z"),"liveTraffic":False,"sourceCommit":subprocess.check_output(["git","-C",str(root),"rev-parse","HEAD"],text=True).strip(),"static":checks,"missingStatic":missing,"live":{"status":live_status,"reason":live_reason,"report":str(live_path) if live_path else ""},"source":spec["source"],"safeMetadata":spec["safeMetadata"],"forbidden":spec["forbidden"],"conductor":spec["conductor"],"rollout":spec["rollout"],"decision":"DO_NOT_COMPLETE_BIDS_PUBLISH_TASK" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE","prerequisite":{"phase5":"INDEPENDENT_GATE","connectorEnablement":"NOT_GRANTED"},"next":"Run isolated Bids Conductor approval with approved-only publish, drain and rollback evidence."}
pathlib.Path(output_path).write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"task":"P6-302","status":status,"liveStatus":live_status,"missingStatic":missing},ensure_ascii=False))
PY
cat "$output"
