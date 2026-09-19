#!/usr/bin/env bash
set -Eeuo pipefail

# P4-004: create stable, secret-free fixtures for topology, tenant flags,
# approval slices, recovery targets and the expand/contract release window.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p4/p4-fixture-spec.json"
output_dir="${RECORD_HUB_P4_FIXTURE_DIR:-$root_dir/.runtime/p4/fixtures}"
command -v jq >/dev/null || { echo "P4-004 missing command: jq" >&2; exit 2; }
command -v shasum >/dev/null || { echo "P4-004 missing command: shasum" >&2; exit 2; }
[[ -f "$spec" ]] || { echo "P4-004 missing fixture spec: $spec" >&2; exit 1; }
jq -e '.version == 1 and .phase == "4" and (.tenants | length > 0) and (.owners | length == 3) and (.approvalSlices | length == 2) and (.releaseWindow.featureFlagDefault == "OFF")' "$spec" >/dev/null
mkdir -p "$output_dir"

jq '{version:.version,phase:.phase,tenant:.tenants[0],owners:.owners}' "$spec" >"$output_dir/topology.json"
jq '{version:.version,tenants:.tenants}' "$spec" >"$output_dir/tenant-flags.json"
jq '{version:.version,approvalSlices:.approvalSlices}' "$spec" >"$output_dir/approval-slices.json"
jq '{version:.version,recoveryTargets:.recoveryTargets}' "$spec" >"$output_dir/recovery-plan.json"
jq '{version:.version,releaseWindow:.releaseWindow}' "$spec" >"$output_dir/release-window.json"

manifest_tmp="$(mktemp "${TMPDIR:-/tmp}/record-hub-p4-fixture-manifest.XXXXXX")"
trap 'rm -f -- "$manifest_tmp"' EXIT HUP INT TERM
{
  printf '{"version":1,"sourceSpec":"deploy/local/p4/p4-fixture-spec.json","files":['
  first=1
  for file in approval-slices.json recovery-plan.json release-window.json tenant-flags.json topology.json; do
    [[ "$first" == "1" ]] || printf ','
    first=0
    hash="sha256:$(shasum -a 256 "$output_dir/$file" | awk '{print $1}')"
    bytes="$(stat -f '%z' "$output_dir/$file" 2>/dev/null || stat -c '%s' "$output_dir/$file")"
    jq -cn --arg path "$file" --arg sha256 "$hash" --argjson bytes "$bytes" '{path:$path,sha256:$sha256,bytes:$bytes}'
  done
  printf ']}\n'
} >"$manifest_tmp"
jq . "$manifest_tmp" >"$output_dir/manifest.json"
jq -e '.version == 1 and (.files | length == 5) and all(.files[]; (.sha256 | startswith("sha256:")))' "$output_dir/manifest.json" >/dev/null
echo "P4-004 fixtures generated: $output_dir"
cat "$output_dir/manifest.json"
