#!/usr/bin/env bash
set -Eeuo pipefail

# P6-502: inventory observation/reconciliation signals without enabling a new
# dashboard or exposing raw event data.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-502-observation-dashboard-spec.json"
output="${RECORD_HUB_P6_OBSERVATION_OUTPUT:-$root_dir/docs/phase-6-observation-dashboard.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-502 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-502" and
  .liveTraffic == false and .failClosed == true and
  (.signals | index("lag")) and (.signals | index("finding")) and
  .metrics.rawPayloadAction == "FORBIDDEN" and
  .metrics.highCardinalityAction == "REJECT" and
  .freshness.staleAction == "MARK_STALE_AND_ALERT" and
  .access.mutations == "NONE" and
  .missingAction == "BLOCKED_BY_OBSERVABILITY_GAP"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" <<'PY'
import json
import pathlib
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

operations = root / "server/internal/modules/projection/operations.go"
metrics = root / "server/internal/observability"
retry = root / "server/internal/modules/projection/retry.go"
rebuild = root / "server/internal/modules/projection/rebuild.go"
api = root / "api/openapi.yaml"
console = root / "web/src/components/console.tsx"

checks = {
    "operationsSnapshot": has(r"ProjectionFreshness|lagAgeSeconds|Backlog", operations),
    "boundedMetrics": has(r"record_hub_projection_backlog|record_hub_projection_lag", operations, metrics),
    "retryAndDead": has(r"Retry|DeadLetter|dead letter", retry, root / "server/internal/modules/projection/nats.go"),
    "recovery": has(r"ProjectionRebuild|rebuild", rebuild, api),
    "scopedApi": has(r"/api/v1/operations/events|tenantId|workspaceId", api, operations),
    "operationsConsole": has(r"tab === \"operations\"|OperationsSnapshot|operations", console),
    "findingSignal": has(r"Finding|finding|quarantine", root / "server/internal", api),
    "freshnessAudit": has(r"GeneratedAt|stale|audit", operations, root / "server/internal/modules/audit", spec_path),
}

missing = [name for name, value in checks.items() if not value]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
status = "PASS_STATIC" if not missing else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-502",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "scope": spec["scope"],
    "signals": spec["signals"],
    "metrics": spec["metrics"],
    "freshness": spec["freshness"],
    "alerts": spec["alerts"],
    "access": spec["access"],
    "decision": "DO_NOT_ENABLE_OBSERVATION_DASHBOARD" if missing else "STATIC_OBSERVATION_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Add finding/reconciliation counters and a scoped dashboard/export with stale-state alerts, then attach live recovery evidence.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-502", "status": status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
