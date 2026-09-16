#!/usr/bin/env bash
set -euo pipefail

# M8-083 is the deterministic cross-repository happy-path gate. It runs the
# same bounded contracts twice for Record Hub to catch accidental stateful
# test behavior, then exercises each producer's summary/outbox and binding
# tests. It intentionally does not call a local fake endpoint as live E2E.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"

for required in "$approver_root" "$fluxion_root/server" "$bids_root/backend"; do
  if [[ ! -d "$required" ]]; then
    echo "required repository path is missing: $required" >&2
    exit 1
  fi
done

echo "[record-hub] Web session/resource/operations and domain happy path (run 1)"
(cd "$record_hub_root" && go test ./server/internal/web ./server/internal/modules/identity ./server/internal/modules/schema ./server/internal/modules/records ./server/internal/modules/projection -count=1)
echo "[record-hub] Web session/resource/operations and domain happy path (run 2)"
(cd "$record_hub_root" && go test ./server/internal/web ./server/internal/modules/identity ./server/internal/modules/schema ./server/internal/modules/records ./server/internal/modules/projection -count=1)

echo "[approver] summary contract/outbox"
(cd "$approver_root" && mvn -q -pl approver-contract,approver-api -am \
  -Dtest=ApplicationSummaryContractTest,RecordHubSummaryOutboxDispatcherTest \
  -Dsurefire.failIfNoSpecifiedTests=false test)

echo "[fluxion] summary contract/outbox and diagnostic workflow"
(cd "$fluxion_root/server" && ./gradlew -q test \
  --tests fluxion.recordhub.ProjectSummaryContractTest \
  --tests fluxion.recordhub.RecordHubOutboxTest \
  --tests fluxion.recordhub.BindingClientTest \
  --tests fluxion.workflow.ProjectDiagnosticWorkflowTest)

echo "[bids] summary/outbox and diagnostic worker"
(cd "$bids_root/backend" && go test ./internal/recordhub ./internal/outbox ./internal/conductor ./internal/workers)

if [[ "${RECORD_HUB_M8_LIVE:-0}" == "1" ]]; then
  echo "M8-083 live browser/API E2E: NOT AUTOMATED" >&2
  echo "Use the M8-085 runbook with Dex, MongoDB, NATS, a Web BFF and all three workflows; this gate will not label a fake endpoint as live." >&2
  exit 2
fi

echo "M8-083 deterministic happy-path gate passed"
echo "M8-083 live browser/API E2E: SKIPPED (set RECORD_HUB_M8_LIVE=1 only with the supervised M8-085 topology)"
