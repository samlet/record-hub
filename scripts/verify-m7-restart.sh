#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
binary="$work_dir/record-hub"

cd "$repository_root"
go test ./server/internal/app ./server/internal/httpapi ./server/internal/modules/projection -run 'Test(Run|PullRunner|Client)' -count=1
go build -trimpath -o "$binary" ./server/cmd/record-hub

run_api_restart() {
  address="127.0.0.1:$1"
  RECORD_HUB_MODE=api RECORD_HUB_HTTP_ADDRESS="$address" RECORD_HUB_SHUTDOWN_TIMEOUT=2s "$binary" serve >"$work_dir/api-$1.log" 2>&1 &
  pid=$!
  ready=0
  i=0
  while [ "$i" -lt 50 ]; do
    if curl --silent --show-error --fail "http://$address/healthz" >/dev/null 2>&1; then
      ready=1
      break
    fi
    i=$((i + 1))
    sleep 0.05
  done
  if [ "$ready" -ne 1 ]; then
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    echo "API process did not become healthy" >&2
    return 1
  fi
  kill -TERM "$pid"
  wait "$pid"
}

run_worker_restart() {
  RECORD_HUB_MODE=worker RECORD_HUB_SHUTDOWN_TIMEOUT=2s "$binary" serve >"$work_dir/worker-$1.log" 2>&1 &
  pid=$!
  sleep 0.15
  kill -TERM "$pid"
  wait "$pid"
}

run_api_restart 18081
run_api_restart 18082
run_worker_restart first
run_worker_restart second

if [ "${RECORD_HUB_M7_RESTART_LIVE:-0}" = "1" ]; then
  echo "M7-076 live Mongo/NATS restart scenario: NOT AUTOMATED; run the local runbook with dependencies under supervision" >&2
else
  echo "M7-076 live Mongo/NATS restart scenario: SKIPPED (set RECORD_HUB_M7_RESTART_LIVE=1 for explicit operator runbook reminder)"
fi

echo "M7-076 API/worker process restart smoke passed"
