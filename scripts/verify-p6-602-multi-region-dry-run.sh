#!/usr/bin/env bash
set -Eeuo pipefail

# P6-602: validate the multi-region decision record without changing topology
# or claiming failover readiness. This is a read-only decision preflight.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-602-multi-region-spec.json"
output="${RECORD_HUB_P6_MULTI_REGION_DRY_RUN_OUTPUT:-$root_dir/build/evidence/phase6/p6-602-dry-run.json}"

for command in date git jq python3; do
  command -v "$command" >/dev/null || { echo "P6-602 dry-run missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-602" and
  .liveTraffic == false and .failClosed == true and
  .decision.default == "SINGLE_REGION_HA" and
  .decision.goNoGo == "NO_GO_UNTIL_EVIDENCE" and
  .decision.zeroRpoClaim == "FORBIDDEN_WITHOUT_LIVE_PROOF" and
  .targets.measurementRequired == true and
  .targets.unverifiedAction == "DO_NOT_COMMIT_TO_TARGET" and
  .boundaries.crossRegionXa == "NOT_ASSUMED" and
  .boundaries.automaticCompensation == "NOT_ASSUMED" and
  .missingAction == "BLOCKED_BY_LIVE_MULTI_REGION_EVIDENCE"
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

expected_options = ["SINGLE_REGION_HA", "ACTIVE_PASSIVE_DR", "ACTIVE_ACTIVE"]
required_evidence = [
    "mixedLoadCapacity", "mongoReplicationAndPitr", "natsStreamReplicationAndReplay",
    "ownerWorkflowFailover", "rpoMeasurement", "rtoMeasurement", "costModel",
    "operatorRunbook", "securityAndScopeMatrix", "riskOwnerSignoff",
]
expected_boundaries = {
    "mongo": "OWNER_DATA_AND_RECORD_HUB_DATA_SEPARATE",
    "nats": "ALLOWLISTED_STREAMS_ONLY",
    "temporalConductor": "OWNER_FAILOVER_POLICY_REQUIRED",
    "crossRegionXa": "NOT_ASSUMED",
    "automaticCompensation": "NOT_ASSUMED",
}
decision = spec.get("decision", {})
targets = spec.get("targets", {})
boundaries = spec.get("boundaries", {})
checks = {
    "optionsComplete": decision.get("options") == expected_options and len(set(expected_options)) == len(expected_options),
    "singleRegionDefault": decision.get("default") == "SINGLE_REGION_HA",
    "evidenceGated": decision.get("goNoGo") == "NO_GO_UNTIL_EVIDENCE" and bool(spec.get("requiredEvidence")),
    "requiredEvidenceComplete": spec.get("requiredEvidence") == required_evidence and len(set(required_evidence)) == len(required_evidence),
    "zeroRpoFailClosed": decision.get("zeroRpoClaim") == "FORBIDDEN_WITHOUT_LIVE_PROOF",
    "rpoRtoMeasured": targets.get("measurementRequired") is True and isinstance(targets.get("rpoMinutes"), int) and isinstance(targets.get("rtoMinutes"), int) and 0 < targets["rpoMinutes"] < targets["rtoMinutes"],
    "unverifiedNoCommit": targets.get("unverifiedAction") == "DO_NOT_COMMIT_TO_TARGET",
    "boundariesExplicit": boundaries == expected_boundaries,
    "liveTrafficDisabled": spec.get("liveTraffic") is False and spec.get("failClosed") is True,
}
status = "PASS_DRY_RUN" if all(checks.values()) else "FAIL_DRY_RUN"
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-602",
    "mode": "SAFE_DRY_RUN",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "checks": checks,
    "decision": decision,
    "requiredEvidence": required_evidence,
    "targets": targets,
    "boundaries": boundaries,
    "live": {
        "status": "BLOCKED",
        "reason": "safe dry-run never changes topology, measures failover, or promotes multi-region evidence",
    },
    "goNoGo": "NO_GO",
    "next": "Collect immutable capacity, replication/replay, failover, RPO/RTO, cost, security and owner-signoff evidence before revisiting the decision.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-602", "mode": "SAFE_DRY_RUN", "status": status}, ensure_ascii=False))
if status != "PASS_DRY_RUN":
    raise SystemExit(1)
PY

cat "$output"
