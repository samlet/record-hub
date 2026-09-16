#!/usr/bin/env bash
set -euo pipefail

# M5-059 live runtime gate. It starts one Record Hub all-mode process against
# already-running native Mongo/NATS services, publishes one valid v1 summary
# per producer subject, and verifies the durable projection writes scoped
# records/checkpoints. The dependency daemons are never stopped or mutated.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$record_hub_root"

for command in curl jq mongosh nats uuidgen; do
  command -v "$command" >/dev/null || { echo "$command is required for M5 runtime smoke" >&2; exit 2; }
done

mongo_uri="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true}"
nats_url="${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}"
database="${RECORD_HUB_MONGODB_DATABASE:-record_hub}"
workspace="${RECORD_HUB_PROJECTION_WORKSPACE_ID:-workspace-m5-live}"
address="${RECORD_HUB_HTTP_ADDRESS:-127.0.0.1:18087}"
tenant="record-hub-m5-$(date +%s)-$$"

# nats-init is intentionally not called here: it publishes a smoke message
# and would add unrelated backlog to the durable consumers under test.
nats --server "$nats_url" stream info DOMAIN_EVENTS >/dev/null
go build -trimpath -o build/record-hub ./server/cmd/record-hub

export RECORD_HUB_MODE=all
export RECORD_HUB_HTTP_ADDRESS="$address"
export RECORD_HUB_SHUTDOWN_TIMEOUT=5s
export RECORD_HUB_MONGODB_URI="$mongo_uri"
export RECORD_HUB_MONGODB_DATABASE="$database"
export RECORD_HUB_NATS_URL="$nats_url"
export RECORD_HUB_PROJECTION_WORKSPACE_ID="$workspace"

log_file="${TMPDIR:-/tmp}/record-hub-m5-runtime.log"
build/record-hub serve >"$log_file" 2>&1 &
server_pid=$!
cleanup() {
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

for _ in {1..160}; do
  if curl --silent --show-error --fail "http://$address/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl --silent --show-error --fail "http://$address/readyz" >/dev/null

publish_event() {
  local source="$1" event_type="$2" aggregate_type="$3" aggregate_id="$4" payload="$5" event_id now envelope subject
  event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  subject="events.$source.$(echo "$event_type" | cut -d. -f2-).v1"
  envelope="$(jq -cn --arg eventId "$event_id" --arg eventType "$event_type" --arg sourceSystem "$source" --arg tenantId "$tenant" --arg aggregateType "$aggregate_type" --arg aggregateId "$aggregate_id" --arg occurredAt "$now" --arg workspaceId "$workspace" --argjson payload "$payload" '{eventId:$eventId,kind:"event",eventType:$eventType,schemaVersion:1,sourceSystem:$sourceSystem,tenantId:$tenantId,aggregateType:$aggregateType,aggregateId:$aggregateId,aggregateVersion:1,occurredAt:$occurredAt,payload:$payload,metadata:{schemaId:("urn:record-hub:summary:" + (if $sourceSystem == "approver" then "application" elif $sourceSystem == "fluxion" then "project" else "tender" end) + ":v1"),workspaceId:$workspaceId}}')"
  nats --server "$nats_url" pub "$subject" "$envelope" >/dev/null
  printf '%s\n' "$event_id"
}

approver_payload='{"applicationId":"app-m5-live","title":"M5 runtime review","status":"OPEN","processRef":"workflow-m5-live","updatedAt":"2026-09-15T15:30:00Z","version":1}'
fluxion_payload='{"projectId":"project-m5-live","type":"engineering","status":"ACTIVE","currentStage":"IMPLEMENTATION","workflowRef":"workflow-m5-live","updatedAt":"2026-09-15T15:30:00Z","version":1}'
bids_payload='{"tenderId":"tender-m5-live","buyerOrganization":"M5 Buyer","name":"M5 runtime tender","status":"OPEN","template":"standard-v1","updatedAt":"2026-09-15T15:30:00Z","version":1}'
approver_id="$(publish_event approver approver.application.summary-changed Application app-m5-live "$approver_payload")"
fluxion_id="$(publish_event fluxion fluxion.project.summary-changed Project project-m5-live "$fluxion_payload")"
bids_id="$(publish_event bids bids.tender.summary-changed Tender tender-m5-live "$bids_payload")"

for _ in {1..160}; do
  count="$(mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); print(d.records.countDocuments({tenantId:'$tenant',workspaceId:'$workspace'}))" | tr -d '[:space:]')"
  [[ "$count" == "3" ]] && break
  sleep 0.25
done
[[ "$count" == "3" ]] || { echo "expected 3 projected records; got $count (log: $log_file)" >&2; exit 1; }

mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const ids=['$approver_id','$fluxion_id','$bids_id']; const rows=d.records.find({tenantId:'$tenant',workspaceId:'$workspace'}).toArray(); if(rows.length!==3 || rows.some(r=>r.projection==null || r.projection.status!=='CURRENT')) quit(1); print('M5 runtime records/checkpoints: PASS');"
echo "M5-059 live Record Hub API/worker/Mongo/NATS projection smoke: PASS"
