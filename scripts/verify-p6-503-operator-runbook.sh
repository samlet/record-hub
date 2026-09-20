#!/usr/bin/env bash
set -Eeuo pipefail

# P6-503: validate the operator procedure and optional immutable drill report.
# This gate never stops services, drains traffic, or performs a rollback.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-503-operator-runbook-spec.json"
output="${RECORD_HUB_P6_RUNBOOK_OUTPUT:-$root_dir/docs/phase-6-operator-runbook.json}"
live_report="${RECORD_HUB_P6_RUNBOOK_LIVE_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-503 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-503" and
  .liveTraffic == false and .failClosed == true and
  (.procedure | index("STOP_NEW")) and (.procedure | index("DRAIN_IN_FLIGHT")) and
  (.procedure | index("ROLLBACK_ARTIFACT")) and
  .replay.newIdAction == "REJECT" and
  .rollback.artifactDigestRequired == true and
  .rollback.deleteInboxOutboxReceiptFindingAudit == false and
  .evidence.immutable == true and
  .missingAction == "BLOCKED_BY_LIVE_ROLLBACK_EVIDENCE"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" "$live_report" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:4])
live_path = pathlib.Path(sys.argv[4]) if sys.argv[4] else None
spec = json.loads(spec_path.read_text())

def has(pattern, *paths):
    result = subprocess.run(
        ["rg", "-q", pattern, *map(str, paths)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return result.returncode == 0

runbook = root / "docs/m8-recovery-runbook.md"
phase4_runbook = root / "docs/phase-4-batch4-runbook.md"
connector = root / "deploy/local/p6/p6-200-connector-registry-spec.json"
rebuild = root / "server/internal/modules/projection/rebuild.go"
audit = root / "server/internal/modules/audit"
evidence_contract = root / "docs/phase-6-evidence-contract.json"

checks = {
    "recoveryRunbook": has(r"eventId|operationId|STOP|DLQ|replay|checkpoint", runbook),
    "stopDrainRollback": has(r"停止新 publisher|drain|回滚|rollback|reconciliation", phase4_runbook),
    "connectorDisable": has(r"STOP_NEW_AND_DRAIN_IN_FLIGHT|DISABLED|drain", connector),
    "rebuildBoundary": has(r"SaveCAS|rebuild|replay|audit", rebuild),
    "auditBoundary": has(r"audit|immutable|operator", audit, evidence_contract),
    "safeEvidence": has(r"rawPayload|fullWorkflowSnapshot|sealedBid|privateKey|forbidden", spec_path, evidence_contract),
}
missing = [name for name, value in checks.items() if not value]
live_status = "BLOCKED"
live_reason = "no immutable stop/drain/replay/disable/rollback drill report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status = "PASS"
            live_reason = "operator rollback drill report is PASS"
        else:
            live_reason = "operator rollback drill report exists but is not live PASS"
    except Exception as error:
        live_reason = f"operator rollback drill report is invalid: {error}"

status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-503",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "operator": spec["operator"],
    "procedure": spec["procedure"],
    "stopConditions": spec["stopConditions"],
    "replay": spec["replay"],
    "rollback": spec["rollback"],
    "evidence": spec["evidence"],
    "decision": "DO_NOT_ENABLE_OPERATOR_ROLLBACK_AUTOMATION" if status != "PASS_STATIC" else "STATIC_RUNBOOK_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Run the isolated native stop/drain/replay/disable/rollback drill, attach immutable redacted evidence and rerun with RECORD_HUB_P6_RUNBOOK_LIVE_REPORT.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-503", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
