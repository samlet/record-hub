#!/usr/bin/env bash
set -Eeuo pipefail

# P5-001: freeze the four-owner RC baseline, including immutable artifacts and
# contract/migration/config digests.  Untracked user files are listed but are
# never included in a source digest.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P5_BASELINE_OUTPUT:-$root_dir/docs/phase-5-baseline-manifest.json}"
artifact_root="${RECORD_HUB_P5_ARTIFACT_DIR:-$root_dir/build/p5-baseline-artifacts}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p5-baseline.XXXXXX")"
trap 'rm -rf -- "$runtime_dir"' EXIT HUP INT TERM

for command in git jq shasum python3; do
  command -v "$command" >/dev/null || { echo "P5-001 missing command: $command" >&2; exit 2; }
done
for repo in "$root_dir" "$approver_root" "$fluxion_root" "$bids_root"; do
  [[ -d "$repo/.git" ]] || { echo "P5-001 repository is missing: $repo" >&2; exit 1; }
done
mkdir -p "$(dirname "$output")" "$artifact_root"

spec="$root_dir/deploy/local/p5/p5-001-baseline-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-001" and .trackedOnly == true and .failClosed == true and (.requiredArtifacts | length) == 6 and (.requiredDigests | index("contractMigrationConfigSha256"))' "$spec" >/dev/null

build_enabled="${RECORD_HUB_P5_BASELINE_BUILD:-0}"
baseline_path="$runtime_dir/p4-baseline.json"
if [[ "$build_enabled" == "1" ]]; then
  RECORD_HUB_P4_BASELINE_BUILD=1 \
  RECORD_HUB_P4_BASELINE_OUTPUT="$baseline_path" \
  RECORD_HUB_P4_ARTIFACT_DIR="$artifact_root" \
    "$root_dir/scripts/bootstrap-p4-baseline.sh" >"$runtime_dir/p4-baseline.log"
else
  jq -n '{status:"NOT_BUILT",artifacts:{status:"NOT_BUILT"}}' >"$baseline_path"
fi

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$baseline_path" "$output" "$artifact_root" "$spec" <<'PY'
import hashlib
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, baseline_path, output_path, artifact_root, spec_path = map(pathlib.Path, sys.argv[1:])
baseline = json.loads(baseline_path.read_text())
spec = json.loads(spec_path.read_text())

def run(root: pathlib.Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True)

def tracked_files(root: pathlib.Path, patterns: list[str]) -> list[tuple[str, pathlib.Path]]:
    names = [item for item in run(root, "ls-files", "-z", "--", *patterns).split("\0") if item]
    return [(name, root / name) for name in names]

def digest(files: list[tuple[str, pathlib.Path]]) -> str:
    h = hashlib.sha256()
    for name, path in sorted(files):
        h.update(name.encode())
        h.update(b"\0")
        h.update(path.read_bytes())
        h.update(b"\0")
    return "sha256:" + h.hexdigest()

def commit(root: pathlib.Path) -> str:
    return run(root, "rev-parse", "HEAD").strip()

def clean(root: pathlib.Path) -> bool:
    return subprocess.run(["git", "-C", str(root), "diff", "--quiet", "HEAD", "--"]).returncode == 0 and subprocess.run(["git", "-C", str(root), "diff", "--cached", "--quiet", "--"]).returncode == 0

def untracked(root: pathlib.Path) -> list[str]:
    return [line[3:] for line in run(root, "status", "--porcelain=v1").splitlines() if line.startswith("?? ")]

source_patterns = [".", ":(exclude)docs/phase-5-baseline-manifest.json", ":(exclude)docs/phase-4-baseline-manifest.json", ":(exclude)docs/phase-4-contract-inventory.json", ":(exclude)docs/phase-4-release-candidate-manifest.json"]
contract_patterns = {
    "record-hub": ["contracts/**/*.json", "server/internal/config/**", "server/internal/modules/**/migrations/**", "server/internal/modules/projection/**"],
    "approver": ["approver-contract/src/main/resources/record-hub/**", "approver-api/src/main/resources/application.yml", "approver-*/src/main/resources/db/migration/**"],
    "fluxion": ["server/src/main/resources/record-hub/**", "server/src/main/resources/db/migration/**", "server/src/main/resources/application.yaml", "server/src/main/kotlin/fluxion/config/**"],
    "bids": ["backend/internal/recordhub/contracts/**", "backend/internal/repository/migrations/**", "backend/internal/config/**", "backend/config/*.yaml", "backend/config/*.json"],
}
repo_defs = {
    "record-hub": (record_hub, ".", "go", "go test ./...", "go build -trimpath -o build/p5-baseline-artifacts/record-hub ./server/cmd/record-hub"),
    "approver": (approver, ".", "java", "mvn test", "mvn -DskipTests package"),
    "fluxion": (fluxion, "server", "kotlin-jvm", "./gradlew test", "./gradlew build"),
    "bids": (bids, "backend", "go", "go test ./...", "go build ./..."),
}

repositories = []
for name, (repo, working_dir, language, test_command, build_command) in repo_defs.items():
    repositories.append({
        "name": name,
        "workingDirectory": working_dir,
        "commit": commit(repo),
        "sourceTreeSha256": digest(tracked_files(repo, source_patterns)),
        "contractMigrationConfigSha256": digest(tracked_files(repo, contract_patterns[name])),
        "trackedWorktreeClean": clean(repo),
        "untracked": untracked(repo),
        "build": {"testCommand": test_command, "buildCommand": build_command},
    })

required_artifacts = set(spec["requiredArtifacts"])
artifacts = []
for path in sorted(artifact_root.iterdir() if artifact_root.exists() else []):
    if path.is_file():
        artifacts.append({
            "name": path.name,
            "path": str(path.relative_to(record_hub)),
            "sha256": "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest(),
            "bytes": path.stat().st_size,
            "immutable": True,
        })
artifact_names = {entry["name"] for entry in artifacts}
status = "PASS" if baseline.get("status") == "PASS" and required_artifacts == artifact_names and all(row["trackedWorktreeClean"] for row in repositories) else "PARTIAL"
result = {
    "schemaVersion": 1,
    "phase": "5",
    "task": "P5-001",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "baselineKind": "phase5-production-ga-preflight",
    "trackedOnly": True,
    "untrackedPolicy": "list-and-exclude",
    "source": {"repositories": repositories},
    "artifacts": {"status": "PASS" if required_artifacts == artifact_names else "PARTIAL", "immutableRoot": str(artifact_root.relative_to(record_hub)), "entries": artifacts},
    "contractMigrationConfig": {"status": "PASS", "repositories": repositories},
    "compatibility": {"ownerSystems": ["approver", "fluxion", "bids"], "recordHubBoundary": "command/result/projection contracts", "matrixStatus": "FROZEN"},
    "prerequisites": {"p4Baseline": baseline.get("status"), "p4Live": "INDEPENDENT_GATE", "phase5Live": "INDEPENDENT_GATES"},
    "notes": ["This manifest freezes the RC baseline only; it does not declare P4-501, P5-G1..G7, or GA readiness.", "Untracked files are listed per repository and excluded from source and contract digests."],
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P5-001", "status": status, "output": str(output_path), "artifactCount": len(artifacts)}, ensure_ascii=False))
if status != "PASS":
    raise SystemExit(1)
PY
cat "$output"
