#!/usr/bin/env sh
set -eu

# Cross-repository deterministic gate for M7-071. The gate exercises the
# producer contract fixtures, outbox payload construction, workflow result
# projection, and Record Hub API redaction tests. It does not require live
# MongoDB, NATS, Temporal, Conductor, or Dex services.

record_hub_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
approver_root=${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}
fluxion_root=${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}
bids_root=${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}

for required in "$approver_root" "$fluxion_root/server" "$bids_root/backend"; do
  if [ ! -d "$required" ]; then
    echo "required repository path is missing: $required" >&2
    exit 1
  fi
done

echo "[record-hub] summary schemas, projection handlers, operations redaction"
cd "$record_hub_root"
go test ./contracts/summaries ./server/internal/modules/projection

echo "[approver] summary contract and Record Hub outbox"
cd "$approver_root"
mvn -q -pl approver-contract,approver-api -am \
  -Dtest=ApplicationSummaryContractTest,RecordHubSummaryOutboxDispatcherTest \
  -Dsurefire.failIfNoSpecifiedTests=false test

echo "[fluxion] summary contract, outbox and workflow response projection"
cd "$fluxion_root/server"
./gradlew test \
  --tests fluxion.recordhub.ProjectSummaryContractTest \
  --tests fluxion.recordhub.RecordHubOutboxTest \
  --tests fluxion.recordhub.BindingClientTest \
  --tests fluxion.workflow.ProjectDiagnosticWorkflowTest

echo "[bids] summary contract, outbox and worker response projection"
cd "$bids_root/backend"
go test ./internal/outbox ./internal/recordhub ./internal/workers

echo "M7-071 payload scan passed"
