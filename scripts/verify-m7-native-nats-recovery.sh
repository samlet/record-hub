#!/usr/bin/env bash
set -euo pipefail

# M7-074 native transport recovery gate. A temporary local NATS JetStream
# server is used so the existing native dependency on :4222 is never stopped.
# Record Hub remains alive while NATS is unavailable, then reconnects to the
# same JetStream store and applies a post-outage event exactly once.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$record_hub_root"

for command in curl jq mongosh nats nats-server uuidgen; do
  command -v "$command" >/dev/null || { echo "$command is required for M7 native NATS recovery smoke" >&2; exit 2; }
done

mongo_uri="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true}"
database="${RECORD_HUB_MONGODB_DATABASE:-record_hub}"
workspace="${RECORD_HUB_PROJECTION_WORKSPACE_ID:-workspace-m7-nats-recovery}"
address="${RECORD_HUB_HTTP_ADDRESS:-127.0.0.1:18089}"
nats_port="${RECORD_HUB_M7_NATS_RECOVERY_PORT:-14222}"
nats_url="nats://127.0.0.1:$nats_port"
tenant="record-hub-m7-nats-$(date +%s)-$$"
aggregate_id="application-m7-nats-$$"
subject="events.approver.application.summary-changed.v1"
event_type="approver.application.summary-changed"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-m7-nats-XXXXXX")"
store_dir="$work_dir/jetstream"
mkdir -p "$store_dir"

server_pid=""
nats_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ -n "$nats_pid" ]]; then
    kill -TERM "$nats_pid" 2>/dev/null || true
    wait "$nats_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

start_nats() {
  local label="$1"
  nats-server -a 127.0.0.1 -p "$nats_port" -js -sd "$store_dir" >"$work_dir/nats-$label.log" 2>&1 &
  nats_pid=$!
  for _ in {1..100}; do
    if nats --server "$nats_url" pub "_record_hub_m7_probe.$label" ping >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$nats_pid" 2>/dev/null; then
      echo "temporary NATS $label process exited early (log: $work_dir/nats-$label.log)" >&2
      return 1
    fi
    sleep 0.1
  done
  echo "temporary NATS $label process did not become reachable" >&2
  return 1
}

stop_nats() {
  local pid="$nats_pid"
  nats_pid=""
  kill -TERM "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

start_record_hub() {
  RECORD_HUB_MODE=all \
    RECORD_HUB_HTTP_ADDRESS="$address" \
    RECORD_HUB_SHUTDOWN_TIMEOUT=5s \
    RECORD_HUB_MONGODB_URI="$mongo_uri" \
    RECORD_HUB_MONGODB_DATABASE="$database" \
    RECORD_HUB_NATS_URL="$nats_url" \
    RECORD_HUB_PROJECTION_WORKSPACE_ID="$workspace" \
    build/record-hub serve >"$work_dir/record-hub.log" 2>&1 &
  server_pid=$!
  for _ in {1..160}; do
    if curl --silent --show-error --fail "http://$address/readyz" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      echo "Record Hub process exited early (log: $work_dir/record-hub.log)" >&2
      return 1
    fi
    sleep 0.1
  done
  echo "Record Hub did not become ready (log: $work_dir/record-hub.log)" >&2
  return 1
}

publish_version() {
  local event_id="$1" aggregate_version="$2" payload envelope now
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload="$(jq -cn \
    --arg id "$aggregate_id" \
    --arg updatedAt "$now" \
    --argjson version "$aggregate_version" \
    '{applicationId:$id,title:"M7 NATS recovery",status:(if $version == 1 then "OPEN" else "APPROVED" end),processRef:"workflow-m7-nats",updatedAt:$updatedAt,version:$version}')"
  envelope="$(jq -cn \
    --arg eventId "$event_id" \
    --arg eventType "$event_type" \
    --arg tenantId "$tenant" \
    --arg aggregateId "$aggregate_id" \
    --arg occurredAt "$now" \
    --arg workspaceId "$workspace" \
    --argjson payload "$payload" \
    '{eventId:$eventId,kind:"event",eventType:$eventType,schemaVersion:1,sourceSystem:"approver",tenantId:$tenantId,aggregateType:"Application",aggregateId:$aggregateId,aggregateVersion:'"$aggregate_version"',occurredAt:$occurredAt,payload:$payload,metadata:{schemaId:"urn:record-hub:summary:application:v1",workspaceId:$workspaceId}}')"
  nats --server "$nats_url" pub "$subject" "$envelope" >/dev/null
}

wait_for_version() {
  local expected="$1"
  for _ in {1..160}; do
    if mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const r=d.records.findOne({tenantId:'$tenant',workspaceId:'$workspace','source.system':'approver','source.type':'application','source.id':'$aggregate_id',recordVersion:$expected,'projection.status':'CURRENT'}); if(r) quit(0); quit(1);" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

go build -trimpath -o build/record-hub ./server/cmd/record-hub
mongosh --quiet "$mongo_uri" --eval "db.getSiblingDB('$database').runCommand({ping:1})" >/dev/null

start_nats first
RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init >/dev/null
start_record_hub
event_one="$(uuidgen | tr '[:upper:]' '[:lower:]')"
event_two="$(uuidgen | tr '[:upper:]' '[:lower:]')"
publish_version "$event_one" 1
wait_for_version 1
echo "M7 NATS pre-outage projection: PASS"

stop_nats
if ! kill -0 "$server_pid" 2>/dev/null; then
  echo "Record Hub exited when NATS became unavailable" >&2
  exit 1
fi
health_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "http://$address/healthz" || true)"
ready_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "http://$address/readyz" || true)"
[[ "$health_status" == "200" ]] || { echo "healthz during NATS outage returned $health_status" >&2; exit 1; }
[[ "$ready_status" == "503" ]] || { echo "readyz during NATS outage returned $ready_status" >&2; exit 1; }
echo "M7 NATS outage liveness/readiness behavior: PASS"

start_nats recovery
publish_version "$event_two" 2
publish_version "$event_two" 2
for _ in {1..160}; do
  if curl --silent --show-error --fail "http://$address/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl --silent --show-error --fail "http://$address/readyz" >/dev/null
wait_for_version 2

mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const r=d.records.findOne({tenantId:'$tenant',workspaceId:'$workspace','source.system':'approver','source.type':'application','source.id':'$aggregate_id',recordVersion:2,'projection.status':'CURRENT'}); const c=d.projection_checkpoints.findOne({tenantId:'$tenant',workspaceId:'$workspace',sourceSystem:'approver',aggregateType:'Application',aggregateId:'$aggregate_id',sourceVersion:2,status:'CURRENT'}); const inbox=d.inbox_events.find({tenantId:'$tenant',workspaceId:'$workspace',eventId:{\$in:['$event_one','$event_two']},consumer:'record-hub-approver-projection-v1',status:'APPLIED'}).toArray(); const audits=d.audit_entries.countDocuments({tenantId:'$tenant',workspaceId:'$workspace',action:'projection.apply'}); if(!r || !c || inbox.length!==2 || audits!==2) quit(1); print('M7 NATS recovery, checkpoint and Inbox dedup: PASS');"
echo "M7-074 native NATS outage/recovery smoke: PASS"
