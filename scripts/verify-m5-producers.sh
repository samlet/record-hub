#!/usr/bin/env bash
set -euo pipefail

record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"

python3 - "$record_hub_root" "$approver_root" "$fluxion_root" "$bids_root" <<'PY'
import hashlib
import json
import pathlib
import sys

record_hub, approver, fluxion, bids = map(pathlib.Path, sys.argv[1:])
manifest = json.loads((record_hub / "contracts/summaries/manifest.json").read_text())
checks = {
    "application": approver / "approver-contract/src/main/resources/record-hub/summaries/application-summary-v1.schema.json",
    "project": fluxion / "server/src/main/resources/record-hub/summaries/project-summary-v1.schema.json",
    "tender": bids / "backend/internal/recordhub/contracts/tender-summary-v1.schema.json",
}
for contract in manifest["contracts"]:
    kind = contract["kind"]
    schema = checks[kind]
    if schema.read_bytes() != (record_hub / "contracts/summaries" / contract["schemaFile"]).read_bytes():
        raise SystemExit(f"{kind}: schema bytes differ from Record Hub")
    actual = hashlib.sha256(schema.read_bytes()).hexdigest()
    expected = contract["schemaContentHash"].removeprefix("sha256:")
    if actual != expected:
        raise SystemExit(f"{kind}: schema hash {actual} != {expected}")
    fixture = record_hub / "contracts/summaries" / contract["fixtureFile"]
    mirror_fixture = schema.parent / contract["fixtureFile"]
    if json.loads(fixture.read_text()) != json.loads(mirror_fixture.read_text()):
        raise SystemExit(f"{kind}: fixture differs from Record Hub")
    for forbidden in ("contactEmail", "phone", "bidAmount", "quotation", "fileUrl", "documentUrl"):
        if forbidden in fixture.read_text():
            raise SystemExit(f"{kind}: forbidden field {forbidden}")
print("M5 contract mirrors: PASS")
PY

(cd "$record_hub_root" && go test ./contracts/summaries ./server/internal/modules/projection -run 'TestM5SummaryProducerEnvelopes|TestSummaryContractAssetsAreAvailableAndSafe' -count=1)
(cd "$approver_root" && mvn -q -pl approver-contract test)
(cd "$fluxion_root/server" && ./gradlew -q test --tests fluxion.recordhub.ProjectSummaryContractTest)
(cd "$bids_root/backend" && GOPROXY="${GOPROXY:-https://proxy.golang.org}" go test ./internal/outbox ./internal/recordhub)

if [[ "${RECORD_HUB_M5_LIVE:-0}" == "1" ]]; then
  (cd "$record_hub_root" && RECORD_HUB_NATS_URL="${RECORD_HUB_NATS_URL:-nats://localhost:4222}" make nats-smoke)
  (cd "$record_hub_root" && make mongo-smoke)
else
  echo "M5 live Mongo/JetStream smoke: SKIPPED (set RECORD_HUB_M5_LIVE=1)"
fi

echo "M5 producer acceptance: PASS"
