#!/usr/bin/env bash
set -Eeuo pipefail

# P6-500: inventory the self-service control-plane boundary. This gate freezes
# policy and reports missing UI/API facets; it never enables mutations or export.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-500-control-plane-spec.json"
output="${RECORD_HUB_P6_CONTROL_PLANE_OUTPUT:-$root_dir/docs/phase-6-control-plane.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-500 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-500" and
  .liveTraffic == false and .failClosed == true and
  .scope.crossScopeAction == "REJECT" and
  .ui.unknownAction == "HIDE_AND_REJECT" and
  .audit.immutable == true and
  .audit.failedAuthorizationAction == "AUDIT_AND_REJECT" and
  .export.redaction == "DROP_AND_AUDIT" and
  .missingAction == "BLOCKED_BY_CONTROL_PLANE_GAP"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" <<'PY'
import json
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())

def has(pattern, *paths):
    result = subprocess.run(
        ["rg", "-q", pattern, *map(str, paths)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return result.returncode == 0

identity = root / "server/internal/modules/identity"
records = root / "server/internal/modules/records"
schema = root / "server/internal/modules/schema"
api = root / "api/openapi.yaml"
console = root / "web/src/components/console.tsx"
requirements = root / "docs/phase-6-requirements.md"

checks = {
    "rbac": has(r"RoleOwner|RoleEditor|RoleViewer|RoleOperator", identity / "authorization.go"),
    "scope": has(r"TenantID|WorkspaceID", records, schema),
    "audit": has(r"audit\.Entry|auditWriter|Append\(", records, schema, root / "server/internal/modules/audit"),
    "tableSchemaApi": has(r"/api/v1/workspaces/\{workspaceId\}/tables", api) and has(r"/api/v1/schemas", api),
    "recordViewConsole": has(r"export function Console|tab === \"schema\"|tab === \"records\"", console),
    "typedRelationApi": has(r"RecordRelation|relations", api) and has(r"RelationType|RecordRelation", records),
    "tagDictionary": has(r"TagDictionary|ControlledTag|tag\.assign", root / "server", root / "web", api),
    "relationConsole": has(r"relationType|relations", console),
    "redaction": has(r"P6-SEC-003|redact|sensitive|sealedBid", requirements, root / "server", spec_path),
}

missing = [name for name, value in checks.items() if not value]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
status = "PASS_STATIC" if not missing else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-500",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "scope": spec["scope"],
    "roles": spec["roles"],
    "actions": spec["actions"],
    "ui": spec["ui"],
    "audit": spec["audit"],
    "export": spec["export"],
    "decision": "DO_NOT_ENABLE_CONTROL_PLANE" if missing else "STATIC_CONTROL_PLANE_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Implement the missing controlled tag dictionary and relation console/API facets, then add browser/API matrix and redacted export evidence.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-500", "status": status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
