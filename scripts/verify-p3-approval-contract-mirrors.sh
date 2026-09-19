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
    record_hub / "contracts/approvals",
    approver / "approver-contract/src/main/resources/record-hub/approvals",
    fluxion / "server/src/main/resources/record-hub/approvals",
    bids / "backend/internal/recordhub/contracts/approvals",
]
assets = [
    "dispatch-approval-request-v1.schema.json",
    "dispatch-approval-result-v1.schema.json",
    "error-codes.json",
    "manifest.json",
    "testdata/valid/dispatch-approval-request-v1.json",
    "testdata/valid/dispatch-approval-result-v1.json",
    "testdata/invalid/dispatch-approval-request-v1-unknown-field.json",
    "testdata/invalid/dispatch-approval-result-v1-decision.json",
]

for root in roots:
    if not root.is_dir():
        raise SystemExit(f"missing approval contract root: {root}")
    actual = {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file()}
    unknown = actual - set(assets)
    if unknown:
        raise SystemExit(f"unknown approval contract assets in {root}: {sorted(unknown)}")

for relative in assets:
    contents = [(root / relative).read_bytes() for root in roots]
    if any(content != contents[0] for content in contents[1:]):
        raise SystemExit(f"byte mismatch across approval contract mirrors: {relative}")

manifest = json.loads((roots[0] / "manifest.json").read_text())
if manifest.get("version") != 1 or manifest.get("contractFamily") != "record-hub-dispatch-approval":
    raise SystemExit("unexpected dispatch approval manifest metadata")
for contract in manifest.get("contracts", []):
    schema_path = roots[0] / contract["schemaFile"]
    schema = json.loads(schema_path.read_text())
    actual_hash = "sha256:" + hashlib.sha256(schema_path.read_bytes()).hexdigest()
    if actual_hash != contract["schemaContentHash"]:
        raise SystemExit(f"approval schema hash mismatch: {contract['kind']}")
    if schema.get("type") != "object" or schema.get("additionalProperties") is not False:
        raise SystemExit(f"approval schema must be a closed object: {contract['kind']}")
    json.loads((roots[0] / contract["validFixtureFile"]).read_text())
    for invalid in contract["invalidFixtureFiles"]:
        json.loads((roots[0] / invalid).read_text())
error_codes_path = roots[0] / manifest["errorCodesFile"]
error_codes = json.loads(error_codes_path.read_text())
if "APPROVAL_TRANSPORT_UNAVAILABLE" not in {item["code"] for item in error_codes.get("codes", [])}:
    raise SystemExit("approval error-code catalog is incomplete")
if "sha256:" + hashlib.sha256(error_codes_path.read_bytes()).hexdigest() != manifest["errorCodesContentHash"]:
    raise SystemExit("approval error-code hash mismatch")
print(f"P3 dispatch approval contract mirrors verified ({len(assets)} assets, {len(roots)} repositories)")
PY
