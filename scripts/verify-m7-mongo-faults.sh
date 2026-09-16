#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

if [ "${RECORD_HUB_M7_MONGO_LIVE:-0}" != "1" ]; then
  go test ./server/internal/modules/projection -run 'TestMongoProjectionApplyAckLossReplayIsDuplicateFree' -count=1
  echo "M7-073 live Mongo response-loss fault injection: SKIPPED (set RECORD_HUB_M7_MONGO_LIVE=1 with RECORD_HUB_MONGODB_URI)"
  exit 0
fi

: "${RECORD_HUB_MONGODB_URI:?RECORD_HUB_MONGODB_URI is required when RECORD_HUB_M7_MONGO_LIVE=1}"
go test ./server/internal/modules/projection -run 'TestMongoProjectionApplyAckLossReplayIsDuplicateFree' -count=1
echo "M7-073 live Mongo response-loss fault injection: PASS"
