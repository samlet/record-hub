#!/usr/bin/env sh
set -eu

# Deterministic cross-repository gate for M7-074. Producer transactions are
# exercised through their existing outbox tests; the Record Hub consumer is
# exercised through reconnect and NAK/ACK recovery tests. No local process is
# stopped by this script.

record_hub_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
approver_root=${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}
fluxion_root=${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}
bids_root=${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}

echo "[record-hub] pull consumer reconnect, NAK and ACK lifecycle"
cd "$record_hub_root"
go test ./server/internal/modules/projection -run 'TestPullRunner(NaksHandlerFailuresAndReconnects|DoubleAcksSuccessfulMessages|PublishesDeterministicFailureToDLQ|DeadLettersAfterMaxDeliver)' -count=1

echo "[approver] summary outbox ACK-loss retry"
cd "$approver_root"
mvn -q -pl approver-api -am -Dtest=RecordHubSummaryOutboxDispatcherTest -Dsurefire.failIfNoSpecifiedTests=false test

echo "[fluxion] summary outbox ACK-loss retry"
cd "$fluxion_root/server"
./gradlew -q test --tests fluxion.recordhub.RecordHubOutboxTest

echo "[bids] summary outbox ACK-loss retry and lease recovery"
cd "$bids_root/backend"
go test ./internal/outbox -run 'Test(TenderSummaryDispatcherPublishesAndMarksSuccess|TenderSummaryDispatcherAckLossLeavesRetryable|EnqueueTenderSummaryChangedUsesSafePayloadAndStableVersion)' -count=1

if [ "${RECORD_HUB_M7_NATS_LIVE:-0}" = "1" ]; then
  cd "$record_hub_root"
  RECORD_HUB_NATS_URL="${RECORD_HUB_NATS_URL:-nats://localhost:4222}" make nats-smoke
  echo "M7-074 NATS live connectivity smoke: PASS"
else
  echo "M7-074 live outage/recovery scenario: SKIPPED (set RECORD_HUB_M7_NATS_LIVE=1 for NATS smoke)"
fi

echo "M7-074 deterministic NATS/outbox recovery gate passed"
