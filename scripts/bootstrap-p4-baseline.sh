#!/usr/bin/env bash
set -Eeuo pipefail

# P4-001: capture source, runtime, build-command and artifact baselines for
# the four repositories.  The manifest intentionally records tracked source
# state only; untracked user files are listed but never included in a digest.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P4_BASELINE_OUTPUT:-$root_dir/docs/phase-4-baseline-manifest.json}"
build_enabled="${RECORD_HUB_P4_BASELINE_BUILD:-0}"
artifact_root="${RECORD_HUB_P4_ARTIFACT_DIR:-$root_dir/build/p4-baseline-artifacts}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p4-baseline.XXXXXX")"
trap 'rm -rf -- "$runtime_dir"' EXIT HUP INT TERM
mkdir -p "$(dirname "$output")" "$artifact_root"

for command in git jq shasum; do
  command -v "$command" >/dev/null || { echo "P4-001 missing command: $command" >&2; exit 2; }
done

sha256_file() {
  shasum -a 256 "$1" | awk '{print "sha256:"$1}'
}

source_digest() {
  local repo="$1"
  (cd "$repo" && git ls-files -z -- . \
    ':(exclude)docs/phase-4-baseline-manifest.json' \
    ':(exclude)docs/phase-4-contract-inventory.json' \
    | xargs -0 shasum -a 256 | shasum -a 256 | awk '{print "sha256:"$1}')
}

tracked_clean() {
  local repo="$1"
  git -C "$repo" diff --quiet HEAD -- && git -C "$repo" diff --cached --quiet --
}

untracked_json() {
  local repo="$1"
  git -C "$repo" status --porcelain=v1 | awk '$1 == "??" {sub(/^.. /, ""); print}' | jq -R -s 'split("\n") | map(select(length > 0))'
}

runtime_for() {
  case "$1" in
    record-hub|bids) command -v go >/dev/null && go version || true ;;
    approver|fluxion) command -v java >/dev/null && java -version 2>&1 | head -1 || true ;;
  esac
}

repo_json() {
  local name="$1" repo="$2" working_dir="$3" test_command="$4" build_command="$5" language="$6"
  [[ -d "$repo" ]] || { echo "missing repository: $repo" >&2; exit 1; }
  local clean=false
  tracked_clean "$repo" && clean=true
  jq -cn \
    --arg name "$name" --arg workingDirectory "$working_dir" \
    --arg commit "$(git -C "$repo" rev-parse HEAD)" \
    --arg sourceTreeSha256 "$(source_digest "$repo")" \
    --arg testCommand "$test_command" --arg buildCommand "$build_command" \
    --arg language "$language" --arg runtime "$(runtime_for "$name")" \
    --argjson trackedWorktreeClean "$clean" --argjson untracked "$(untracked_json "$repo")" \
    '{name:$name,workingDirectory:$workingDirectory,commit:$commit,sourceTreeSha256:$sourceTreeSha256,trackedWorktreeClean:$trackedWorktreeClean,untracked:$untracked,build:{testCommand:$testCommand,buildCommand:$buildCommand},runtime:{language:$language,version:$runtime}}'
}

artifact_row() {
  local name="$1" repo="$2" file="$3" command="$4"
  local bytes
  bytes="$(stat -f '%z' "$file" 2>/dev/null || stat -c '%s' "$file")"
  jq -cn --arg name "$name" --arg repository "$repo" --arg path "${file#"$root_dir"/}" \
    --arg sha256 "$(sha256_file "$file")" --arg command "$command" --argjson bytes "$bytes" \
    '{name:$name,repository:$repository,path:$path,sha256:$sha256,bytes:$bytes,buildCommand:$command,status:"PASS"}' \
    >>"$runtime_dir/artifacts.jsonl"
}

