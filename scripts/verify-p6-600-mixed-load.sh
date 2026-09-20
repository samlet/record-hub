#!/usr/bin/env bash
set -Eeuo pipefail

# P6-600: validate the mixed-load evaluation contract and optional native
# evidence. It does not start load, mutate services, or change limits.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-600-mixed-load-spec.json"
output="${RECORD_HUB_P6_MIXED_LOAD_OUTPUT:-$root_dir/docs/phase-6-mixed-load.json}"
live_report="${RECORD_HUB_P6_MIXED_LOAD_LIVE_REPORT:-}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-600 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-600" and
  .liveTraffic == false and .workload.tenantIsolation == "REQUIRED" and
  (.measurements | index("p95LatencyMs")) and (.measurements | index("backlogDrainSeconds")) and
  .backpressure.queryBudget == "REJECT_OVER_BUDGET" and
  .backpressure.connector == "STOP_NEW_AND_DRAIN_IN_FLIGHT" and
  .slo.errorBudgetAction == "STOP_AND_REVIEW" and
  .cost.rawPayloadAction == "FORBIDDEN" and
  .missingAction == "BLOCKED_BY_LIVE_CAPACITY_EVIDENCE"
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

checks = {
    "priorCapacityHarness": (root / "scripts/verify-p3-capacity-baseline.sh").is_file() and has(r"publish|drain|p95|p99", root / "scripts/verify-p3-capacity-baseline.sh"),
    "queryBudget": has(r"QueryBudget|ErrQueryBudgetExceeded|MaxPageRows", root / "server/internal/observability", root / "server/internal/modules/records"),
    "rateBackpressure": has(r"RATE_LIMITED|Retry-After|backpressure|rate limit", root / "server/internal/observability", root / "server/internal/app"),
    "projectionMetrics": has(r"projection_backlog|projection_lag|projection_dlq|projection_redeliveries", root / "server/internal/modules/projection"),
    "natsBoundedRetry": has(r"MaxDeliver|retryDelay|DeadLetter", root / "server/internal/modules/projection/nats.go", root / "server/internal/modules/projection/retry.go"),
    "tenantScope": has(r"TenantID|WorkspaceID|tenantId|workspaceId", root / "server/internal/modules/records", root / "server/internal/modules/projection"),
    "mixedOwnerHarness": has(r"approver.*fluxion.*bids|fourOwnerMixedWorkload|Temporal.*Conductor", root / "scripts", root / "docs"),
}
missing = [name for name, value in checks.items() if not value]
live_status = "BLOCKED"
live_reason = "no immutable four-owner mixed-load/cost/backpressure report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS":
            live_status = "PASS"
            live_reason = "native mixed-load report is PASS"
        else:
            live_reason = "mixed-load report exists but is not live PASS"
    except Exception as error:
        live_reason = f"mixed-load report is invalid: {error}"

status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-600",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "live": {"status": live_status, "reason": live_reason, "report": str(live_path) if live_path else ""},
    "workload": spec["workload"],
    "measurements": spec["measurements"],
    "backpressure": spec["backpressure"],
    "slo": spec["slo"],
    "cost": spec["cost"],
    "decision": "DO_NOT_DECLARE_MIXED_LOAD_CAPACITY" if status != "PASS_STATIC" else "STATIC_MIXED_LOAD_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Run isolated four-owner mixed load at each event-rate step, capture immutable cost/SLO/backpressure evidence, and rerun with RECORD_HUB_P6_MIXED_LOAD_LIVE_REPORT.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-600", "status": status, "liveStatus": live_status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
