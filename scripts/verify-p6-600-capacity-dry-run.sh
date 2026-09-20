#!/usr/bin/env bash
set -Eeuo pipefail

# P6-600: validate a bounded mixed-load plan without starting load or changing limits.
# This is a contract preflight only; it never starts the four owners or publishes events.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-600-mixed-load-spec.json"
output="${RECORD_HUB_P6_MIXED_LOAD_DRY_RUN_OUTPUT:-$root_dir/build/evidence/phase6/p6-600-dry-run.json}"

for command in date git jq python3; do
  command -v "$command" >/dev/null || { echo "P6-600 dry-run missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-600" and
  .liveTraffic == false and .failClosed == true and
  .workload.tenantIsolation == "REQUIRED" and
  (.measurements | index("p95LatencyMs")) and (.measurements | index("p99LatencyMs")) and
  .backpressure.queryBudget == "REJECT_OVER_BUDGET" and
  .backpressure.rateLimit == "429_RETRY_AFTER" and
  .backpressure.connector == "STOP_NEW_AND_DRAIN_IN_FLIGHT" and
  .slo.errorBudgetAction == "STOP_AND_REVIEW" and
  .cost.rawPayloadAction == "FORBIDDEN" and
  .cost.unboundedLabelAction == "REJECT" and
  .missingAction == "BLOCKED_BY_LIVE_CAPACITY_EVIDENCE"
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

expected_systems = ["record-hub", "approver", "fluxion", "bids"]
expected_planes = ["records", "connector", "temporal", "conductor", "nats", "mongo"]
expected_rates = [10, 50, 100, 250, 500]
required_measurements = [
    "requestRate", "publishRate", "drainRate", "p50LatencyMs", "p95LatencyMs",
    "p99LatencyMs", "backlogDrainSeconds", "retryCount", "deadCount",
    "queryCostUnits", "memoryBytes", "cpuSeconds",
]
expected_dimensions = ["tenant", "workspace", "consumer", "operation"]

def unique(values):
    return len(values) == len(set(values))

workload = spec.get("workload", {})
backpressure = spec.get("backpressure", {})
slo = spec.get("slo", {})
cost = spec.get("cost", {})
checks = {
    "ownerSet": workload.get("systems") == expected_systems,
    "planeSet": workload.get("planes") == expected_planes,
    "rateStepsBounded": workload.get("eventRateSteps") == expected_rates and unique(expected_rates) and all(isinstance(rate, int) and 0 < rate <= 10000 for rate in expected_rates),
    "sampleLimitBounded": isinstance(workload.get("sampleLimit"), int) and 0 < workload["sampleLimit"] <= 10000,
    "measurementSet": spec.get("measurements") == required_measurements and unique(spec.get("measurements", [])),
    "queryBackpressure": backpressure.get("queryBudget") == "REJECT_OVER_BUDGET",
    "rateBackpressure": backpressure.get("rateLimit") == "429_RETRY_AFTER",
    "natsBackpressure": backpressure.get("natsPublish") == "BOUNDED_AND_REJECT_ON_OVERLOAD",
    "connectorBackpressure": backpressure.get("connector") == "STOP_NEW_AND_DRAIN_IN_FLIGHT",
    "sloOrder": isinstance(slo.get("p95LatencyMs"), int) and isinstance(slo.get("p99LatencyMs"), int) and 0 < slo["p95LatencyMs"] <= slo["p99LatencyMs"],
    "backlogBounded": isinstance(slo.get("maxBacklogDrainSeconds"), int) and slo["maxBacklogDrainSeconds"] > 0,
    "deadCountBounded": slo.get("maxDeadCount") == 0,
    "stopOnBudget": slo.get("errorBudgetAction") == "STOP_AND_REVIEW",
    "costDimensions": cost.get("requiredDimensions") == expected_dimensions and unique(expected_dimensions),
    "costFailClosed": cost.get("rawPayloadAction") == "FORBIDDEN" and cost.get("unboundedLabelAction") == "REJECT",
    "tenantIsolation": workload.get("tenantIsolation") == "REQUIRED",
}
status = "PASS_DRY_RUN" if all(checks.values()) else "FAIL_DRY_RUN"
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-600",
    "mode": "SAFE_DRY_RUN",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "checks": checks,
    "plan": {
        "systems": expected_systems,
        "planes": expected_planes,
        "eventRateSteps": expected_rates,
        "sampleLimit": workload.get("sampleLimit"),
        "measurements": required_measurements,
        "slo": slo,
        "backpressure": backpressure,
        "costDimensions": expected_dimensions,
    },
    "live": {
        "status": "BLOCKED",
        "reason": "safe dry-run never starts load, publishes events, changes limits, or evaluates live capacity evidence",
    },
    "decision": "DO_NOT_DECLARE_MIXED_LOAD_CAPACITY",
    "next": "Run the isolated four-owner mixed load at every bounded rate step and attach immutable redacted cost/SLO/backpressure evidence before rerunning make p6-600.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-600", "mode": "SAFE_DRY_RUN", "status": status}, ensure_ascii=False))
if status != "PASS_DRY_RUN":
    raise SystemExit(1)
PY

cat "$output"
