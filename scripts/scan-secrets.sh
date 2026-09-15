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
run_gitleaks dir /repo
