#!/usr/bin/env bash
set -euo pipefail

# M8-084 cross-repository negative-path gate. Every check is deterministic and
# bounded; the optional live flag deliberately exits non-zero until the
# supervised Mongo/NATS/Dex/Temporal/Conductor topology is available.
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

echo "[record-hub] bad state/CSRF, forged session, nonce and scope failures"
(cd "$record_hub_root" && go test ./server/internal/web -run 'Test(OIDCRejectsInvalidState|CSRFProtectsLogout|SessionMiddlewareDoesNotTrustMalformedCookie|ResourceRouterReceivesSessionPrincipalAndCSRFBoundary)' -count=1)
(cd "$record_hub_root" && go test ./server/internal/modules/identity -run 'Test(OIDCVerifierRejectsInvalidClaims|OIDCVerifierNonceBinding|AuthorizerDeniesBoundaryFailures)' -count=1)

echo "[record-hub] projection unknown consumer/scope, gap and DLQ failures"
(cd "$record_hub_root" && go test ./server/internal/modules/projection -run 'Test(OperationsServiceRejectsUnknownConsumerAndReaderScopeMismatch|OperationsServiceRejectsUnboundedQueryAndReaderErrors|PullRunner.*|SummaryHandlersReject.*)' -count=1)

echo "[approver] summary payload and Outbox ACK-loss retry"
(cd "$approver_root" && mvn -q -pl approver-contract,approver-api -am \
  -Dtest=ApplicationSummaryContractTest,RecordHubSummaryOutboxDispatcherTest \
  -Dsurefire.failIfNoSpecifiedTests=false test)

echo "[fluxion] workflow retry/failure and Outbox ACK-loss"
(cd "$fluxion_root/server" && ./gradlew -q test \
  --tests fluxion.recordhub.RecordHubOutboxTest \
  --tests fluxion.recordhub.BindingClientTest \
  --tests fluxion.workflow.ProjectDiagnosticWorkflowTest)

echo "[bids] diagnostic retry/failure and Outbox ACK-loss"
(cd "$bids_root/backend" && go test ./internal/recordhub ./internal/outbox ./internal/conductor ./internal/workers)

if [[ "${RECORD_HUB_M8_LIVE:-0}" == "1" ]]; then
  echo "M8-084 live outage/bad-token/failure E2E: NOT AUTOMATED" >&2
  echo "Run the supervised M8-085/M8-086 topology; this gate never treats an unavailable dependency as a pass." >&2
  exit 2
fi

echo "M8-084 deterministic failure-path gate passed"
echo "M8-084 live outage/bad-token/failure E2E: SKIPPED (set RECORD_HUB_M8_LIVE=1 only with the supervised topology)"
