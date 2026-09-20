#!/usr/bin/env bash
set -Eeuo pipefail

# P6-001: inventory the real four-repository contract/schema assets. This is
# read-only and does not enable connectors, publish NATS messages, or start a
# workflow.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P6_INVENTORY_OUTPUT:-$root_dir/docs/phase-6-connector-schema-inventory.json}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p6-inventory.XXXXXX")"
trap 'rm -rf -- "$runtime_dir"' EXIT HUP INT TERM

for command in git jq find shasum python3; do
  command -v "$command" >/dev/null || { echo "P6-001 missing command: $command" >&2; exit 2; }
done
for repo in "$root_dir" "$approver_root" "$fluxion_root" "$bids_root"; do
  [[ -d "$repo/.git" ]] || { echo "P6-001 repository is missing: $repo" >&2; exit 1; }
done

spec="$root_dir/deploy/local/p6/p6-001-inventory-spec.json"
jq -e '.version == 1 and .phase == "6" and .task == "P6-001" and .trackedOnly == true and .liveTraffic == false and .failClosed == true and (.requiredFamilies | length) == 3' "$spec" >/dev/null
mkdir -p "$(dirname "$output")"

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$spec" "$output" <<'PY'
import hashlib
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, spec_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())

def git(root: pathlib.Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True)

def commit(root: pathlib.Path) -> str:
    return git(root, "rev-parse", "HEAD").strip()

def tracked(root: pathlib.Path, relative: str) -> bool:
    return relative in {line for line in git(root, "ls-files").splitlines()}

def file_digest(path: pathlib.Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()

def version_from(path: pathlib.Path) -> str:
    try:
        value = json.loads(path.read_text())
    except Exception:
        return "UNPARSED"
    for key in ("schemaVersion", "version", "$id"):
        if key in value:
            return str(value[key])
    return "UNSPECIFIED"

def inventory(owner: str, repo: pathlib.Path, base: str) -> list[dict]:
    base_path = repo / base
    rows = []
    for path in sorted(base_path.rglob("*")) if base_path.exists() else []:
        if not path.is_file() or (path.suffix != ".json" and path.name != "manifest.json"):
            continue
        relative = path.relative_to(repo).as_posix()
        if not tracked(repo, relative) or "/target/" in f"/{relative}" or "/build/" in f"/{relative}":
            continue
        relative_parts = path.relative_to(base_path).parts
        family = relative_parts[0] if len(relative_parts) > 1 and relative_parts[0] not in {"testdata", "manifest.json"} else "root"
        if family == "root" and "summary" in path.name:
            family = "summaries"
        if "testdata" in relative_parts:
            kind = "fixture"
            fixture_index = relative_parts.index("testdata")
            family = relative_parts[fixture_index - 1] if fixture_index > 0 else "root"
        else:
            kind = "manifest" if path.name == "manifest.json" else "schema"
        rows.append({
            "owner": owner,
            "path": relative,
            "family": family,
            "kind": kind,
            "schemaVersion": version_from(path),
            "sha256": file_digest(path),
            "sourceCommit": commit(repo),
        })
    return rows

roots = {
    "record-hub": (record_hub, "contracts"),
    "approver": (approver, "approver-contract/src/main/resources/record-hub"),
    "fluxion": (fluxion, "server/src/main/resources/record-hub"),
    "bids": (bids, "backend/internal/recordhub/contracts"),
}
rows = []
for owner, (repo, base) in roots.items():
    rows.extend(inventory(owner, repo, base))
families = sorted({row["family"] for row in rows})
owners = sorted({row["owner"] for row in rows})
required_families = set(spec["requiredFamilies"])
status = "PASS" if len(rows) > 0 and set(owners) == set(spec["owners"]) and required_families.issubset(set(families)) else "FAIL"
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-001",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "trackedOnly": True,
    "liveTraffic": False,
    "owners": owners,
    "families": families,
    "entries": rows,
    "excluded": ["generated classes", "build outputs", "target directories", "untracked files"],
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Use this inventory to define P6-200 registry entries and P6-201 SDK parity; do not enable unknown connectors.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-001", "status": status, "entries": len(rows), "families": families}, ensure_ascii=False))
if status != "PASS":
    raise SystemExit(1)
PY
cat "$output"
