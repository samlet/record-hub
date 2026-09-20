#!/usr/bin/env bash
set -Eeuo pipefail

# P6-200: validate the connector registry lifecycle contract. This gate only
# inspects manifests and tests; it never transitions a connector or sends work.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-200-connector-registry-spec.json"
manifest="$root_dir/contracts/connectors/manifest.json"
output="${RECORD_HUB_P6_CONNECTOR_REGISTRY_OUTPUT:-$root_dir/docs/phase-6-connector-registry.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-200 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-200" and
  .liveTraffic == false and .failClosed == true and
  .key.match == "exact" and .key.unknownAction == "REJECT" and
  .lifecycle.initialStatus == "DRAFT" and
  .lifecycle.disableBehavior == "STOP_NEW_AND_DRAIN_IN_FLIGHT" and
  .compatibility.windowInclusive == true and
  .compatibility.outsideWindowAction == "REJECT"
' "$spec" >/dev/null

python3 - "$spec" "$manifest" <<'PY'
import json
import pathlib
import re
import sys

spec = json.loads(pathlib.Path(sys.argv[1]).read_text())
manifest = json.loads(pathlib.Path(sys.argv[2]).read_text())
required = set(spec["manifest"]["requiredFields"])
statuses = set(spec["manifest"]["statusValues"])
entries = manifest.get("entries", [])
if manifest.get("version") != 1 or not entries:
    raise SystemExit("P6-200 connector manifest is missing or empty")
keys = set()
version_pattern = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
hash_pattern = re.compile(r"^sha256:[0-9a-f]{64}$")
for entry in entries:
    missing = required - set(entry) - {"revision"}
    if missing:
        raise SystemExit(f"P6-200 manifest missing fields: {sorted(missing)}")
    key = (entry["connector"], entry["event"], entry["schemaVersion"])
    if key in keys:
        raise SystemExit(f"P6-200 duplicate key: {key}")
    keys.add(key)
    if entry["schemaVersion"] < 1 or entry["status"] not in statuses:
        raise SystemExit(f"P6-200 invalid key/status: {key}")
    if not all(version_pattern.fullmatch(entry[field]) for field in ("sdkVersion", "compatibilityMin", "compatibilityMax")):
        raise SystemExit(f"P6-200 invalid version in {key}")
    if not hash_pattern.fullmatch(entry["contractHash"]):
        raise SystemExit(f"P6-200 invalid contract hash in {key}")
    if not entry["allowedFields"] or len(entry["allowedFields"]) != len(set(entry["allowedFields"])):
        raise SystemExit(f"P6-200 allowedFields is empty or duplicated in {key}")
    minimum = tuple(map(int, entry["compatibilityMin"].split(".")))
    maximum = tuple(map(int, entry["compatibilityMax"].split(".")))
    if minimum > maximum:
        raise SystemExit(f"P6-200 inverted compatibility window in {key}")
transitions = {tuple(item) for item in spec["lifecycle"]["allowedTransitions"]}
for pair in (("DRAFT", "ENABLED"), ("ENABLED", "DISABLED"), ("DISABLED", "ENABLED")):
    if pair not in transitions:
        raise SystemExit(f"P6-200 lifecycle transition missing: {pair}")
PY

rg -q 'Resolve\(|Transition\(|StatusEnabled|StatusDisabled|ErrSDKIncompatible' "$root_dir/server/internal/modules/connector/registry.go" "$root_dir/server/internal/modules/connector/registry_test.go"
rg -q 'contractHash|compatibilityMin|compatibilityMax|allowedFields' "$manifest"
rg -q 'owner|drain|rollback|operatorAudit|NOT_GRANTED' "$root_dir/docs/phase-6-design.md" "$root_dir/docs/phase-6-requirements.md" "$spec"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson key "$(jq -c '.key' "$spec")" \
  --argjson manifest "$(jq -c '.manifest' "$spec")" \
  --argjson lifecycle "$(jq -c '.lifecycle' "$spec")" \
  --argjson compatibility "$(jq -c '.compatibility' "$spec")" \
  --argjson entries "$(jq -c '.entries' "$manifest")" \
  '{schemaVersion:1,phase:"6",task:"P6-200",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,key:$key,manifest:$manifest,lifecycle:$lifecycle,compatibility:$compatibility,entries:$entries,prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Resolve only exact enabled keys inside the inclusive SDK window; disable drains in-flight work and quarantines unknown results."}' \
  >"$output"
cat "$output"
