#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
image=${GITLEAKS_IMAGE:-zricethezav/gitleaks:v8.28.0}

run_gitleaks() {
  docker run --rm \
    --volume "$repository_root:/repo:ro" \
    "$image" "$@" --config /repo/.gitleaks.toml --no-banner --no-color --redact
}

run_gitleaks git /repo

# Scan the current source tree, including untracked files, while respecting
# .gitignore so intentionally local credential files are never copied into the
# scanner workspace. Git history above remains the backstop for committed data.
scan_tree=$(mktemp -d "$repository_root/.gitleaks-scan.XXXXXX")
trap 'rm -rf "$scan_tree"' EXIT HUP INT TERM
git -C "$repository_root" ls-files --cached --others --exclude-standard -z |
  tar -C "$repository_root" --null -T - -cf - |
  tar -C "$scan_tree" -xf -

docker run --rm \
  --volume "$scan_tree:/scan:ro" \
  "$image" dir /scan --config /scan/.gitleaks.toml --no-banner --no-color --redact
