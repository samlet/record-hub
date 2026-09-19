#!/usr/bin/env bash
set -Eeuo pipefail

# P4-003: validate the evidence manifest contract and provide the persistent
# directory convention used by Phase 4 runners.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_root="$root_dir/contracts/evidence"
manifest="$contract_root/manifest.json"
schema="$contract_root/phase4-evidence-manifest-v1.schema.json"
valid="$contract_root/testdata/valid/phase4-evidence-manifest-v1.json"
invalid="$contract_root/testdata/invalid/phase4-evidence-manifest-v1-status.json"
for file in "$manifest" "$schema" "$valid" "$invalid"; do
  [[ -f "$file" ]] || { echo "P4-003 missing evidence contract asset: $file" >&2; exit 1; }
done
command -v jq >/dev/null || { echo "P4-003 missing command: jq" >&2; exit 2; }
command -v shasum >/dev/null || { echo "P4-003 missing command: shasum" >&2; exit 2; }
go test ./contracts/evidence -count=1

jq -e '.version == 1 and .contractFamily == "record-hub-phase4-evidence" and (.contracts | length == 1)' "$manifest" >/dev/null
jq -e '."$id" == "urn:record-hub:phase4-evidence-manifest:v1" and .additionalProperties == false' "$schema" >/dev/null
jq -e '.schemaVersion == 1 and .status == "PASS" and (.repositories | length == 4) and .redactions.secretsRemoved and .redactions.payloadsOmitted and all(.files[]; ((.path | startswith("/") | not) and (.path | contains("..") | not)))' "$valid" >/dev/null
if jq -e '.status != "UNKNOWN"' "$invalid" >/dev/null 2>&1; then
  echo "P4-003 invalid fixture unexpectedly accepted" >&2
  exit 1
fi
schema_hash="sha256:$(shasum -a 256 "$schema" | awk '{print $1}')"
valid_hash="sha256:$(shasum -a 256 "$valid" | awk '{print $1}')"
evidence_root="${RECORD_HUB_P4_EVIDENCE_ROOT:-$root_dir/build/evidence/phase4}"
mkdir -p "$evidence_root"
jq -n --arg schemaHash "$schema_hash" --arg validFixtureHash "$valid_hash" --arg evidenceRoot "${evidence_root#"$root_dir"/}" \
  '{gate:"P4-003",status:"PASS",schema:"contracts/evidence/phase4-evidence-manifest-v1.schema.json",schemaSha256:$schemaHash,validFixtureSha256:$validFixtureHash,persistentRoot:$evidenceRoot,layout:["manifest.json","config-redacted.json","results.json","checksums.sha256","logs/","metrics/","db-assertions/","security/","recovery/","release/"]}'
