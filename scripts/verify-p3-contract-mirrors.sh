#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
record_hub_root="$(cd "$script_dir/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"

python3 - "$record_hub_root" "$approver_root" "$fluxion_root" "$bids_root" <<'PY'
import hashlib
import json
import pathlib
import sys

record_hub, approver, fluxion, bids = map(pathlib.Path, sys.argv[1:])
roots = [
    record_hub / "contracts/commands",
    approver / "approver-contract/src/main/resources/record-hub/commands",
    fluxion / "server/src/main/resources/record-hub/commands",
    bids / "backend/internal/recordhub/contracts/commands",
]
assets = [
    "command-envelope-v1.schema.json",
    "result-envelope-v1.schema.json",
    "error-codes.json",
    "manifest.json",
    "testdata/valid/command-envelope-v1.json",
    "testdata/valid/result-envelope-v1.json",
    "testdata/invalid/command-envelope-v1-unknown-field.json",
    "testdata/invalid/result-envelope-v1-status.json",
]

for root in roots:
    if not root.is_dir():
        raise SystemExit(f"missing command contract root: {root}")

for relative in assets:
    contents = []
    for root in roots:
        file_path = root / relative
        if not file_path.is_file():
            raise SystemExit(f"missing mirrored asset: {file_path}")
        contents.append(file_path.read_bytes())
    if any(content != contents[0] for content in contents[1:]):
        raise SystemExit(f"byte mismatch across command contract mirrors: {relative}")

manifest = json.loads((roots[0] / "manifest.json").read_text())
if manifest.get("version") != 1 or manifest.get("contractFamily") != "record-hub-command-result":
    raise SystemExit("unexpected command/result manifest metadata")
for contract in manifest.get("contracts", []):
    schema = (roots[0] / contract["schemaFile"]).read_bytes()
    actual = "sha256:" + hashlib.sha256(schema).hexdigest()
    if actual != contract["schemaContentHash"]:
        raise SystemExit(f"manifest schema hash mismatch: {contract['kind']}")
    json.loads(schema)
    json.loads((roots[0] / contract["validFixtureFile"]).read_text())
    for invalid_fixture in contract["invalidFixtureFiles"]:
        json.loads((roots[0] / invalid_fixture).read_text())
error_codes = roots[0] / manifest["errorCodesFile"]
actual_error_hash = "sha256:" + hashlib.sha256(error_codes.read_bytes()).hexdigest()
if actual_error_hash != manifest["errorCodesContentHash"]:
    raise SystemExit("manifest error-code hash mismatch")
catalog = json.loads(error_codes.read_text())
if catalog.get("version") != 1 or len(catalog.get("codes", [])) < 8:
    raise SystemExit("error-code catalog is incomplete")
print(f"P3 command/result contract mirrors verified ({len(assets)} assets, {len(roots)} repositories)")
PY
