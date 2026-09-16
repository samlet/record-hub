#!/usr/bin/env bash
set -Eeuo pipefail

# M5-059 supervised live gate.  This is opt-in because it starts the three
# producer runtimes and creates temporary PostgreSQL/SQLite databases.  The
# native MongoDB, NATS JetStream and Conductor daemons are never stopped or
# mutated beyond their normal stream/definition APIs.
if [[ "${RECORD_HUB_M5_SUPERVISED_LIVE:-0}" != "1" ]]; then
  echo "M5-059 supervised producer -> relay -> projection gate: SKIPPED (set RECORD_HUB_M5_SUPERVISED_LIVE=1)"
  exit 0
fi

record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
mongo_uri="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true}"
nats_url="${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}"
database="${RECORD_HUB_MONGODB_DATABASE:-record_hub}"
workspace="${RECORD_HUB_M5_SUPERVISED_WORKSPACE:-workspace-m5-supervised}"
record_hub_address="${RECORD_HUB_M5_SUPERVISED_RECORD_HUB_ADDRESS:-127.0.0.1:18088}"
approver_port="${RECORD_HUB_M5_SUPERVISED_APPROVER_PORT:-18091}"
fluxion_port="${RECORD_HUB_M5_SUPERVISED_FLUXION_PORT:-18092}"
pg_user="${RECORD_HUB_M5_PG_USER:-$(id -un)}"
run_id="$(date +%s)-$$"
approver_db="record_hub_m5_approver_${run_id}"
fluxion_db="record_hub_m5_fluxion_${run_id}"
tenant="record-hub-m5-supervised-${run_id}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-m5-supervised.XXXXXX")"

record_hub_pid=""
approver_pid=""
fluxion_pid=""
bids_pid=""
created_databases=()

for command in curl jq mongosh nats uuidgen psql createdb dropdb sqlite3 mvn java go lsof; do
  command -v "$command" >/dev/null || {
    echo "$command is required for M5 supervised live gate" >&2
    exit 2
  }
done
[[ -d "$approver_root" && -d "$fluxion_root" && -d "$bids_root" ]] || {
  echo "producer repository path is missing" >&2
  exit 2
}

port_free() {
  local address="$1" port="${address##*:}"
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "TCP port $port is already in use; choose another M5 supervised port" >&2
    exit 2
  fi
}

