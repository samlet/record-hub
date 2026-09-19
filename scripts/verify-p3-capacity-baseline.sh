#!/usr/bin/env bash
set -Eeuo pipefail

# P3-407: bounded native transport/projection baseline. It publishes safe
# synthetic summary envelopes (no business-table writes) and reports event
# rate, drain time, and projection latency percentiles.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P3_CAPACITY_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-407-evidence.XXXXXX")}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-407-runtime.XXXXXX")"
mkdir -p "$evidence_dir" "$work_dir/nats"
mongo_uri="${RECORD_HUB_P3_CAPACITY_MONGO_URI:-mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}"
mongo_database="record_hub_p3_capacity_$(date +%s)_$$"
if [[ "$mongo_uri" == *\?* ]]; then
  mongo_base="${mongo_uri%%\?*}"
  mongo_database_uri="${mongo_base%/}/${mongo_database}?${mongo_uri#*\?}"
else
  mongo_database_uri="${mongo_uri%/}/${mongo_database}"
fi
nats_port="${RECORD_HUB_P3_CAPACITY_NATS_PORT:-14224}"
nats_monitor_port="${RECORD_HUB_P3_CAPACITY_NATS_MONITOR_PORT:-18224}"
record_hub_port="${RECORD_HUB_P3_CAPACITY_RECORD_HUB_PORT:-18084}"
nats_url="nats://127.0.0.1:${nats_port}"
workspace="workspace-p3-capacity"
tenant="tenant-p3-capacity"
sample_size="${RECORD_HUB_P3_CAPACITY_EVENTS:-200}"
nats_pid=""
record_hub_pid=""

cleanup() {
  set +e
  [[ -n "$record_hub_pid" ]] && kill -TERM "$record_hub_pid" 2>/dev/null || true
  [[ -n "$nats_pid" ]] && kill -TERM "$nats_pid" 2>/dev/null || true
  [[ -n "$record_hub_pid" ]] && wait "$record_hub_pid" 2>/dev/null || true
  [[ -n "$nats_pid" ]] && wait "$nats_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

if [[ "${RECORD_HUB_P3_CAPACITY_LIVE:-0}" != "1" ]]; then
  echo "P3-407 capacity baseline: SKIPPED (set RECORD_HUB_P3_CAPACITY_LIVE=1)"
  exit 0
fi

for command in curl go jq mongosh nats nats-server python3 uuidgen; do
  command -v "$command" >/dev/null || {
    echo "P3-407 capacity baseline: SKIPPED (missing command: $command)"
    exit 0
  }
done
if ! mongosh --quiet "$mongo_uri" --eval 'quit(db.runCommand({ping:1}).ok ? 0 : 1)' >/dev/null 2>&1; then
  jq -n --arg mongo "$mongo_uri" '{gate:"P3-407",status:"SKIPPED",reason:"configured MongoDB replica set is not reachable",mongoUri:$mongo,retry:"Start a native MongoDB replica set and rerun RECORD_HUB_P3_CAPACITY_LIVE=1 ./scripts/verify-p3-capacity-baseline.sh"}' \
    | tee "$evidence_dir/capacity-baseline.json" "$evidence_dir/p3-407-capacity.json" >/dev/null
  echo "P3-407 capacity baseline: SKIPPED (MongoDB is not reachable)"
  exit 0
fi
[[ "$sample_size" =~ ^[1-9][0-9]*$ && "$sample_size" -le 10000 ]] || { echo "RECORD_HUB_P3_CAPACITY_EVENTS must be 1..10000" >&2; exit 2; }

go build -trimpath -o "$work_dir/record-hub" ./server/cmd/record-hub
nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$work_dir/nats" -n record-hub-p3-capacity >"$evidence_dir/nats.log" 2>&1 &
nats_pid="$!"
for _ in {1..160}; do
  curl --silent --show-error --fail "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" >/dev/null 2>&1 && break
  sleep 0.1
done
curl --silent --show-error --fail "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" >/dev/null
(cd "$root_dir" && RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init) >"$evidence_dir/nats-init.log" 2>&1

RECORD_HUB_MODE=all \
RECORD_HUB_HTTP_ADDRESS="127.0.0.1:${record_hub_port}" \
RECORD_HUB_SHUTDOWN_TIMEOUT=5s \
RECORD_HUB_MONGODB_URI="$mongo_uri" \
RECORD_HUB_MONGODB_DATABASE="$mongo_database" \
RECORD_HUB_NATS_URL="$nats_url" \
RECORD_HUB_PROJECTION_WORKSPACE_ID="$workspace" \
  "$work_dir/record-hub" serve >"$evidence_dir/record-hub.log" 2>&1 &
record_hub_pid="$!"
for _ in {1..200}; do
  curl --silent --show-error --fail "http://127.0.0.1:${record_hub_port}/readyz" >/dev/null 2>&1 && break
  sleep 0.1
done
curl --silent --show-error --fail "http://127.0.0.1:${record_hub_port}/readyz" >/dev/null

start_ms="$(python3 -c 'import time; print(time.time_ns()//1_000_000)')"
for index in $(seq 1 "$sample_size"); do
  event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
  aggregate_id="capacity-${index}-$$"
  now="$(python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"))')"
  payload="$(jq -cn --arg id "$aggregate_id" --arg now "$now" '{applicationId:$id,title:"capacity baseline",status:"OPEN",processRef:"p3-407",updatedAt:$now,version:1}')"
  envelope="$(jq -cn --arg eventId "$event_id" --arg tenantId "$tenant" --arg aggregateId "$aggregate_id" --arg occurredAt "$now" --arg workspaceId "$workspace" --argjson payload "$payload" '{eventId:$eventId,kind:"event",eventType:"approver.application.summary-changed",schemaVersion:1,sourceSystem:"approver",tenantId:$tenantId,aggregateType:"Application",aggregateId:$aggregateId,aggregateVersion:1,occurredAt:$occurredAt,payload:$payload,metadata:{schemaId:"urn:record-hub:summary:application:v1",workspaceId:$workspaceId}}')"
  nats --server "$nats_url" pub -H "Nats-Msg-Id: $event_id" events.approver.application.summary-changed.v1 "$envelope" >/dev/null
