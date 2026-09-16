#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

go test ./server/internal/observability ./server/internal/modules/binding ./server/internal/modules/records ./server/internal/modules/projection \
  -run 'Test(Rate|BindingHTTPHandlerRejectsOversizedBody|ViewServiceEnforcesSchemaFieldAllowlistAndCursorBounds|InboxClaimBoundsPayloadAndIdentifiers|OperationsServiceRejectsUnboundedQueryAndReaderErrors|PullRunnerRejectsUnboundedConfiguration|SummaryHandlerRejectsOversizedEnvelope)' -count=1

echo "M7-077 bounded query/payload/rate-limit gate passed"