cleanup() {
  set +e
  for pid in "$bids_pid" "$fluxion_pid" "$approver_pid" "$record_hub_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
      for _ in {1..30}; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
      done
      kill -KILL "$pid" 2>/dev/null || true
    fi
  done
  if ((${#created_databases[@]})); then
    for db_name in "${created_databases[@]}"; do
      dropdb --if-exists -U "$pg_user" "$db_name" >/dev/null 2>&1 || true
    done
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

port_free "$record_hub_address"
port_free "127.0.0.1:$approver_port"
port_free "127.0.0.1:$fluxion_port"

echo "M5 supervised live gate: building producer runtimes"
(cd "$approver_root" && mvn -q -DskipTests package)
(cd "$fluxion_root/server" && ./gradlew -q installDist)
(cd "$bids_root/backend" && go build -trimpath -o "$work_dir/bids-worker" ./cmd/worker)
go build -trimpath -o "$work_dir/record-hub" "$record_hub_root/server/cmd/record-hub"

approver_jar="$(find "$approver_root/approver-api/target" -maxdepth 1 -type f -name 'approver-api-*.jar' ! -name '*-plain.jar' | head -1)"
fluxion_bin="$fluxion_root/server/build/install/fluxion-server/bin/fluxion-server"
[[ -f "$approver_jar" && -x "$fluxion_bin" ]] || {
  echo "producer build artifacts were not created" >&2
  exit 1
}

echo "M5 supervised live gate: creating isolated producer databases"
createdb -U "$pg_user" "$approver_db"
created_databases+=("$approver_db")
createdb -U "$pg_user" "$fluxion_db"
created_databases+=("$fluxion_db")

echo "M5 supervised live gate: starting Record Hub and producer relays"
(
  cd "$record_hub_root"
  exec env \
    RECORD_HUB_MODE=all \
    RECORD_HUB_HTTP_ADDRESS="$record_hub_address" \
    RECORD_HUB_SHUTDOWN_TIMEOUT=5s \
    RECORD_HUB_MONGODB_URI="$mongo_uri" \
    RECORD_HUB_MONGODB_DATABASE="$database" \
    RECORD_HUB_NATS_URL="$nats_url" \
    RECORD_HUB_PROJECTION_WORKSPACE_ID="$workspace" \
    "$work_dir/record-hub" serve
) >"$work_dir/record-hub.log" 2>&1 &
record_hub_pid=$!

for _ in {1..160}; do
  curl -fsS "http://$record_hub_address/readyz" >/dev/null 2>&1 && break
  kill -0 "$record_hub_pid" 2>/dev/null || { cat "$work_dir/record-hub.log" >&2; exit 1; }
  sleep 0.25
done
curl -fsS "http://$record_hub_address/readyz" >/dev/null

(
  cd "$approver_root"
  exec env \
    APPROVER_SECURITY_MODE=HEADER \
    APPROVER_API_PORT="$approver_port" \
    APPROVER_DB_URL="jdbc:postgresql://127.0.0.1:5432/$approver_db" \
    APPROVER_DB_USER="$pg_user" \
    APPROVER_DB_PASSWORD="${APPROVER_DB_PASSWORD:-}" \
    APPROVER_RECORD_HUB_WORKSPACE_ID="$workspace" \
    APPROVER_RECORD_HUB_NATS_URL="$nats_url" \
    java -jar "$approver_jar"
) >"$work_dir/approver.log" 2>&1 &
approver_pid=$!

(
  cd "$fluxion_root/server"
  exec env \
    FLUXION_SERVER_PORT="$fluxion_port" \
    FLUXION_PG_URL="jdbc:postgresql://127.0.0.1:5432/$fluxion_db" \
    FLUXION_PG_USER="$pg_user" \
    FLUXION_PG_PASSWORD="${FLUXION_PG_PASSWORD:-}" \
    FLUXION_RECORD_HUB_NATS_URL="$nats_url" \
    FLUXION_RECORD_HUB_WORKSPACE_ID="$workspace" \
    "$fluxion_bin"
) >"$work_dir/fluxion.log" 2>&1 &
fluxion_pid=$!

for _ in {1..240}; do
  approver_ready=0
  fluxion_ready=0
  curl -fsS "http://127.0.0.1:$approver_port/actuator/health" >/dev/null 2>&1 && approver_ready=1
  curl -fsS "http://127.0.0.1:$fluxion_port/api/health" >/dev/null 2>&1 && fluxion_ready=1
  [[ "$approver_ready" == 1 && "$fluxion_ready" == 1 ]] && break
  kill -0 "$approver_pid" 2>/dev/null || { cat "$work_dir/approver.log" >&2; exit 1; }
  kill -0 "$fluxion_pid" 2>/dev/null || { cat "$work_dir/fluxion.log" >&2; exit 1; }
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$approver_port/actuator/health" >/dev/null
curl -fsS "http://127.0.0.1:$fluxion_port/api/health" >/dev/null

bids_db="$work_dir/bids.db"
# Use a fresh SQLite file: the checked-in demo database may contain historical
# rows that cannot be reshaped by GORM's current foreign-key migration. The
# worker still owns and executes the complete migration against this file.
(
  cd "$bids_root/backend"
  exec env \
    DB_DRIVER=sqlite \
    DATABASE_DSN="file:$bids_db?cache=shared&_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL" \
    CONDUCTOR_SERVER_URL="${CONDUCTOR_SERVER_URL:-http://127.0.0.1:8080/api}" \
    RECORD_HUB_NATS_URL="$nats_url" \
    RECORD_HUB_WORKSPACE_ID="$workspace" \
    OUTBOX_POLL_INTERVAL=250ms \
    "$work_dir/bids-worker"
) >"$work_dir/bids.log" 2>&1 &
bids_pid=$!
for _ in {1..240}; do
  grep -q 'bids workers started' "$work_dir/bids.log" && break
  kill -0 "$bids_pid" 2>/dev/null || { cat "$work_dir/bids.log" >&2; exit 1; }
  sleep 0.25
done
grep -q 'bids workers started' "$work_dir/bids.log" || { cat "$work_dir/bids.log" >&2; exit 1; }

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
approver_aggregate_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
fluxion_aggregate_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
bids_aggregate_id="${tenant}-tender"
approver_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
fluxion_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
bids_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"

approver_payload="$(jq -cn --arg id "${tenant}-application" --arg now "$now" '{applicationId:$id,title:"M5 supervised application",status:"OPEN",processRef:"m5-supervised-workflow",updatedAt:$now,version:1}')"
fluxion_payload="$(jq -cn --arg id "${tenant}-project" --arg now "$now" '{projectId:$id,type:"engineering",status:"ACTIVE",currentStage:"IMPLEMENTATION",workflowRef:"m5-supervised-workflow",updatedAt:$now,version:1}')"
bids_payload="$(jq -cn --arg id "${tenant}-tender" --arg now "$now" '{tenderId:$id,buyerOrganization:"M5 supervised buyer",name:"M5 supervised tender",status:"OPEN",template:"standard-v1",updatedAt:$now,version:1}')"

make_envelope() {
  local source="$1" event_type="$2" aggregate_type="$3" aggregate_id="$4" event_id="$5" schema="$6" payload="$7"
  jq -cn --arg eventId "$event_id" --arg eventType "$event_type" --arg sourceSystem "$source" \
    --arg tenantId "$tenant" --arg aggregateType "$aggregate_type" --arg aggregateId "$aggregate_id" \
    --arg occurredAt "$now" --arg workspaceId "$workspace" --arg schemaId "$schema" --argjson payload "$payload" \
    '{eventId:$eventId,kind:"event",eventType:$eventType,schemaVersion:1,sourceSystem:$sourceSystem,tenantId:$tenantId,aggregateType:$aggregateType,aggregateId:$aggregateId,aggregateVersion:1,correlationId:"m5-supervised-workflow",occurredAt:$occurredAt,payload:$payload,metadata:{schemaId:$schemaId,workspaceId:$workspaceId}}'
}

approver_envelope="$(make_envelope approver approver.application.summary-changed Application "$approver_aggregate_id" "$approver_event_id" urn:record-hub:summary:application:v1 "$approver_payload")"
fluxion_envelope="$(make_envelope fluxion fluxion.project.summary-changed Project "$fluxion_aggregate_id" "$fluxion_event_id" urn:record-hub:summary:project:v1 "$fluxion_payload")"
bids_envelope="$(make_envelope bids bids.tender.summary-changed Tender "$bids_aggregate_id" "$bids_event_id" urn:record-hub:summary:tender:v1 "$bids_payload")"

psql -X -q -v ON_ERROR_STOP=1 -U "$pg_user" -d "$approver_db" \
  -v event_id="$approver_event_id" -v tenant_id="$tenant" -v aggregate_id="$approver_aggregate_id" -v payload="$approver_envelope" <<'SQL'
INSERT INTO integration_outbox (id, tenant_id, aggregate_type, aggregate_id, message_type, payload)
VALUES (:'event_id'::uuid, :'tenant_id', 'APPLICATION', :'aggregate_id'::uuid,
        'APPLICATION_SUMMARY_CHANGED', :'payload'::jsonb);
SQL
psql -X -q -v ON_ERROR_STOP=1 -U "$pg_user" -d "$fluxion_db" \
  -v event_id="$fluxion_event_id" -v tenant_id="$tenant" -v aggregate_id="$fluxion_aggregate_id" -v payload="$fluxion_envelope" <<'SQL'
INSERT INTO record_hub_outbox (id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, attempts)
VALUES (:'event_id'::uuid, :'tenant_id', 'Project', :'aggregate_id'::uuid,
        'fluxion.project.summary-changed', :'payload', 'PENDING', 0);
SQL
sqlite3 "$bids_db" <<SQL
.parameter init
.parameter set :payload '$bids_envelope'
INSERT INTO outbox_events (id, organization_id, event_type, aggregate_type, aggregate_id, deduplication_key, payload, status, attempts, available_at, created_at, updated_at)
VALUES ('$bids_event_id', '$tenant', 'TENDER_SUMMARY_CHANGED', 'TENDER', '$bids_aggregate_id', 'record-hub:m5-supervised:$bids_aggregate_id', :payload, 'PENDING', 0, datetime('now'), datetime('now'), datetime('now'));
SQL
echo "M5 supervised live gate: seeded source outboxes"

for _ in {1..240}; do
  approver_status="$(psql -X -At -U "$pg_user" -d "$approver_db" -c "SELECT status FROM integration_outbox WHERE id = '$approver_event_id'" 2>/dev/null || true)"
  fluxion_status="$(psql -X -At -U "$pg_user" -d "$fluxion_db" -c "SELECT status FROM record_hub_outbox WHERE id = '$fluxion_event_id'" 2>/dev/null || true)"
  bids_status="$(sqlite3 "$bids_db" "SELECT status FROM outbox_events WHERE id = '$bids_event_id'")"
  [[ "$approver_status" == SENT && "$fluxion_status" == SENT && "$bids_status" == SUCCEEDED ]] && break
  sleep 0.25
done
[[ "$approver_status" == SENT && "$fluxion_status" == SENT && "$bids_status" == SUCCEEDED ]] || {
  echo "producer outboxes did not drain: approver=$approver_status fluxion=$fluxion_status bids=$bids_status" >&2
  echo "--- approver ---" >&2; tail -40 "$work_dir/approver.log" >&2
  echo "--- fluxion ---" >&2; tail -40 "$work_dir/fluxion.log" >&2
  echo "--- bids ---" >&2; tail -40 "$work_dir/bids.log" >&2
  exit 1
}

for _ in {1..240}; do
  projected="$(mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); print(d.records.countDocuments({tenantId:'$tenant',workspaceId:'$workspace',projection:{\$exists:true}}))" | tr -d '[:space:]')"
  [[ "$projected" == 3 ]] && break
  sleep 0.25
done
[[ "$projected" == 3 ]] || { echo "expected 3 projected records, got $projected" >&2; exit 1; }
mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const rows=d.records.find({tenantId:'$tenant',workspaceId:'$workspace'}).toArray(); if(rows.length!==3 || rows.some(r=>r.projection==null || r.projection.status!=='CURRENT')) quit(1); print('M5 supervised source outbox -> relay -> projection: PASS');"
echo "M5-059 supervised live producer -> relay -> projection gate: PASS"
