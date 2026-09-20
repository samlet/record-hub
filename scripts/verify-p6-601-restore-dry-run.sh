#!/usr/bin/env bash
set -Eeuo pipefail

# P6-601: validate a retention/restore plan without deleting, restoring, or
# mutating any data. This is a contract preflight only.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-601-retention-restore-spec.json"
output="${RECORD_HUB_P6_RETENTION_DRY_RUN_OUTPUT:-$root_dir/build/evidence/phase6/p6-601-dry-run.json}"

for command in date git jq python3; do
  command -v "$command" >/dev/null || { echo "P6-601 dry-run missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-601" and
  .liveTraffic == false and .failClosed == true and
  .retention.policyVersionRequired == true and
  .retention.legalHold == "PRESERVE_AND_AUDIT" and
  .retention.expiryAction == "ARCHIVE_THEN_DELETE_ONLY_AFTER_VERIFICATION" and
  .retention.rawPayloadArchive == "OWNER_POLICY_ONLY" and
  .backup.encryption == "REQUIRED" and .backup.pitr == "REQUIRED" and
  .backup.manifestDigest == "SHA256" and
  .backup.restoreTarget == "DISPOSABLE_ISOLATED_SERVICE" and
  .rebuild.mode == "STAGED_REPLAY_THEN_CAS_POINTER_SWITCH" and
  .rebuild.liveReadIsolation == "REQUIRED" and
  .rebuild.sourcePointerRequired == true and
  .rebuild.operatorAuditRequired == true and
  .missingAction == "BLOCKED_BY_LIVE_RESTORE_EVIDENCE"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())
output_path.parent.mkdir(parents=True, exist_ok=True)

expected_scopes = ["tenant", "workspace", "table", "schema", "record", "audit", "inbox", "outbox", "dlq"]
expected_assertions = [
    "count", "contentHash", "index", "schemaVersion", "recordVersion",
    "cursor", "checkpoint", "inbox", "outbox", "audit", "backlog",
]
retention = spec.get("retention", {})
backup = spec.get("backup", {})
rebuild = spec.get("rebuild", {})
checks = {
    "scopeCoverage": retention.get("scopes") == expected_scopes and len(set(expected_scopes)) == len(expected_scopes),
    "versionedPolicy": retention.get("policyVersionRequired") is True,
    "legalHoldPreserved": retention.get("legalHold") == "PRESERVE_AND_AUDIT",
    "verifiedExpiry": retention.get("expiryAction") == "ARCHIVE_THEN_DELETE_ONLY_AFTER_VERIFICATION",
    "rawArchiveOwnerPolicy": retention.get("rawPayloadArchive") == "OWNER_POLICY_ONLY",
    "encryptedPITR": backup.get("encryption") == "REQUIRED" and backup.get("pitr") == "REQUIRED",
    "manifestDigest": backup.get("manifestDigest") == "SHA256",
    "isolatedRestoreTarget": backup.get("restoreTarget") == "DISPOSABLE_ISOLATED_SERVICE",
    "assertionCoverage": spec.get("restoreAssertions") == expected_assertions and len(set(expected_assertions)) == len(expected_assertions),
    "stagedRebuild": rebuild.get("mode") == "STAGED_REPLAY_THEN_CAS_POINTER_SWITCH",
    "liveReadIsolation": rebuild.get("liveReadIsolation") == "REQUIRED",
    "sourcePointer": rebuild.get("sourcePointerRequired") is True,
    "operatorAudit": rebuild.get("operatorAuditRequired") is True,
}
status = "PASS_DRY_RUN" if all(checks.values()) else "FAIL_DRY_RUN"
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-601",
    "mode": "SAFE_DRY_RUN",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "checks": checks,
    "plan": {
        "retention": retention,
        "backup": backup,
        "restoreAssertions": expected_assertions,
        "rebuild": rebuild,
    },
    "live": {
        "status": "BLOCKED",
        "reason": "safe dry-run never deletes data, restores a backup, switches a pointer, or evaluates live restore evidence",
    },
    "decision": "DO_NOT_CHANGE_RETENTION_OR_RESTORE_POLICY",
    "next": "Run isolated encrypted backup/PITR restore, legal-hold/expiry checks and staged rebuild pointer verification before rerunning make p6-601.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-601", "mode": "SAFE_DRY_RUN", "status": status}, ensure_ascii=False))
if status != "PASS_DRY_RUN":
    raise SystemExit(1)
PY

cat "$output"
