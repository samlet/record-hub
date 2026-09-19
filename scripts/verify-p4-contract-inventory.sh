#!/usr/bin/env bash
set -Eeuo pipefail

# P4-002: verify shared command/approval mirrors and inventory owner-specific
# summary contracts.  Summary schemas are intentionally not byte-identical:
# each owner publishes only the resource it owns.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P4_CONTRACT_INVENTORY_OUTPUT:-$root_dir/docs/phase-4-contract-inventory.json}"
mkdir -p "$(dirname "$output")"

"$root_dir/scripts/verify-p3-contract-mirrors.sh"
"$root_dir/scripts/verify-p3-approval-contract-mirrors.sh"
command -v jq >/dev/null || { echo "P4-002 missing command: jq" >&2; exit 2; }
command -v shasum >/dev/null || { echo "P4-002 missing command: shasum" >&2; exit 2; }

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$output" <<'PY'
import hashlib
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, output = map(pathlib.Path, sys.argv[1:])
roots = [
    ("record-hub", "summaries", record_hub / "contracts/summaries"),
    ("approver", "summaries", approver / "approver-contract/src/main/resources/record-hub/summaries"),
    ("fluxion", "summaries", fluxion / "server/src/main/resources/record-hub/summaries"),
    ("bids", "summaries", bids / "backend/internal/recordhub/contracts"),
]

def commit(path: pathlib.Path) -> str:
    return subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()

def sha(path: pathlib.Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()

entries = []
for repository, family, root in roots:
    if not root.is_dir():
        raise SystemExit(f"missing {family} contract root: {root}")
    manifest_path = root / "manifest.json"
    if not manifest_path.is_file():
        raise SystemExit(f"missing contract manifest: {manifest_path}")
    manifest = json.loads(manifest_path.read_text())
    if manifest.get("version") != 1 or not manifest.get("contracts"):
        raise SystemExit(f"invalid contract manifest: {manifest_path}")
    referenced = {"manifest.json"}
    contracts = []
    for contract in manifest["contracts"]:
        schema = root / contract["schemaFile"]
        fixture = root / contract.get("fixtureFile", contract.get("validFixtureFile", ""))
        if not schema.is_file() or not fixture.is_file():
            raise SystemExit(f"manifest references missing asset: {schema} / {fixture}")
        if contract.get("schemaContentHash") and contract["schemaContentHash"] != sha(schema):
            raise SystemExit(f"schema hash mismatch: {schema}")
        json.loads(schema.read_text())
        json.loads(fixture.read_text())
        referenced.update({contract["schemaFile"], contract.get("fixtureFile", contract.get("validFixtureFile", ""))})
        contracts.append({"kind": contract.get("kind"), "schemaId": contract.get("schemaId"), "schema": contract["schemaFile"], "fixture": contract.get("fixtureFile", contract.get("validFixtureFile"))})
    files = []
    for file_path in sorted(root.rglob("*.json")):
        relative = file_path.relative_to(root).as_posix()
        files.append({"path": relative, "sha256": sha(file_path), "bytes": file_path.stat().st_size})
    entries.append({"repository": repository, "family": family, "commit": commit(root if repository != "bids" else bids), "rootKind": root.name, "contracts": contracts, "files": files})

result = {
    "schemaVersion": 1,
    "gate": "P4-002",
    "status": "PASS",
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "sharedMirrors": {"commands": "PASS", "approvals": "PASS"},
    "ownerSpecificFamilies": entries,
    "notes": ["Commands and approvals are byte-identical mirrors; summaries are owner-specific and hash-checked against their manifests."]
}
output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"gate": "P4-002", "status": "PASS", "output": str(output), "families": len(entries)}, ensure_ascii=False))
PY
