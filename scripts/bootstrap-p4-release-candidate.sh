#!/usr/bin/env bash
set -Eeuo pipefail

# P4-500: build the four-owner release candidate, freeze source/contract/
# migration/config digests, and make artifact replacement detectable.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P4_RC_OUTPUT:-$root_dir/docs/phase-4-release-candidate-manifest.json}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p4-rc.XXXXXX")"
trap 'rm -rf -- "$runtime_dir"' EXIT HUP INT TERM
for command in git jq shasum python3 diff; do command -v "$command" >/dev/null || { echo "P4-500 missing command: $command" >&2; exit 2; }; done

record_commit="$(git -C "$root_dir" rev-parse HEAD)"
artifact_root="$root_dir/build/p4-release-candidate/rc-${record_commit:0:12}"
mkdir -p "$artifact_root"
find "$artifact_root" -type f -print0 | xargs -0 -r shasum -a 256 | sort >"$runtime_dir/artifacts.before"

RECORD_HUB_P4_BASELINE_BUILD=1 \
RECORD_HUB_P4_BASELINE_OUTPUT="$runtime_dir/baseline.json" \
RECORD_HUB_P4_ARTIFACT_DIR="$artifact_root" \
  "$root_dir/scripts/bootstrap-p4-baseline.sh" >"$runtime_dir/baseline.log"

find "$artifact_root" -type f -print0 | xargs -0 -r shasum -a 256 | sort >"$runtime_dir/artifacts.after"
if [[ -s "$runtime_dir/artifacts.before" ]] && ! diff -u "$runtime_dir/artifacts.before" "$runtime_dir/artifacts.after" >/dev/null; then
  echo "P4-500 immutable artifact violation: existing RC artifacts changed" >&2
  exit 1
fi

RECORD_HUB_P4_CONTRACT_INVENTORY_OUTPUT="$runtime_dir/contract-inventory.json" make -C "$root_dir" p3-contract-gate >"$runtime_dir/contracts.log"
RECORD_HUB_P4_CONTRACT_INVENTORY_OUTPUT="$runtime_dir/contract-inventory.json" make -C "$root_dir" p4-contract-inventory >>"$runtime_dir/contracts.log"
make -C "$root_dir" p4-evidence-contract >"$runtime_dir/evidence.log"
make -C "$root_dir" p4-batch4 >"$runtime_dir/batch4.log"

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$runtime_dir/baseline.json" "$runtime_dir/contract-inventory.json" "$output" "$artifact_root" <<'PY'
import hashlib
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, baseline_path, inventory_path, output_path, artifact_root = map(pathlib.Path, sys.argv[1:])
output = str(output_path)
baseline = json.loads(baseline_path.read_text())
inventory = json.loads(inventory_path.read_text())

def tracked_files(root: pathlib.Path, patterns: list[str]) -> list[pathlib.Path]:
    rows = subprocess.check_output(["git", "-C", str(root), "ls-files", "-z", "--", *patterns])
    names = [item for item in rows.decode().split("\0") if item]
    return [root / name for name in names]

def digest(files: list[pathlib.Path]) -> str:
    h = hashlib.sha256()
    for path in sorted(files):
        h.update(path.relative_to(path.parents[len(path.parents) - 1]).as_posix().encode())
        h.update(b"\0")
        h.update(path.read_bytes())
        h.update(b"\0")
    return "sha256:" + h.hexdigest()

def commit(root: pathlib.Path) -> str:
    return subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()

repos = {
    "record-hub": (record_hub, ["contracts/**/*.json", "server/internal/config/**", "server/internal/modules/**/migrations/**", "server/internal/modules/projection/**"]),
    "approver": (approver, ["approver-contract/src/main/resources/record-hub/**", "approver-api/src/main/resources/application.yml", "approver-*/src/main/resources/db/migration/**"]),
    "fluxion": (fluxion, ["server/src/main/resources/record-hub/**", "server/src/main/resources/db/migration/**", "server/src/main/resources/application.yaml", "server/src/main/kotlin/fluxion/config/**"]),
    "bids": (bids, ["backend/internal/recordhub/contracts/**", "backend/internal/repository/migrations/**", "backend/internal/config/**", "backend/config/*.yaml", "backend/config/*.json"]),
}
summaries = []
for name, (root, patterns) in repos.items():
    files = tracked_files(root, patterns)
    summaries.append({
        "repository": name,
        "commit": commit(root),
        "trackedFileCount": len(files),
        "contractMigrationConfigSha256": digest(files),
        "scope": {"trackedOnly": True, "untrackedExcluded": True},
    })

artifact_entries = []
for path in sorted(artifact_root.iterdir()):
    if path.is_file():
        artifact_entries.append({
            "name": path.name,
            "path": str(path.relative_to(record_hub)),
            "sha256": "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest(),
            "bytes": path.stat().st_size,
        })

result = {
    "schemaVersion": 1,
    "phase": "4",
    "gate": "P4-500",
    "status": "PASS" if baseline.get("status") == "PASS" and len(artifact_entries) == 6 else "FAIL",
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "releaseCandidate": "rc-" + commit(record_hub)[:12],
    "source": {"repositories": summaries, "untrackedPolicy": "excluded-from-digest-listed-by-baseline"},
    "artifacts": {"status": "PASS" if len(artifact_entries) == 6 else "FAIL", "immutableRoot": str(artifact_root.relative_to(record_hub)), "entries": artifact_entries},
    "gates": {"baseline": baseline.get("status"), "contractInventory": inventory.get("status"), "evidenceContract": "PASS", "batch4": "PARTIAL_STATIC_PASS_LIVE_SKIPPED"},
    "migrationContractConfig": {"status": "PASS", "repositories": summaries},
    "live": {"status": "SKIPPED", "reason": "P4-501 requires isolated four-owner topology and explicit rotation/restore/load/rollback evidence"},
    "next": "P4-501 is blocked until all required live cases can be executed without SKIPPED/PARTIAL.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"gate": "P4-500", "status": result["status"], "output": output, "artifactCount": len(artifact_entries)}, ensure_ascii=False))
if result["status"] != "PASS":
    raise SystemExit(1)
PY
cat "$output"