done
publish_end_ms="$(python3 -c 'import time; print(time.time_ns()//1_000_000)')"

count=0
for _ in {1..400}; do
  count="$(mongosh --quiet "$mongo_database_uri" --eval "print(db.records.countDocuments({tenantId:'$tenant',workspaceId:'$workspace','source.system':'approver'}))" 2>/dev/null || echo 0)"
  [[ "$count" == "$sample_size" ]] && break
  sleep 0.05
done
drain_end_ms="$(python3 -c 'import time; print(time.time_ns()//1_000_000)')"
[[ "$count" == "$sample_size" ]] || { echo "capacity projection did not drain: $count/$sample_size" >&2; exit 1; }

mongosh --quiet "$mongo_database_uri" --eval "print(JSON.stringify(db.records.find({tenantId:'$tenant',workspaceId:'$workspace','source.system':'approver'},{createdAt:1,'projection.syncedAt':1,_id:0}).toArray()))" >"$evidence_dir/latency-samples.json"
python3 - "$evidence_dir/latency-samples.json" "$start_ms" "$publish_end_ms" "$drain_end_ms" "$sample_size" <<'PY' >"$evidence_dir/capacity-metrics.json"
import json, sys
from datetime import datetime
samples = json.load(open(sys.argv[1]))
start, publish_end, drain_end, total = map(int, sys.argv[2:6])
def ms(value):
    if isinstance(value, dict) and "$date" in value:
        value = value["$date"]
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp() * 1000
latencies = sorted(max(0.0, ms(row["projection"]["syncedAt"]) - ms(row["createdAt"])) for row in samples if row.get("projection") and row.get("createdAt"))
def percentile(values, fraction):
    if not values:
        return None
    index = min(len(values) - 1, max(0, int((len(values) - 1) * fraction)))
    return round(values[index], 3)
publish_ms = max(1, publish_end - start)
drain_ms = max(1, drain_end - start)
print(json.dumps({"gate":"P3-407","status":"PASS","scope":"synthetic-summary-transport-projection","events":total,"publishSeconds":round(publish_ms/1000,3),"drainSeconds":round(drain_ms/1000,3),"publishEventsPerSecond":round(total/(publish_ms/1000),3),"drainEventsPerSecond":round(total/(drain_ms/1000),3),"projectionLatencyMs":{"samples":len(latencies),"p50":percentile(latencies,.50),"p95":percentile(latencies,.95),"p99":percentile(latencies,.99),"max":round(max(latencies),3) if latencies else None},"fourOwnerMixedWorkload":"SKIPPED"}, sort_keys=True))
PY
cp "$evidence_dir/capacity-metrics.json" "$evidence_dir/capacity-baseline.json"
cp "$evidence_dir/capacity-metrics.json" "$evidence_dir/p3-407-capacity.json"
cat "$evidence_dir/capacity-metrics.json"
