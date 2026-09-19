#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

for command in jq shasum; do
  command -v "$command" >/dev/null || { echo "$command is required for P3 fixture bootstrap" >&2; exit 2; }
done

spec_file="$root_dir/deploy/local/p3/p3-fixture-spec.json"
output_dir="${RECORD_HUB_P3_FIXTURE_DIR:-$root_dir/.runtime/p3/fixtures}"
mkdir -p "$output_dir"
tmp_dir="$(mktemp -d "$output_dir/.staging.XXXXXX")"
cleanup() { rm -rf -- "$tmp_dir"; }
trap cleanup EXIT HUP INT TERM

jq -e '(.version == 1) and (.owners | length == 3) and (.schemas | length >= 5)' "$spec_file" >/dev/null

policy_files=(
  "approver-application-annotate-policy.json"
  "fluxion-project-annotate-policy.json"
  "bids-tender-annotate-policy.json"
)
for policy_file in "${policy_files[@]}"; do
  jq -e '(.version == 1) and (.policies | length == 1) and (.policies[0].expectedVersionRequired == true) and (.policies[0].maxPayloadBytes == 262144)' \
    "$root_dir/deploy/local/p3/$policy_file" >/dev/null
done
jq -e 'length == 2 and all(.[]; (.issuer | length > 0) and (.subject | length > 0) and (.scope == "recordhub.binding.snapshot") and (.purpose == "diagnostic") and (.resourceType == (if .resourceSystem == "fluxion" then "PROJECT" else "TENDER" end)))' \
  "$root_dir/deploy/local/p3/binding-machine-policies.json" >/dev/null

jq -S '.' "$spec_file" >"$tmp_dir/topology.json"
jq -S -s '[.[].policies[]]' "${policy_files[@]/#/$root_dir/deploy/local/p3/}" >"$tmp_dir/command-policies.json"
jq -S '.owners' "$spec_file" >"$tmp_dir/owner-fixtures.json"
jq -S '.schemas' "$spec_file" >"$tmp_dir/schema-plan.json"
jq -S '.' "$root_dir/deploy/local/p3/binding-machine-policies.json" >"$tmp_dir/binding-machine-policies.json"
jq -c '.' "$tmp_dir/command-policies.json" >"$tmp_dir/record-hub-command-policies.env.json"

for file in topology.json command-policies.json owner-fixtures.json schema-plan.json binding-machine-policies.json record-hub-command-policies.env.json; do
  mv "$tmp_dir/$file" "$output_dir/$file"
done

entries='[]'
for file in topology.json command-policies.json owner-fixtures.json schema-plan.json binding-machine-policies.json record-hub-command-policies.env.json; do
  hash="$(shasum -a 256 "$output_dir/$file" | awk '{print $1}')"
  entries="$(jq -c --arg path "$file" --arg sha256 "$hash" '. + [{path:$path,sha256:$sha256}]' <<<"$entries")"
done
jq -S -n --arg generatedBy "scripts/bootstrap-p3-fixtures.sh" --argjson entries "$entries" \
  '{version:1,generatedBy:$generatedBy,entries:$entries}' >"$output_dir/manifest.json"

echo "P3 fixture set ready: $output_dir"
echo "P3 fixture hash: $(shasum -a 256 "$output_dir/manifest.json" | awk '{print $1}')"
