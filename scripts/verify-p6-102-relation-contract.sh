#!/usr/bin/env bash
set -Eeuo pipefail

# P6-102: validate typed relation, safe summary and finding boundaries. This
# static gate never resolves findings or publishes a cross-owner relation.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-102-relation-contract-spec.json"
output="${RECORD_HUB_P6_RELATION_CONTRACT_OUTPUT:-$root_dir/docs/phase-6-relations-contract.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-102 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-102" and
  .liveTraffic == false and .failClosed == true and
  .typedRef.versionRequired == true and
  .relation.unknownRelationAction == "QUARANTINE" and
  .safeSummary.redactionAction == "DROP_AND_AUDIT" and
  .finding.rawPayload == "FORBIDDEN"
' "$spec" >/dev/null

python3 - "$spec" <<'PY'
import json
import pathlib
import re
import sys

spec = json.loads(pathlib.Path(sys.argv[1]).read_text())
ref = spec["typedRef"]
systems = set(ref["systemAllowlist"])
if len(systems) != len(ref["systemAllowlist"]):
    raise SystemExit("P6-102 duplicate typed-ref system")
if not all(re.fullmatch(ref["systemPattern"], value) for value in systems):
    raise SystemExit("P6-102 invalid typed-ref system")
if not re.fullmatch(ref["typePattern"], "Project") or not re.fullmatch(ref["idPattern"], "id-1"):
    raise SystemExit("P6-102 typed-ref patterns are unusable")
relation = spec["relation"]
if len(relation["allowedRelationTypes"]) != len(set(relation["allowedRelationTypes"])):
    raise SystemExit("P6-102 duplicate relation type")
if any(not re.fullmatch(relation["relationTypePattern"], value) for value in relation["allowedRelationTypes"]):
    raise SystemExit("P6-102 invalid relation type")
if set(relation["allowedStatus"]) != {"CURRENT", "GAP", "CONFLICT", "BROKEN", "FORBIDDEN"}:
    raise SystemExit("P6-102 relation status set is incomplete")
if len(relation["allowedCardinality"]) != 4:
    raise SystemExit("P6-102 cardinality set is incomplete")
source = spec["source"]
required_source = {"sourceVersion", "snapshotHash", "sourcePointer", "observedAt"}
if not required_source.issubset(set(source["requiredFields"])):
    raise SystemExit("P6-102 source metadata is incomplete")
if not re.fullmatch(source["hashPattern"], "sha256:" + "0" * 64):
    raise SystemExit("P6-102 hash pattern is unusable")
safe = spec["safeSummary"]
for field in ("payload", "rawPayload", "workflowInput", "accessToken", "privateKey", "secret"):
    if field not in safe["forbiddenFields"]:
        raise SystemExit(f"P6-102 sensitive field is not forbidden: {field}")
if set(safe["allowedFields"]) & set(safe["forbiddenFields"]):
    raise SystemExit("P6-102 summary allowlist overlaps forbidden fields")
finding = spec["finding"]
if not {"GAP", "CONFLICT", "HASH_MISMATCH", "FORBIDDEN"}.issubset(set(finding["allowedKinds"])):
    raise SystemExit("P6-102 finding kinds are incomplete")
if not {"findingId", "relationRef", "kind", "expectedVersion", "observedVersion", "sourcePointer", "detectedAt", "resolutionState"}.issubset(set(finding["requiredFields"])):
    raise SystemExit("P6-102 finding fields are incomplete")
PY

rg -q 'RecordRelation|RelationTarget|resolvedRecordId|FORBIDDEN' "$root_dir/api/openapi.yaml"
rg -q 'ProjectApplicationAssociation|AssociationGap|AssociationConflict|ProposalHash|SourceVersion' "$root_dir/server/internal/modules/projection/association.go"
rg -q 'sha256|PayloadHash|canonical' "$root_dir/server/internal/modules/projection" "$root_dir/contracts/bindings/snapshot-hash-v1.json"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson typedRef "$(jq -c '.typedRef' "$spec")" \
  --argjson relation "$(jq -c '.relation' "$spec")" \
  --argjson source "$(jq -c '.source' "$spec")" \
  --argjson safeSummary "$(jq -c '.safeSummary' "$spec")" \
  --argjson finding "$(jq -c '.finding' "$spec")" \
  '{schemaVersion:1,phase:"6",task:"P6-102",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,typedRef:$typedRef,relation:$relation,source:$source,safeSummary:$safeSummary,finding:$finding,prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Persist only typed refs and safe summaries; record gaps/conflicts as findings and quarantine forbidden or unverifiable relations."}' \
  >"$output"
cat "$output"
