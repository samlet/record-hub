#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
image=${SYFT_IMAGE:-anchore/syft:v1.33.0}
output=${SBOM_OUTPUT:-"$repository_root/build/record-hub.cdx.json"}

case "$output" in
  "$repository_root"/*) ;;
  *)
    echo "SBOM_OUTPUT must be inside the repository" >&2
    exit 2
    ;;
esac

test -f "$repository_root/build/record-hub"
mkdir -p "$(dirname -- "$output")"

docker run --rm \
  --volume "$repository_root:/repo:ro" \
  "$image" file:/repo/build/record-hub \
  --quiet \
  --output cyclonedx-json >"$output"

jq -e '.bomFormat == "CycloneDX" and (.components | length > 0)' "$output" >/dev/null
