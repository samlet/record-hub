#!/usr/bin/env bash
set -Eeuo pipefail

# P6-101: validate the controlled tag dictionary and its audit/scope boundary.
# This static gate never mutates records or enables connector traffic.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-101-tag-contract-spec.json"
output="${RECORD_HUB_P6_TAG_CONTRACT_OUTPUT:-$root_dir/docs/phase-6-tags-contract.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-101 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-101" and
  .liveTraffic == false and .failClosed == true and
  .scope.crossTenantAssignment == "DENY" and
  .scope.unknownTagAction == "QUARANTINE" and
  .security.unknownAssignmentAction == "QUARANTINE" and
  .audit.immutable == true
' "$spec" >/dev/null

python3 - "$spec" <<'PY'
import json
import pathlib
import re
import sys

spec = json.loads(pathlib.Path(sys.argv[1]).read_text())
levels = set(spec["scope"]["levels"])
pattern = re.compile(spec["tagId"]["pattern"])
entries = spec["dictionary"]
if not entries:
    raise SystemExit("P6-101 dictionary must not be empty")
ids = [item["id"] for item in entries]
if len(ids) != len(set(ids)):
    raise SystemExit("P6-101 duplicate tag id")
groups = {}
for item in entries:
    if not pattern.fullmatch(item["id"]):
        raise SystemExit(f"P6-101 invalid tag id: {item['id']}")
    if item["id"] != item["id"].lower() or item["scope"] not in levels:
        raise SystemExit(f"P6-101 non-normalized tag or invalid scope: {item['id']}")
    groups.setdefault(item["group"], []).append(item["id"])
if not all(item["group"] in groups for item in entries):
    raise SystemExit("P6-101 tag group missing")
configured_groups = {item["group"] for item in spec["mutualExclusion"]}
if not configured_groups.issubset(groups):
    raise SystemExit("P6-101 mutual exclusion references unknown group")
if any(item["mode"] != "at-most-one-active" for item in spec["mutualExclusion"]):
    raise SystemExit("P6-101 unsupported mutual exclusion mode")
required_audit = {
    "eventId", "action", "actor", "scope", "target", "before", "after",
    "reason", "correlationId", "occurredAt",
}
if not required_audit.issubset(set(spec["audit"]["requiredFields"])):
    raise SystemExit("P6-101 audit fields are incomplete")
if not {"assign", "unassign", "quarantine"}.issubset(set(spec["audit"]["actions"])):
    raise SystemExit("P6-101 audit actions are incomplete")
for prefix in spec["security"]["forbiddenTagPrefixes"]:
    if any(tag_id.startswith(prefix) for tag_id in ids):
        raise SystemExit(f"P6-101 forbidden security tag prefix: {prefix}")
PY

rg -q 'tags|record tags' "$root_dir/api/openapi.yaml" "$root_dir/server/internal/modules/records" "$root_dir/web/src/lib/api.ts"
rg -q 'audit|redact|sensitive|PII' "$root_dir/docs/phase-6-design.md" "$root_dir/docs/phase-6-requirements.md" "$spec"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson scope "$(jq -c '.scope' "$spec")" \
  --argjson tagId "$(jq -c '.tagId' "$spec")" \
  --argjson dictionary "$(jq -c '.dictionary' "$spec")" \
  --argjson mutualExclusion "$(jq -c '.mutualExclusion' "$spec")" \
  --argjson apiOperations "$(jq -c '.apiOperations' "$spec")" \
  --argjson audit "$(jq -c '.audit' "$spec")" \
  --argjson security "$(jq -c '.security' "$spec")" \
  '{schemaVersion:1,phase:"6",task:"P6-101",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,scope:$scope,tagId:$tagId,dictionary:$dictionary,mutualExclusion:$mutualExclusion,apiOperations:$apiOperations,audit:$audit,security:$security,prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Only dictionary-approved tags may be assigned; quarantine unknown or cross-tenant assignments and retain immutable audit evidence."}' \
  >"$output"
cat "$output"