if [[ "$build_enabled" == "1" ]]; then
  mkdir -p "$artifact_root"
  go build -trimpath -o "$artifact_root/record-hub" "$root_dir/server/cmd/record-hub"
  artifact_row record-hub record-hub "$artifact_root/record-hub" 'go build -trimpath -o build/p4-baseline-artifacts/record-hub ./server/cmd/record-hub'

  (cd "$approver_root" && mvn -q -DskipTests package)
  approver_api_jar="$(find "$approver_root/approver-api/target" -maxdepth 1 -type f -name 'approver-api-*.jar' ! -name '*-plain.jar' | head -1)"
  approver_worker_jar="$(find "$approver_root/approver-worker/target" -maxdepth 1 -type f -name 'approver-worker-*.jar' ! -name '*-plain.jar' | head -1)"
  [[ -n "$approver_api_jar" && -n "$approver_worker_jar" ]] || { echo "Approver jars were not created" >&2; exit 1; }
  cp "$approver_api_jar" "$artifact_root/approver-api.jar"
  cp "$approver_worker_jar" "$artifact_root/approver-worker.jar"
  artifact_row approver-api approver "$artifact_root/approver-api.jar" 'mvn -DskipTests package'
  artifact_row approver-worker approver "$artifact_root/approver-worker.jar" 'mvn -DskipTests package'

  (cd "$fluxion_root/server" && ./gradlew -q build)
  fluxion_jar="$(find "$fluxion_root/server/build/libs" -maxdepth 1 -type f -name '*.jar' ! -name '*-plain.jar' | head -1)"
  [[ -n "$fluxion_jar" ]] || { echo "Fluxion jar was not created" >&2; exit 1; }
  cp "$fluxion_jar" "$artifact_root/fluxion-server.jar"
  artifact_row fluxion-server fluxion "$artifact_root/fluxion-server.jar" './gradlew build'

  (cd "$bids_root/backend" && go build -trimpath -o "$artifact_root/bids-api" ./cmd/api && go build -trimpath -o "$artifact_root/bids-worker" ./cmd/worker)
  artifact_row bids-api bids "$artifact_root/bids-api" 'go build -trimpath -o build/p4-baseline-artifacts/bids-api ./cmd/api'
  artifact_row bids-worker bids "$artifact_root/bids-worker" 'go build -trimpath -o build/p4-baseline-artifacts/bids-worker ./cmd/worker'
else
  : >"$runtime_dir/artifacts.jsonl"
fi

repositories_json="$(
  repo_json record-hub "$root_dir" . 'go test ./...' 'go build -trimpath -o build/record-hub ./server/cmd/record-hub' go
  repo_json approver "$approver_root" . 'mvn test' 'mvn -DskipTests package' java
  repo_json fluxion "$fluxion_root" server './gradlew test' './gradlew build' kotlin-jvm
  repo_json bids "$bids_root" backend 'go test ./...' 'go build ./...' go
)"
printf '%s\n' "$repositories_json" | jq -s '.' >"$runtime_dir/repositories.json"
artifacts="$(jq -s '.' "$runtime_dir/artifacts.jsonl")"
source_clean="$(jq -e 'all(.[]; .trackedWorktreeClean)' "$runtime_dir/repositories.json" >/dev/null && echo true || echo false)"
artifact_status="NOT_BUILT"
manifest_status="PARTIAL"
if [[ "$build_enabled" == "1" ]]; then
  artifact_status="PASS"
  manifest_status="PASS"
  [[ "$source_clean" == "true" ]] || manifest_status="FAIL"
fi

jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg status "$manifest_status" --arg artifactStatus "$artifact_status" \
  --arg baselineKind "phase4-integration-beta" --arg buildMode "$([[ "$build_enabled" == "1" ]] && echo build || echo source-only)" \
  --argjson repositories "$(cat "$runtime_dir/repositories.json")" --argjson artifacts "$artifacts" \
  --argjson sourceClean "$source_clean" \
  '{schemaVersion:1,phase:"4",baselineKind:$baselineKind,status:$status,generatedAt:$generatedAt,buildMode:$buildMode,source:{trackedWorktreesClean:$sourceClean},repositories:$repositories,artifacts:{status:$artifactStatus,entries:$artifacts},contractGate:{commands:"make p3-contract-gate",evidenceContract:"make p4-evidence-contract",inventory:"make p4-contract-inventory"},notes:["Untracked files are listed per repository but excluded from sourceTreeSha256.","Artifact paths are under the ignored build/p4-baseline-artifacts directory."]}' \
  >"$output"
cat "$output"
[[ "$manifest_status" == "PASS" ]] || { echo "P4-001 baseline is $manifest_status; rerun with RECORD_HUB_P4_BASELINE_BUILD=1 and a tracked-clean worktree" >&2; exit 1; }
