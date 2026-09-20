#!/usr/bin/env bash
set -Eeuo pipefail

# P6-601: validate retention/archive/restore policy and optional immutable
# restore evidence. It never deletes data or runs a restore.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-601-retention-restore-spec.json"
output="${RECORD_HUB_P6_RETENTION_OUTPUT:-$root_dir/docs/phase-6-retention-restore.json}"
live_report="${RECORD_HUB_P6_RETENTION_LIVE_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-601 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-601" and
  .liveTraffic == false and .retention.policyVersionRequired == true and
  .retention.legalHold == "PRESERVE_AND_AUDIT" and
  .backup.pitr == "REQUIRED" and .backup.restoreTarget == "DISPOSABLE_ISOLATED_SERVICE" and
  (.restoreAssertions | index("count")) and (.restoreAssertions | index("contentHash")) and
  (.restoreAssertions | index("checkpoint")) and
  .rebuild.mode == "STAGED_REPLAY_THEN_CAS_POINTER_SWITCH" and
  .missingAction == "BLOCKED_BY_LIVE_RESTORE_EVIDENCE"
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
    result = subprocess.run(["rg", "-q", pattern, *map(str, paths)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return result.returncode == 0

mongo_spec = root / "deploy/local/p5/p5-200-mongo-spec.json"
mongo_doc = root / "docs/phase-5-batch2-p5-200.md"
restore_doc = root / "docs/phase-3-batch4f-backup-restore.md"
rebuild = root / "server/internal/modules/projection/rebuild.go"
archive = root / "server/internal/modules/projection"
audit = root / "server/internal/modules/audit"

checks = {
    "retentionPolicy": has(r"retention|archive|PITR", mongo_spec, mongo_doc, restore_doc),
    "encryptedBackup": has(r"encrypt|SHA-256|SHA256|manifest", mongo_spec, restore_doc),
    "isolatedRestore": has(r"isolated|disposable|restore target", mongo_spec, restore_doc),
    "restoreAssertions": has(r"count|hash|index|version|cursor|checkpoint|backlog", mongo_spec, restore_doc),
    "stagedRebuild": has(r"staging|staged|CAS|pointer|replay", rebuild),
    "archiveScope": has(r"ProjectionEventArchive|tenantId|workspaceId|PayloadHash", archive),
    "operatorAudit": has(r"audit|operator", rebuild, audit),
}
missing = [name for name, value in checks.items() if not value]
live_status = "BLOCKED"
live_reason = "no immutable archive/retention/PITR restore report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status = "PASS"
            live_reason = "native retention/restore report is PASS"
        else:
            live_reason = "retention/restore report exists but is not live PASS"
    except Exception as error:
        live_reason = f"retention/restore report is invalid: {error}"

status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-601",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "retention": spec["retention"],
    "backup": spec["backup"],
    "restoreAssertions": spec["restoreAssertions"],
    "rebuild": spec["rebuild"],
    "decision": "DO_NOT_CHANGE_RETENTION_OR_RESTORE_POLICY" if status != "PASS_STATIC" else "STATIC_RETENTION_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Run isolated encrypted backup/PITR restore, retention expiry/legal-hold checks and staged rebuild pointer verification; attach immutable evidence and rerun with RECORD_HUB_P6_RETENTION_LIVE_REPORT.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-601", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
