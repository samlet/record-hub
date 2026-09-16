#!/usr/bin/env bash
set -euo pipefail

# M7-076 native restart gate. It starts only Record Hub against already-running
# native MongoDB/NATS services, proves an event is applied, stops the process,
# publishes the next version twice while it is down, and verifies recovery plus
# Inbox idempotency after restart. Dependency daemons are never stopped.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$record_hub_root"

for command in curl jq mongosh nats uuidgen; do
  command -v "$command" >/dev/null || { echo "$command is required for M7 native restart smoke" >&2; exit 2; }
done

mongo_uri="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true}"
nats_url="${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}"
database="${RECORD_HUB_MONGODB_DATABASE:-record_hub}"
workspace="${RECORD_HUB_PROJECTION_WORKSPACE_ID:-workspace-m7-restart}"
address="${RECORD_HUB_HTTP_ADDRESS:-127.0.0.1:18088}"
tenant="record-hub-m7-restart-$(date +%s)-$$"
aggregate_id="application-m7-restart-$$"
subject="events.approver.application.summary-changed.v1"
event_type="approver.application.summary-changed"
log_dir="${TMPDIR:-/tmp}/record-hub-m7-restart-$$"
mkdir -p "$log_dir"

mongosh --quiet "$mongo_uri" --eval "db.getSiblingDB('$database').runCommand({ping:1})" >/dev/null
nats --server "$nats_url" stream info DOMAIN_EVENTS >/dev/null
go build -trimpath -o build/record-hub ./server/cmd/record-hub

server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$log_dir"
}
trap cleanup EXIT HUP INT TERM

start_server() {
  local label="$1"
  RECORD_HUB_MODE=all \
    RECORD_HUB_HTTP_ADDRESS="$address" \
    RECORD_HUB_SHUTDOWN_TIMEOUT=5s \
    RECORD_HUB_MONGODB_URI="$mongo_uri" \
    RECORD_HUB_MONGODB_DATABASE="$database" \
    RECORD_HUB_NATS_URL="$nats_url" \
    RECORD_HUB_PROJECTION_WORKSPACE_ID="$workspace" \
    build/record-hub serve >"$log_dir/$label.log" 2>&1 &
  server_pid=$!
  for _ in {1..160}; do
    if curl --silent --show-error --fail "http://$address/readyz" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      echo "Record Hub $label process exited early (log: $log_dir/$label.log)" >&2
      return 1
    fi
    sleep 0.1
  done
  echo "Record Hub $label process did not become ready (log: $log_dir/$label.log)" >&2
  return 1
}

stop_server() {
  local pid="$server_pid"
  server_pid=""
  kill -TERM "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

publish_version() {
  local event_id="$1" aggregate_version="$2" payload envelope now
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload="$(jq -cn \
    --arg id "$aggregate_id" \
    --arg updatedAt "$now" \
    --argjson version "$aggregate_version" \
    '{applicationId:$id,title:"M7 restart recovery",status:(if $version == 1 then "OPEN" else "APPROVED" end),processRef:"workflow-m7-restart",updatedAt:$updatedAt,version:$version}')"
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

event_one="$(uuidgen | tr '[:upper:]' '[:lower:]')"
event_two="$(uuidgen | tr '[:upper:]' '[:lower:]')"
start_server first
publish_version "$event_one" 1
wait_for_version 1
echo "M7 restart pre-stop projection: PASS"

stop_server
# The durable consumer remains server-side. Publish while Record Hub is down;
# the same event is intentionally sent twice to exercise Inbox deduplication.
publish_version "$event_two" 2
publish_version "$event_two" 2

start_server second
if ! wait_for_version 2; then
  echo "version 2 was not projected after restart (logs: $log_dir)" >&2
  exit 1
fi

mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const r=d.records.findOne({tenantId:'$tenant',workspaceId:'$workspace','source.system':'approver','source.type':'application','source.id':'$aggregate_id',recordVersion:2,'projection.status':'CURRENT'}); const c=d.projection_checkpoints.findOne({tenantId:'$tenant',workspaceId:'$workspace',sourceSystem:'approver',aggregateType:'Application',aggregateId:'$aggregate_id',sourceVersion:2,status:'CURRENT'}); const inbox=d.inbox_events.find({tenantId:'$tenant',workspaceId:'$workspace',eventId:{\$in:['$event_one','$event_two']},consumer:'record-hub-approver-projection-v1',status:'APPLIED'}).toArray(); const audits=d.audit_entries.countDocuments({tenantId:'$tenant',workspaceId:'$workspace',action:'projection.apply'}); if(!r || !c || inbox.length!==2 || audits!==2) quit(1); print('M7 restart recovery, checkpoint and Inbox dedup: PASS');"
stop_server
echo "M7-076 native Record Hub restart smoke: PASS"
