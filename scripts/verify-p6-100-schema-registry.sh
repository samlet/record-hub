#!/usr/bin/env bash
set -Eeuo pipefail

# P6-100: validate the versioned semantic registry contract. This gate only
# freezes metadata and compatibility policy; it never enables record traffic.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-100-schema-registry-spec.json"
output="${RECORD_HUB_P6_SCHEMA_REGISTRY_OUTPUT:-$root_dir/docs/phase-6-schema-registry.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-100 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-100" and
  .liveTraffic == false and .failClosed == true and
  .vocabulary.baseIri == "https://schema.org/" and
  .vocabulary.allowlistMode == "EXPLICIT" and
  .vocabulary.unknownIriAction == "QUARANTINE" and
  .constraints.inheritance == "explicit-parent-acyclic" and
  .constraints.unknownTypeAction == "QUARANTINE" and
  .constraints.unknownPropertyAction == "QUARANTINE"
' "$spec" >/dev/null

python3 - "$spec" <<'PY'
import json
import pathlib
import re
import sys

spec = json.loads(pathlib.Path(sys.argv[1]).read_text())
base = spec["vocabulary"]["baseIri"]
iri_pattern = re.compile(spec["constraints"]["typeIriPattern"])
property_pattern = re.compile(spec["constraints"]["propertyIriPattern"])
types = spec["types"]
properties = spec["properties"]

if not types or not properties:
    raise SystemExit("P6-100 registry must define types and properties")
type_ids = [item["iri"] for item in types]
if len(type_ids) != len(set(type_ids)):
    raise SystemExit("P6-100 duplicate type IRI")
if spec["rootType"] not in type_ids:
    raise SystemExit("P6-100 rootType is not registered")
by_type = {item["iri"]: item for item in types}
for item in types:
    iri = item["iri"]
    if not iri_pattern.fullmatch(iri) or not iri.startswith(base):
        raise SystemExit(f"P6-100 invalid type IRI: {iri}")
    parent = item.get("parent")
    if iri == spec["rootType"]:
        if parent is not None:
            raise SystemExit("P6-100 root type must not have a parent")
    elif parent not in by_type or parent == iri:
        raise SystemExit(f"P6-100 invalid parent for {iri}: {parent}")

for start in type_ids:
    seen = set()
    current = start
    while current is not None:
        if current in seen:
            raise SystemExit(f"P6-100 inheritance cycle at {current}")
        seen.add(current)
        current = by_type[current].get("parent")

property_ids = [item["iri"] for item in properties]
if len(property_ids) != len(set(property_ids)):
    raise SystemExit("P6-100 duplicate property IRI")
allowed_types = set(spec["constraints"]["allowedFieldTypes"])
for item in properties:
    iri = item["iri"]
    if not property_pattern.fullmatch(iri) or not iri.startswith(base):
        raise SystemExit(f"P6-100 invalid property IRI: {iri}")
    if item["fieldType"] not in allowed_types:
        raise SystemExit(f"P6-100 unsupported field type: {item['fieldType']}")
    if not item.get("appliesTo") or any(value not in by_type for value in item["appliesTo"]):
        raise SystemExit(f"P6-100 property appliesTo references unknown type: {iri}")

required_changes = {
    ("add-optional-property", "COMPATIBLE"),
    ("remove-property", "BREAKING"),
    ("rename-property", "BREAKING"),
    ("narrow-field-type", "BREAKING"),
    ("add-required-property", "BREAKING"),
}
actual = {(item["change"], item["classification"]) for item in spec["compatibility"]["rules"]}
missing = required_changes - actual
if missing:
    raise SystemExit(f"P6-100 missing compatibility rules: {sorted(missing)}")
PY

rg -q 'NormalizeSemanticTypes|ErrInvalidSemanticType' "$root_dir/server/internal/modules/schema/semantic_types.go" "$root_dir/server/internal/modules/schema/semantic_types_test.go"
rg -q 'CheckBackwardCompatibility|property-removed|required-added|type-narrowed' "$root_dir/server/internal/modules/schema/compatibility.go"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson vocabulary "$(jq -c '.vocabulary' "$spec")" \
  --argjson types "$(jq -c '.types' "$spec")" \
  --argjson properties "$(jq -c '.properties' "$spec")" \
  --argjson compatibility "$(jq -c '.compatibility' "$spec")" \
  --argjson constraints "$(jq -c '.constraints' "$spec")" \
  '{schemaVersion:1,phase:"6",task:"P6-100",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,vocabulary:$vocabulary,rootType:"https://schema.org/Thing",types:$types,properties:$properties,compatibility:$compatibility,constraints:$constraints,prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Use only explicit registry entries; quarantine unknown IRIs and require a new version for breaking changes."}' \
  >"$output"
cat "$output"
