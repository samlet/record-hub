#!/usr/bin/env bash
set -Eeuo pipefail

# P6-503: validate a redacted operator evidence envelope without touching live traffic.
# This is deliberately a dry-run. It never stops, drains, disables, replays, or rolls back.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-503-operator-runbook-spec.json"
output="${RECORD_HUB_P6_RUNBOOK_DRY_RUN_OUTPUT:-$root_dir/build/evidence/phase6/p6-503-dry-run.json}"
candidate="${RECORD_HUB_P6_RUNBOOK_DRY_RUN_REPORT:-}"

for command in date git jq python3; do
  command -v "$command" >/dev/null || { echo "P6-503 dry-run missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-503" and
  .liveTraffic == false and .failClosed == true and
  .replay.newIdAction == "REJECT" and
  .replay.rawPayloadConstruction == "FORBIDDEN" and
  .rollback.artifactDigestRequired == true and
  .rollback.oldWorkerRetention == "REQUIRED" and
  .rollback.deleteInboxOutboxReceiptFindingAudit == false and
  .rollback.postRollbackReconciliation == "REQUIRED" and
  .evidence.immutable == true and
  .missingAction == "BLOCKED_BY_LIVE_ROLLBACK_EVIDENCE"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" "$candidate" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:4])
candidate_path = pathlib.Path(sys.argv[4]) if sys.argv[4] else None
spec = json.loads(spec_path.read_text())
output_path.parent.mkdir(parents=True, exist_ok=True)

expected_procedure = [
    "STOP_NEW",
    "CAPTURE_SAFE_EVIDENCE",
    "DRAIN_IN_FLIGHT",
    "DISABLE_CONNECTOR",
    "REPLAY_ORIGINAL_ID",
    "RECONCILE_RECEIPTS_AND_FINDINGS",
    "ROLLBACK_ARTIFACT",
    "READ_ONLY_SMOKE",
    "RESUME_SCOPED_TRAFFIC",
]
required_evidence = [
    "incidentId",
    "actor",
    "scope",
    "oldArtifactDigest",
    "newArtifactDigest",
    "action",
    "checkpoint",
    "backlog",
    "findingIds",
    "occurredAt",
]
forbidden_evidence = [
    "accessToken",
    "privateKey",
    "PII",
    "sealedBid",
    "quote",
    "bankAccount",
    "invoiceAttachment",
    "rawPayload",
    "fullWorkflowSnapshot",
]

def non_empty(value):
    return value is not None and value != "" and value != [] and value != {}

def normalized(value):
    return "".join(char.lower() for char in str(value) if char.isalnum())

forbidden_keys = {normalized(item) for item in forbidden_evidence}

def forbidden_key_paths(value, path="$", found=None):
    found = [] if found is None else found
    if isinstance(value, dict):
        for key, child in value.items():
            child_path = f"{path}.{key}"
            if normalized(key) in forbidden_keys:
                found.append(child_path)
            forbidden_key_paths(child, child_path, found)
    elif isinstance(value, list):
        for index, child in enumerate(value):
            forbidden_key_paths(child, f"{path}[{index}]", found)
    return found

static_checks = {
    "procedureOrder": spec.get("procedure") == expected_procedure,
    "stopConditionsBounded": bool(spec.get("stopConditions")) and all(isinstance(item, str) and item for item in spec["stopConditions"]),
    "replayIdentityBound": spec.get("replay", {}).get("identity") == ["eventId", "operationId", "externalRequestId"],
    "replayRejectsNewId": spec.get("replay", {}).get("newIdAction") == "REJECT",
    "rollbackArtifactAndRetention": spec.get("rollback", {}).get("artifactDigestRequired") is True and spec.get("rollback", {}).get("oldWorkerRetention") == "REQUIRED",
    "rollbackKeepsHistory": spec.get("rollback", {}).get("deleteInboxOutboxReceiptFindingAudit") is False,
    "rollbackReconciles": spec.get("rollback", {}).get("postRollbackReconciliation") == "REQUIRED",
    "evidenceRequiredFields": spec.get("evidence", {}).get("requiredFields") == required_evidence,
    "evidenceForbiddenFields": spec.get("evidence", {}).get("forbiddenFields") == forbidden_evidence,
    "evidenceFieldsDisjoint": not set(required_evidence) & set(forbidden_evidence),
    "scopeFailClosed": spec.get("operator", {}).get("unknownScopeAction") == "REJECT" and set(spec.get("operator", {}).get("scope", [])) == {"tenantId", "workspaceId", "connector"},
}

candidate_status = "NOT_SUPPLIED"
candidate_checks = {
    "readableJson": True,
    "requiredFields": True,
    "scopeFields": True,
    "artifactDigests": True,
    "redactedKeys": True,
    "noLiveTraffic": True,
}
candidate_errors = []
if candidate_path is not None:
    candidate_status = "INVALID"
    try:
        candidate = json.loads(candidate_path.read_text())
        if not isinstance(candidate, dict):
            raise ValueError("evidence envelope must be a JSON object")
        missing = [field for field in required_evidence if not non_empty(candidate.get(field))]
        candidate_checks["requiredFields"] = not missing
        if missing:
            candidate_errors.append("missing required fields: " + ", ".join(missing))
        scope = candidate.get("scope")
        candidate_checks["scopeFields"] = isinstance(scope, dict) and all(non_empty(scope.get(field)) for field in ("tenantId", "workspaceId", "connector"))
        if not candidate_checks["scopeFields"]:
            candidate_errors.append("scope must include tenantId, workspaceId, connector")
        candidate_checks["artifactDigests"] = all(isinstance(candidate.get(field), str) and candidate[field].strip() for field in ("oldArtifactDigest", "newArtifactDigest"))
        if not candidate_checks["artifactDigests"]:
            candidate_errors.append("artifact digests must be non-empty strings")
        forbidden_paths = forbidden_key_paths(candidate)
        candidate_checks["redactedKeys"] = not forbidden_paths
        if forbidden_paths:
            candidate_errors.append("forbidden evidence keys: " + ", ".join(forbidden_paths))
        candidate_checks["noLiveTraffic"] = candidate.get("liveTraffic") is not True and candidate.get("liveStatus") != "PASS"
        if not candidate_checks["noLiveTraffic"]:
            candidate_errors.append("dry-run input must not claim liveTraffic or liveStatus PASS")
        if not candidate_errors:
            candidate_status = "VALID_REDACTED"
    except Exception as error:
        candidate_checks["readableJson"] = False
        candidate_errors.append(str(error))

all_static = all(static_checks.values())
candidate_ok = candidate_status in {"NOT_SUPPLIED", "VALID_REDACTED"}
status = "PASS_DRY_RUN" if all_static and candidate_ok else "FAIL_DRY_RUN"
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-503",
    "mode": "SAFE_DRY_RUN",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": static_checks,
    "candidate": {
        "status": candidate_status,
        "path": str(candidate_path) if candidate_path else "",
        "checks": candidate_checks,
        "errors": candidate_errors,
    },
    "live": {
        "status": "BLOCKED",
        "reason": "safe dry-run never evaluates or promotes live rollback evidence",
    },
    "procedure": expected_procedure,
    "decision": "DO_NOT_TOUCH_LIVE_TRAFFIC",
    "next": "Attach immutable redacted native drill evidence and run make p6-503 with RECORD_HUB_P6_RUNBOOK_LIVE_REPORT only after an isolated operator drill.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-503", "mode": "SAFE_DRY_RUN", "status": status, "candidate": candidate_status}, ensure_ascii=False))
if status != "PASS_DRY_RUN":
    raise SystemExit(1)
PY

cat "$output"
