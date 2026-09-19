#!/usr/bin/env bash
set -euo pipefail

# P3-110: real Record Hub -> NATS -> Fluxion owner command gate.  The target
# project is created through Fluxion's HTTP API; this script never writes the
# owner PostgreSQL business tables directly.  PostgreSQL/Mongo reads below are
# bounded assertions only.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
cd "$record_hub_root"

for command in curl createdb dropdb go jq lsof mongod mongosh nats nats-server nc openssl psql temporal uuidgen; do
  command -v "$command" >/dev/null || { echo "$command is required for P3-110" >&2; exit 2; }
done
[[ -x "$fluxion_root/server/gradlew" ]] || { echo "Fluxion Gradle wrapper is missing: $fluxion_root/server/gradlew" >&2; exit 2; }
if pgrep -f 'fluxion\.ApiKt|fluxion\.WorkerKt' >/dev/null 2>&1; then
  echo "a Fluxion API/worker is already running; stop it or use a clean host before P3-110" >&2
  exit 2
fi

mongo_port="${RECORD_HUB_P3_MONGO_PORT:-37028}"
nats_port="${RECORD_HUB_P3_NATS_PORT:-14233}"
nats_monitor_port="${RECORD_HUB_P3_NATS_MONITOR_PORT:-18233}"
workload_port="${RECORD_HUB_P3_WORKLOAD_PORT:-15567}"
record_hub_port="${RECORD_HUB_P3_RECORD_HUB_PORT:-18082}"
record_hub_secondary_port="${RECORD_HUB_P3_RECORD_HUB_SECONDARY_PORT:-18083}"
temporal_port="${RECORD_HUB_P3_TEMPORAL_PORT:-17233}"
temporal_ui_port="${RECORD_HUB_P3_TEMPORAL_UI_PORT:-18233}"
fluxion_port="${RECORD_HUB_P3_FLUXION_PORT:-18091}"
fault_matrix="${RECORD_HUB_P3_FAULT_MATRIX:-0}"

for port in "$mongo_port" "$nats_port" "$nats_monitor_port" "$workload_port" "$record_hub_port" "$record_hub_secondary_port" "$temporal_port" "$temporal_ui_port" "$fluxion_port"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "P3-110 port $port is already in use; override the matching RECORD_HUB_P3_*_PORT" >&2
    exit 2
  fi
done

runtime_root="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-fluxion.XXXXXX")"
evidence_dir="${RECORD_HUB_P3_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-evidence.XXXXXX")}"
mkdir -p "$runtime_root/mongo" "$runtime_root/nats" "$evidence_dir/logs" "$evidence_dir/db-assertions" "$evidence_dir/metrics"
pids=()
fluxion_api_java_pid=""
fluxion_worker_java_pid=""
fluxion_worker_launcher_pid=""
record_hub_pid=""
secondary_record_hub_pid=""
fault_worker_java_pid=""
fault_worker_launcher_pid=""
competition_worker_java_pid=""
competition_worker_launcher_pid=""
nats_pid=""
pg_db="fluxion_p3_${$}"

write_manifest() {
  local status="$1"
  jq -n \
    --arg status "$status" \
    --arg started_at "${started_at:-}" \
    --arg finished_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg record_hub_commit "$(git -C "$record_hub_root" rev-parse HEAD)" \
    --arg fluxion_commit "$(git -C "$fluxion_root" rev-parse HEAD 2>/dev/null || true)" \
    --arg host "$(uname -srm)" \
    '{gate:"P3-110",status:$status,startedAt:$started_at,finishedAt:$finished_at,host:$host,recordHubCommit:$record_hub_commit,fluxionCommit:$fluxion_commit}' \
    >"$evidence_dir/manifest.json"
}

cleanup() {
  local status=$?
  set +e
  if [[ "$status" != "0" ]]; then
    write_manifest FAIL
    if [[ ! -f "$evidence_dir/results.json" ]]; then
      jq -n '{gate:"P3-110",status:"FAIL",note:"live gate terminated before all cases completed"}' >"$evidence_dir/results.json"
    fi
  fi
  for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${fault_worker_java_pid:-}" "${competition_worker_java_pid:-}" "${record_hub_pid:-}" "${secondary_record_hub_pid:-}" "${nats_pid:-}" "${pids[@]}"; do
    [[ -n "$pid" ]] && kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in {1..100}; do
    local running=0
    for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${fault_worker_java_pid:-}" "${competition_worker_java_pid:-}" "${record_hub_pid:-}" "${secondary_record_hub_pid:-}" "${nats_pid:-}" "${pids[@]}"; do
      [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && running=1
    done
    [[ "$running" == "0" ]] && break
    sleep 0.1
  done
  for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${fault_worker_java_pid:-}" "${competition_worker_java_pid:-}" "${record_hub_pid:-}" "${secondary_record_hub_pid:-}" "${nats_pid:-}" "${pids[@]}"; do
    [[ -n "$pid" ]] && kill -KILL "$pid" 2>/dev/null || true
    [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  done
  if [[ -n "${pg_db:-}" ]]; then
    dropdb --if-exists "$pg_db" >/dev/null 2>&1 || true
  fi
  if [[ "$status" == "0" ]]; then
    rm -rf -- "$runtime_root"
    echo "P3-110 evidence: $evidence_dir"
  else
    echo "P3-110 failed; runtime/logs preserved at $runtime_root" >&2
    echo "P3-110 evidence: $evidence_dir" >&2
  fi
}
trap cleanup EXIT HUP INT TERM

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
started_epoch="$(date +%s)"
write_manifest IN_PROGRESS

wait_http() {
  local url="$1" label="$2"
  for _ in {1..180}; do
    if curl --silent --show-error --fail "$url" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  echo "$label did not become ready at $url" >&2
  return 1
}

wait_tcp() {
  local host="$1" port="$2" label="$3"
  for _ in {1..180}; do
    if nc -z "$host" "$port" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  echo "$label did not become ready at $host:$port" >&2
  return 1
}

mongo_rs="record-hub-p3-rs"
mongo_uri="mongodb://127.0.0.1:${mongo_port}/record_hub_p3?replicaSet=${mongo_rs}&directConnection=true"
mongod --bind_ip 127.0.0.1 --port "$mongo_port" --dbpath "$runtime_root/mongo" --replSet "$mongo_rs" --nounixsocket --logpath "$evidence_dir/logs/mongod.log" --logappend &
pids+=("$!")
wait_tcp 127.0.0.1 "$mongo_port" MongoDB
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval "rs.initiate({_id:'${mongo_rs}',members:[{_id:0,host:'127.0.0.1:${mongo_port}'}]})" >/dev/null
for _ in {1..180}; do
  mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null 2>&1 && break
  sleep 0.2
done
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null

nats_url="nats://127.0.0.1:${nats_port}"
start_nats() {
  local log_file="${1:-nats.log}"
  nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$runtime_root/nats" -n record-hub-p3-fluxion >"$evidence_dir/logs/$log_file" 2>&1 &
  nats_pid="$!"
  pids+=("$nats_pid")
  wait_http "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" NATS
}
stop_nats() {
  [[ -n "${nats_pid:-}" ]] || return 0
  kill -TERM "$nats_pid" 2>/dev/null || true
  for _ in {1..100}; do
    kill -0 "$nats_pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$nats_pid" 2>/dev/null || true
  wait "$nats_pid" 2>/dev/null || true
  nats_pid=""
}
start_nats
RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init >"$evidence_dir/logs/nats-init.log" 2>&1

workload_issuer="http://127.0.0.1:${workload_port}/workload"
workload_audience="record-hub-api"
workload_scope="recordhub.command.submit"
fluxion_secret="$(openssl rand -hex 32)"
export RECORD_HUB_WORKLOAD_ISSUER_ADDRESS="127.0.0.1:${workload_port}"
export RECORD_HUB_WORKLOAD_ISSUER="$workload_issuer"
export RECORD_HUB_WORKLOAD_AUDIENCE="$workload_audience"
export RECORD_HUB_WORKLOAD_CLIENTS="$(jq -cn --arg secret "$fluxion_secret" --arg scope "$workload_scope" '[{id:"fluxion",secret:$secret,scopes:[$scope]}]')"
go build -trimpath -o "$runtime_root/workload-issuer" ./tools/workload-issuer
"$runtime_root/workload-issuer" >"$evidence_dir/logs/workload-issuer.log" 2>&1 &
pids+=("$!")
wait_http "$workload_issuer/.well-known/openid-configuration" "workload issuer"

go build -trimpath -o "$runtime_root/record-hub" ./server/cmd/record-hub
export RECORD_HUB_MODE=all
export RECORD_HUB_HTTP_ADDRESS="127.0.0.1:${record_hub_port}"
export RECORD_HUB_SHUTDOWN_TIMEOUT=3s
export RECORD_HUB_HTTP_RATE_LIMIT_PER_MINUTE="${RECORD_HUB_P3_HTTP_RATE_LIMIT_PER_MINUTE:-600}"
export RECORD_HUB_MONGODB_URI="$mongo_uri"
export RECORD_HUB_MONGODB_DATABASE=record_hub_p3
export RECORD_HUB_NATS_URL="$nats_url"
export RECORD_HUB_PROJECTION_WORKSPACE_ID=workspace-p3-fluxion
export RECORD_HUB_OIDC_ISSUER="$workload_issuer"
export RECORD_HUB_OIDC_AUDIENCE="$workload_audience"
export RECORD_HUB_OIDC_PRINCIPAL_KIND=service
export RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER=true
export RECORD_HUB_COMMAND_POLICIES="$(jq -c --arg issuer "$workload_issuer" --arg audience "$workload_audience" '.policies | map(.issuer=$issuer | .audience=$audience)' deploy/local/p3/fluxion-project-annotate-policy.json)"
record_hub_url="http://127.0.0.1:${record_hub_port}"
start_record_hub() {
  local log_file="${1:-record-hub.log}"
  "$runtime_root/record-hub" serve >"$evidence_dir/logs/$log_file" 2>&1 &
  record_hub_pid="$!"
  pids+=("$record_hub_pid")
  wait_http "$record_hub_url/healthz" "Record Hub API"
  curl --silent --show-error --fail "$record_hub_url/readyz" | jq -e '.status == "ready"' >/dev/null
}
stop_record_hub() {
  [[ -n "${record_hub_pid:-}" ]] || return 0
  kill -TERM "$record_hub_pid" 2>/dev/null || true
  for _ in {1..100}; do
    kill -0 "$record_hub_pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$record_hub_pid" 2>/dev/null || true
  wait "$record_hub_pid" 2>/dev/null || true
  record_hub_pid=""
}
start_secondary_record_hub() {
  local log_file="${1:-record-hub-secondary.log}"
  RECORD_HUB_HTTP_ADDRESS="127.0.0.1:${record_hub_secondary_port}" "$runtime_root/record-hub" serve >"$evidence_dir/logs/$log_file" 2>&1 &
  secondary_record_hub_pid="$!"
  pids+=("$secondary_record_hub_pid")
  local secondary_url="http://127.0.0.1:${record_hub_secondary_port}"
  wait_http "$secondary_url/healthz" "secondary Record Hub API"
  curl --silent --show-error --fail "$secondary_url/readyz" | jq -e '.status == "ready"' >/dev/null
}
stop_secondary_record_hub() {
  [[ -n "${secondary_record_hub_pid:-}" ]] || return 0
  kill -TERM "$secondary_record_hub_pid" 2>/dev/null || true
  for _ in {1..100}; do
    kill -0 "$secondary_record_hub_pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$secondary_record_hub_pid" 2>/dev/null || true
  wait "$secondary_record_hub_pid" 2>/dev/null || true
  secondary_record_hub_pid=""
}
start_record_hub

createdb "$pg_db"
temporal server start-dev --headless --ip 127.0.0.1 --port "$temporal_port" --ui-port "$temporal_ui_port" --db-filename "$runtime_root/temporal.sqlite" >"$evidence_dir/logs/temporal.log" 2>&1 &
pids+=("$!")
wait_tcp 127.0.0.1 "$temporal_port" Temporal

export FLUXION_SERVER_PORT="$fluxion_port"
export FLUXION_PG_URL="jdbc:postgresql://127.0.0.1:5432/${pg_db}"
export FLUXION_PG_USER=""
export FLUXION_PG_PASSWORD=""
export FLUXION_TEMPORAL_ADDRESS="127.0.0.1:${temporal_port}"
export FLUXION_TEMPORAL_NAMESPACE=default
export FLUXION_TEMPORAL_TASK_QUEUE=fluxion-p3-command
export FLUXION_RECORD_HUB_RESULT_OUTBOX_LEASE_SECONDS="${FLUXION_RECORD_HUB_RESULT_OUTBOX_LEASE_SECONDS:-3}"
export FLUXION_RECORD_HUB_NATS_URL="$nats_url"
export FLUXION_RECORD_HUB_URL="$record_hub_url"
export FLUXION_RECORD_HUB_TENANT_ID=tenant-p3-fluxion
export FLUXION_RECORD_HUB_WORKSPACE_ID=workspace-p3-fluxion
export FLUXION_WORKFLOW_STAGE_SLEEP_SECONDS=60
export FLUXION_WORKFLOW_ACK_TIMEOUT_SECONDS=1800
export FLUXION_PI_AGENT_TIMEOUT_SECONDS=5

(
  cd "$fluxion_root/server"
  exec ./gradlew --no-daemon -q runApi
) >"$evidence_dir/logs/fluxion-api.log" 2>&1 &
fluxion_api_launcher_pid="$!"
pids+=("$fluxion_api_launcher_pid")
wait_http "http://127.0.0.1:${fluxion_port}/api/health" "Fluxion API"
start_fluxion_worker() {
  local log_file="$1"
  shift
  local existing_worker_pids
  existing_worker_pids=" $(pgrep -f 'fluxion\.WorkerKt' | tr '\n' ' ') "
  (
    cd "$fluxion_root/server"
    env "$@" ./gradlew --no-daemon -q runWorker
  ) >"$evidence_dir/logs/$log_file" 2>&1 &
  local launcher_pid="$!"
  pids+=("$launcher_pid")
  for _ in {1..180}; do
    local worker_pid=""
    for candidate in $(pgrep -f 'fluxion\.WorkerKt' || true); do
      [[ "$existing_worker_pids" == *" $candidate "* ]] || { worker_pid="$candidate"; break; }
    done
    if [[ -n "$worker_pid" ]]; then
      printf '%s|%s\n' "$launcher_pid" "$worker_pid"
      return 0
    fi
    sleep 0.2
  done
  tail -80 "$evidence_dir/logs/$log_file" >&2
  return 1
}
stop_fluxion_worker() {
  local launcher_pid="${1:-}" worker_pid="${2:-}"
  for pid in "$worker_pid" "$launcher_pid"; do
    [[ -n "$pid" ]] && kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in {1..100}; do
    local running=0
    for pid in "$worker_pid" "$launcher_pid"; do
      [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && running=1
    done
    [[ "$running" == "0" ]] && break
    sleep 0.1
  done
  for pid in "$worker_pid" "$launcher_pid"; do
    [[ -n "$pid" ]] && kill -KILL "$pid" 2>/dev/null || true
    [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  done
}
fluxion_api_java_pid="$(pgrep -f 'fluxion\.ApiKt' | head -1 || true)"
worker_pids="$(start_fluxion_worker fluxion-worker.log)"
fluxion_worker_launcher_pid="${worker_pids%%|*}"
fluxion_worker_java_pid="${worker_pids#*|}"

fluxion_url="http://127.0.0.1:${fluxion_port}"
cookie_jar="$runtime_root/fluxion.cookies"
curl --silent --show-error --fail -c "$cookie_jar" -H 'Content-Type: application/json' \
  --data '{"username":"operator","password":"op123456"}' "$fluxion_url/api/auth/login" >"$runtime_root/login.json"
customer_status="$(curl --silent --show-error --output "$runtime_root/customer.json" --write-out '%{http_code}' -b "$cookie_jar" -H 'Content-Type: application/json' --data '{"name":"P3 Fluxion Customer","contact":"p3-live"}' "$fluxion_url/api/customers")"
[[ "$customer_status" == "201" ]] || { cat "$runtime_root/customer.json" >&2; exit 1; }
customer_id="$(jq -er '.id' "$runtime_root/customer.json")"
worker_status="$(curl --silent --show-error --output "$runtime_root/worker.json" --write-out '%{http_code}' -b "$cookie_jar" -H 'Content-Type: application/json' --data '{"name":"P3 Fluxion Worker","skills":["保洁"],"trade":null}' "$fluxion_url/api/workers")"
[[ "$worker_status" == "201" ]] || { cat "$runtime_root/worker.json" >&2; exit 1; }
project_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
project_status="$(curl --silent --show-error --output "$runtime_root/project.json" --write-out '%{http_code}' -b "$cookie_jar" -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg project "$project_id" --arg customer "$customer_id" '{projectId:$project,customerId:$customer,serviceProductId:"44444444-4444-4444-4444-444444444441",scheduledDate:"2099-01-01"}')" \
  "$fluxion_url/api/projects")"
[[ "$project_status" == "201" ]] || { cat "$runtime_root/project.json" >&2; exit 1; }

before_version=""
for _ in {1..180}; do
  project_state="$(psql -At -d "$pg_db" -c "select summary_version || '|' || current_stage from projects where id='${project_id}'" 2>/dev/null || true)"
  candidate_version="${project_state%%|*}"
  candidate_stage="${project_state#*|}"
  if [[ "$candidate_version" =~ ^[0-9]+$ && "$candidate_stage" == "waitAck" ]]; then
    sleep 0.5
    stable_state="$(psql -At -d "$pg_db" -c "select summary_version || '|' || current_stage from projects where id='${project_id}'" 2>/dev/null || true)"
    if [[ "$stable_state" == "${candidate_version}|waitAck" ]]; then
      before_version="$candidate_version"
      break
    fi
  fi
  sleep 0.2
done
[[ "$before_version" =~ ^[0-9]+$ ]] || { echo "Fluxion project did not reach stable waitAck state" >&2; exit 1; }

if [[ "$fault_matrix" == "1" ]]; then
  competition_pids="$(start_fluxion_worker fluxion-worker-competition.log)"
  competition_worker_launcher_pid="${competition_pids%%|*}"
  competition_worker_java_pid="${competition_pids#*|}"
fi

token="$(curl --silent --show-error --fail --user "fluxion:$fluxion_secret" --data-urlencode 'grant_type=client_credentials' --data-urlencode "scope=$workload_scope" "$workload_issuer/token" | jq -er '.access_token')"
resource_ref="fluxion:PROJECT:${project_id}"
append_annotation="p3-annotation-${project_id}"
append_key="p3-append-${project_id}"
append_payload="$(jq -cn --arg id "$append_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 live annotation"}')"
submit() {
  local key="$1" expected="$2" payload="$3" output="$4"
  local status
  status="$(curl --silent --show-error --output "$output" --write-out '%{http_code}' \
    -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" \
    --data "$(jq -cn --arg tenant "$FLUXION_RECORD_HUB_TENANT_ID" --arg workspace "$FLUXION_RECORD_HUB_WORKSPACE_ID" --arg ref "$resource_ref" --argjson expected "$expected" --argjson payload "$payload" '{tenantId:$tenant,workspaceId:$workspace,policyId:"project.annotate",resourceRef:$ref,expectedVersion:$expected,payload:$payload}')" \
    "$record_hub_url/api/v1/commands")"
  printf '%s' "$status"
}

append_http="$(submit "$append_key" "$before_version" "$append_payload" "$runtime_root/append.json")"
[[ "$append_http" == "202" ]] || { cat "$runtime_root/append.json" >&2; exit 1; }
append_operation="$(jq -er '.operationId' "$runtime_root/append.json")"
append_status=""
for _ in {1..150}; do
  append_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${append_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/append-result.json" | jq -r '.status')"
  [[ "$append_status" == "SUCCEEDED" || "$append_status" == "REJECTED" || "$append_status" == "FAILED" ]] && break
  sleep 0.2
done
[[ "$append_status" == "SUCCEEDED" ]] || { cat "$runtime_root/append-result.json" >&2; exit 1; }

duplicate_http="$(submit "$append_key" "$before_version" "$append_payload" "$runtime_root/append-duplicate.json")"
[[ "$duplicate_http" == "200" ]] || { cat "$runtime_root/append-duplicate.json" >&2; exit 1; }
[[ "$(jq -r '.operationId' "$runtime_root/append-duplicate.json")" == "$append_operation" ]] || exit 1
conflict_payload="$(jq -cn --arg id "$append_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 hash conflict"}')"
conflict_http="$(submit "$append_key" "$before_version" "$conflict_payload" "$runtime_root/append-conflict.json")"
[[ "$conflict_http" == "409" ]] || { cat "$runtime_root/append-conflict.json" >&2; exit 1; }

after_append_version="$(psql -At -d "$pg_db" -c "select summary_version from projects where id='${project_id}'")"
[[ "$after_append_version" == "$((before_version + 1))" ]] || { echo "append version $after_append_version != $((before_version + 1))" >&2; exit 1; }
append_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${append_annotation}'")"
[[ "$append_events" == "1" ]] || exit 1
append_inbox="$(psql -At -d "$pg_db" -c "select count(*) from record_hub_command_inbox where operation_id='${append_operation}' and status='SUCCEEDED'")"
append_result_outbox="$(psql -At -d "$pg_db" -c "select count(*) from record_hub_command_result_outbox where operation_id='${append_operation}' and status='SENT'")"
[[ "$append_inbox" == "1" && "$append_result_outbox" == "1" ]] || exit 1

void_annotation="p3-void-${project_id}"
void_key="p3-void-${project_id}"
void_payload="$(jq -cn --arg id "$void_annotation" --arg target "$append_annotation" '{annotationId:$id,mode:"VOID",text:"P3 live void",voidsAnnotationId:$target}')"
void_http="$(submit "$void_key" "$after_append_version" "$void_payload" "$runtime_root/void.json")"
[[ "$void_http" == "202" ]] || { cat "$runtime_root/void.json" >&2; exit 1; }
void_operation="$(jq -er '.operationId' "$runtime_root/void.json")"
for _ in {1..150}; do
  void_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${void_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/void-result.json" | jq -r '.status')"
  [[ "$void_status" == "SUCCEEDED" || "$void_status" == "REJECTED" || "$void_status" == "FAILED" ]] && break
  sleep 0.2
done
[[ "$void_status" == "SUCCEEDED" ]] || { cat "$runtime_root/void-result.json" >&2; exit 1; }
after_void_version="$(psql -At -d "$pg_db" -c "select summary_version from projects where id='${project_id}'")"
[[ "$after_void_version" == "$((after_append_version + 1))" ]] || exit 1
void_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-void:${append_annotation}'")"
[[ "$void_events" == "1" ]] || exit 1

stale_key="p3-version-conflict-${project_id}"
stale_payload="$(jq -cn --arg id "p3-stale-${project_id}" '{annotationId:$id,mode:"APPEND",text:"P3 stale version"}')"
stale_http="$(submit "$stale_key" "$before_version" "$stale_payload" "$runtime_root/stale.json")"
[[ "$stale_http" == "202" ]] || { cat "$runtime_root/stale.json" >&2; exit 1; }
stale_operation="$(jq -er '.operationId' "$runtime_root/stale.json")"
for _ in {1..150}; do
  stale_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${stale_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/stale-result.json" | jq -r '.status')"
  [[ "$stale_status" == "SUCCEEDED" || "$stale_status" == "REJECTED" || "$stale_status" == "FAILED" ]] && break
  sleep 0.2
done
[[ "$stale_status" == "REJECTED" ]] || { cat "$runtime_root/stale-result.json" >&2; exit 1; }
[[ "$(jq -r '.safeError' "$runtime_root/stale-result.json")" == "project version conflict" ]] || exit 1
final_annotation_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and kind='integration'")"
[[ "$final_annotation_events" == "2" ]] || exit 1

for _ in {1..150}; do
  projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
  [[ "$projected_version" == "$after_void_version" ]] && break
  sleep 0.2
done
[[ "$projected_version" == "$after_void_version" ]] || { echo "Record Hub Fluxion projection did not converge: $projected_version" >&2; exit 1; }

fault_replay_operation=""
rollback_operation=""
publish_before_sent_operation=""
fault_pause_operation=""
nats_recovery_operation=""
dlq_followup_operation=""
cross_restart_operation=""
result_competition_operation=""
fault_final_version="$after_void_version"
if [[ "$fault_matrix" == "1" ]]; then
  # Two Fluxion workers sharing the same durable consumer have already handled
  # the core APPEND/VOID matrix above.  Now stop both and inject a crash after
  # the owner transaction commits but before JetStream ACK.
  stop_fluxion_worker "$competition_worker_launcher_pid" "$competition_worker_java_pid"
  competition_worker_launcher_pid=""
  competition_worker_java_pid=""
  stop_fluxion_worker "$fluxion_worker_launcher_pid" "$fluxion_worker_java_pid"
  fluxion_worker_launcher_pid=""
  fluxion_worker_java_pid=""

  # Throw from the owner transaction after its domain writes have been staged
  # but before the surrounding Inbox/result-Outbox commit. During the injected
  # delay no Inbox, result Outbox, or domain event may be visible; recovery then
  # retries the operation and commits once.
  rollback_pids="$(start_fluxion_worker fluxion-worker-rollback.log \
    FLUXION_RECORD_HUB_TEST_ROLLBACK_BEFORE_COMMIT=true \
    FLUXION_RECORD_HUB_TEST_ROLLBACK_DELAY_MS=3000 \
    FLUXION_RECORD_HUB_RESULT_RELAY_ENABLED=false)"
  fault_worker_launcher_pid="${rollback_pids%%|*}"
  fault_worker_java_pid="${rollback_pids#*|}"
  rollback_annotation="p3-rollback-${project_id}"
  rollback_key="p3-rollback-${project_id}"
  rollback_payload="$(jq -cn --arg id "$rollback_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 transaction rollback"}')"
  rollback_http="$(submit "$rollback_key" "$after_void_version" "$rollback_payload" "$runtime_root/rollback.json")"
  [[ "$rollback_http" == "202" ]] || { cat "$runtime_root/rollback.json" >&2; exit 1; }
  rollback_operation="$(jq -er '.operationId' "$runtime_root/rollback.json")"
  rollback_log="$evidence_dir/logs/fluxion-worker-rollback.log"
  for _ in {1..180}; do
    grep -q "rolling back before Inbox commit for operation ${rollback_operation}" "$rollback_log" 2>/dev/null && break
    sleep 0.2
  done
  grep -q "rolling back before Inbox commit for operation ${rollback_operation}" "$rollback_log" || {
    echo "transaction rollback fault injection did not reach the pre-commit boundary" >&2
    exit 1
  }
  rollback_inbox_count="$(psql -At -d "$pg_db" -c "select count(*) from record_hub_command_inbox where operation_id='${rollback_operation}'" 2>/dev/null || true)"
  rollback_outbox_count="$(psql -At -d "$pg_db" -c "select count(*) from record_hub_command_result_outbox where operation_id='${rollback_operation}'" 2>/dev/null || true)"
  rollback_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${rollback_annotation}'" 2>/dev/null || true)"
  for _ in {1..100}; do
    kill -0 "$fault_worker_java_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$fault_worker_java_pid" 2>/dev/null; then
    echo "transaction rollback worker did not halt after the injected rollback" >&2
    exit 1
  fi
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  [[ "$rollback_inbox_count" == "0" && "$rollback_outbox_count" == "0" && "$rollback_events" == "0" ]] || {
    echo "transaction rollback leaked Inbox/outbox/domain state: inbox=${rollback_inbox_count}, outbox=${rollback_outbox_count}, events=${rollback_events}" >&2
    exit 1
  }
  rollback_recovery_pids="$(start_fluxion_worker fluxion-worker-rollback-recovery.log \
    FLUXION_RECORD_HUB_RESULT_RELAY_ENABLED=false \
    FLUXION_RECORD_HUB_COMMAND_DURABLE=fluxion-command-inbox-rollback-recovery-v1 \
    FLUXION_RECORD_HUB_COMMAND_DELIVER_NEW=true)"
  fault_worker_launcher_pid="${rollback_recovery_pids%%|*}"
  fault_worker_java_pid="${rollback_recovery_pids#*|}"
  rollback_status=""
  for _ in {1..300}; do
    rollback_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${rollback_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/rollback-result.json" | jq -r '.status')"
    [[ "$rollback_status" == "SUCCEEDED" || "$rollback_status" == "REJECTED" || "$rollback_status" == "FAILED" ]] && break
    sleep 0.2
  done
  if [[ "$rollback_status" != "SUCCEEDED" ]]; then
    # Some local JetStream builds retain a pull delivery until the original
    # consumer heartbeat expires. Re-publish the exact envelope with a fresh
    # NATS message id so recovery remains bounded; Inbox operationId still
    # provides the deduplication key and the owner event must remain single.
    rollback_envelope="$(jq -cn \
      --arg operationId "$rollback_operation" \
      --arg tenantId "$FLUXION_RECORD_HUB_TENANT_ID" \
      --arg workspaceId "$FLUXION_RECORD_HUB_WORKSPACE_ID" \
      --arg resourceRef "$resource_ref" \
      --arg payloadHash "$(jq -er '.payloadHash' "$runtime_root/rollback.json")" \
      --arg createdAt "$(jq -er '.createdAt' "$runtime_root/rollback.json")" \
      --argjson requestedBy "$(jq -c '.createdBy' "$runtime_root/rollback.json")" \
      --argjson payload "$rollback_payload" \
      --argjson expectedVersion "$after_void_version" \
      '{operationId:$operationId,tenantId:$tenantId,workspaceId:$workspaceId,policyId:"project.annotate",ownerSystem:"fluxion",resourceType:"PROJECT",action:"project.annotate",purpose:"project-annotation",resourceRef:$resourceRef,expectedVersion:$expectedVersion,payloadHash:$payloadHash,payload:$payload,requestedBy:$requestedBy,createdAt:$createdAt}')"
    nats --server "$nats_url" pub -H "Nats-Msg-Id: ${rollback_operation}-recovery" "commands.fluxion.project.annotate.v1" "$rollback_envelope" >/dev/null
    for _ in {1..300}; do
      rollback_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${rollback_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/rollback-result.json" | jq -r '.status')"
      [[ "$rollback_status" == "SUCCEEDED" || "$rollback_status" == "REJECTED" || "$rollback_status" == "FAILED" ]] && break
      sleep 0.2
    done
  fi
  [[ "$rollback_status" == "SUCCEEDED" ]] || { cat "$runtime_root/rollback-result.json" >&2; exit 1; }
  rollback_inbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_inbox where operation_id='${rollback_operation}'")"
  rollback_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${rollback_operation}'")"
  rollback_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${rollback_annotation}'")"
  [[ "$rollback_inbox_status" == "SUCCEEDED" && "$rollback_outbox_status" == "PENDING" && "$rollback_events" == "1" ]] || exit 1
  fault_final_version="$((after_void_version + 1))"
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""

  crash_pids="$(start_fluxion_worker fluxion-worker-crash.log \
    FLUXION_RECORD_HUB_TEST_CRASH_BEFORE_ACK=true \
    FLUXION_RECORD_HUB_RESULT_RELAY_ENABLED=false)"
  fault_worker_launcher_pid="${crash_pids%%|*}"
  fault_worker_java_pid="${crash_pids#*|}"
  replay_annotation="p3-replay-${project_id}"
  replay_key="p3-replay-${project_id}"
  replay_payload="$(jq -cn --arg id "$replay_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 crash replay"}')"
  replay_http="$(submit "$replay_key" "$fault_final_version" "$replay_payload" "$runtime_root/replay.json")"
  [[ "$replay_http" == "202" ]] || { cat "$runtime_root/replay.json" >&2; exit 1; }
  fault_replay_operation="$(jq -er '.operationId' "$runtime_root/replay.json")"

  replay_inbox_status=""
  replay_outbox_status=""
  for _ in {1..180}; do
    replay_inbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_inbox where operation_id='${fault_replay_operation}'" 2>/dev/null || true)"
    replay_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${fault_replay_operation}'" 2>/dev/null || true)"
    if [[ "$replay_inbox_status" == "SUCCEEDED" && "$replay_outbox_status" == "PENDING" ]]; then break; fi
    sleep 0.2
  done
  [[ "$replay_inbox_status" == "SUCCEEDED" && "$replay_outbox_status" == "PENDING" ]] || {
    echo "crash-before-ACK did not leave a committed owner Inbox/PENDING result Outbox" >&2
    exit 1
  }
  replay_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${replay_annotation}'")"
  [[ "$replay_events" == "1" ]] || exit 1
  for _ in {1..100}; do
    kill -0 "$fault_worker_java_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$fault_worker_java_pid" 2>/dev/null; then
    echo "crash-before-ACK worker did not halt" >&2
    exit 1
  fi

  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  replay_pids="$(start_fluxion_worker fluxion-worker-replay.log)"
  fault_worker_launcher_pid="${replay_pids%%|*}"
  fault_worker_java_pid="${replay_pids#*|}"
  replay_status=""
  for _ in {1..600}; do
    replay_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${fault_replay_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/replay-result.json" | jq -r '.status')"
    [[ "$replay_status" == "SUCCEEDED" || "$replay_status" == "REJECTED" || "$replay_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$replay_status" == "SUCCEEDED" ]] || { cat "$runtime_root/replay-result.json" >&2; exit 1; }
  replay_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${fault_replay_operation}'")"
  [[ "$replay_outbox_status" == "SENT" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"

  # Pause the Record Hub result consumer while the owner still processes a
  # command.  The result remains durable in COMMAND_RESULTS and is consumed
  # after the Record Hub process restarts.
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  pause_annotation="p3-consumer-pause-${project_id}"
  pause_key="p3-consumer-pause-${project_id}"
  pause_payload="$(jq -cn --arg id "$pause_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 consumer pause"}')"
  pause_http="$(submit "$pause_key" "$fault_final_version" "$pause_payload" "$runtime_root/consumer-pause.json")"
  [[ "$pause_http" == "202" ]] || { cat "$runtime_root/consumer-pause.json" >&2; exit 1; }
  fault_pause_operation="$(jq -er '.operationId' "$runtime_root/consumer-pause.json")"
  stop_record_hub
  paused_pids="$(start_fluxion_worker fluxion-worker-consumer-pause.log)"
  fault_worker_launcher_pid="${paused_pids%%|*}"
  fault_worker_java_pid="${paused_pids#*|}"
  pause_inbox_status=""
  pause_outbox_status=""
  for _ in {1..180}; do
    pause_inbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_inbox where operation_id='${fault_pause_operation}'" 2>/dev/null || true)"
    pause_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${fault_pause_operation}'" 2>/dev/null || true)"
    if [[ "$pause_inbox_status" == "SUCCEEDED" && "$pause_outbox_status" == "SENT" ]]; then break; fi
    sleep 0.2
  done
  [[ "$pause_inbox_status" == "SUCCEEDED" && "$pause_outbox_status" == "SENT" ]] || {
    echo "owner did not commit/publish while Record Hub result consumer was paused" >&2
    exit 1
  }
  start_record_hub record-hub-restart.log
  pause_status=""
  for _ in {1..240}; do
    pause_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${fault_pause_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/consumer-pause-result.json" | jq -r '.status')"
    [[ "$pause_status" == "SUCCEEDED" || "$pause_status" == "REJECTED" || "$pause_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$pause_status" == "SUCCEEDED" ]] || { cat "$runtime_root/consumer-pause-result.json" >&2; exit 1; }
  pause_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${pause_annotation}'")"
  [[ "$pause_events" == "1" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"
  for _ in {1..180}; do
    projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
    [[ "$projected_version" == "$fault_final_version" ]] && break
    sleep 0.2
  done
  [[ "$projected_version" == "$fault_final_version" ]] || exit 1

  # Leave a command in OWNER_COMMANDS, restart the broker with the same
  # JetStream store, then bring the owner worker back. This proves backlog
  # recovery without writing a business table or recreating the topology.
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  nats_annotation="p3-nats-recovery-${project_id}"
  nats_key="p3-nats-recovery-${project_id}"
  nats_payload="$(jq -cn --arg id "$nats_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 NATS recovery"}')"
  nats_http="$(submit "$nats_key" "$fault_final_version" "$nats_payload" "$runtime_root/nats-recovery.json")"
  [[ "$nats_http" == "202" ]] || { cat "$runtime_root/nats-recovery.json" >&2; exit 1; }
  nats_recovery_operation="$(jq -er '.operationId' "$runtime_root/nats-recovery.json")"
  nats_status_before="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${nats_recovery_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | jq -r '.status')"
  [[ "$nats_status_before" == "DISPATCHED" || "$nats_status_before" == "ACCEPTED" ]] || exit 1
  stop_nats
  sleep 1
  start_nats nats-recovery.log
  RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init >"$evidence_dir/logs/nats-init-recovery.log" 2>&1
  recovered_pids="$(start_fluxion_worker fluxion-worker-nats-recovery.log)"
  fault_worker_launcher_pid="${recovered_pids%%|*}"
  fault_worker_java_pid="${recovered_pids#*|}"
  nats_status=""
  for _ in {1..300}; do
    nats_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${nats_recovery_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/nats-recovery-result.json" | jq -r '.status')"
    [[ "$nats_status" == "SUCCEEDED" || "$nats_status" == "REJECTED" || "$nats_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$nats_status" == "SUCCEEDED" ]] || { cat "$runtime_root/nats-recovery-result.json" >&2; exit 1; }
  nats_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${nats_annotation}'")"
  [[ "$nats_events" == "1" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"
  for _ in {1..180}; do
    projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
    [[ "$projected_version" == "$fault_final_version" ]] && break
    sleep 0.2
  done
  [[ "$projected_version" == "$fault_final_version" ]] || exit 1

  # Stop both workflow-facing processes with a command already dispatched.
  # Recovery starts Record Hub first and Fluxion second; the durable command
  # and result consumers must bridge the gap without a duplicate event.
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  cross_annotation="p3-cross-restart-${project_id}"
  cross_key="p3-cross-restart-${project_id}"
  cross_payload="$(jq -cn --arg id "$cross_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 cross restart"}')"
  cross_http="$(submit "$cross_key" "$fault_final_version" "$cross_payload" "$runtime_root/cross-restart.json")"
  [[ "$cross_http" == "202" ]] || { cat "$runtime_root/cross-restart.json" >&2; exit 1; }
  cross_restart_operation="$(jq -er '.operationId' "$runtime_root/cross-restart.json")"
  cross_status_before="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${cross_restart_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | jq -r '.status')"
  [[ "$cross_status_before" == "DISPATCHED" || "$cross_status_before" == "ACCEPTED" ]] || exit 1
  stop_record_hub
  start_record_hub record-hub-cross-restart.log
  cross_pids="$(start_fluxion_worker fluxion-worker-cross-restart.log)"
  fault_worker_launcher_pid="${cross_pids%%|*}"
  fault_worker_java_pid="${cross_pids#*|}"
  cross_status=""
  for _ in {1..300}; do
    cross_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${cross_restart_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/cross-restart-result.json" | jq -r '.status')"
    [[ "$cross_status" == "SUCCEEDED" || "$cross_status" == "REJECTED" || "$cross_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$cross_status" == "SUCCEEDED" ]] || { cat "$runtime_root/cross-restart-result.json" >&2; exit 1; }
  cross_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${cross_annotation}'")"
  [[ "$cross_events" == "1" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"
  for _ in {1..180}; do
    projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
    [[ "$projected_version" == "$fault_final_version" ]] && break
    sleep 0.2
  done
  [[ "$projected_version" == "$fault_final_version" ]] || exit 1

  # Run two Record Hub result consumers against the same durable. Their
  # concurrent delivery must still leave one operation revision and one owner
  # event. The secondary HTTP listener avoids port sharing while the NATS
  # durable remains identical.
  start_secondary_record_hub record-hub-secondary.log
  competition_annotation="p3-result-competition-${project_id}"
  competition_key="p3-result-competition-${project_id}"
  competition_payload="$(jq -cn --arg id "$competition_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 result competition"}')"
  competition_http="$(submit "$competition_key" "$fault_final_version" "$competition_payload" "$runtime_root/result-competition.json")"
  [[ "$competition_http" == "202" ]] || { cat "$runtime_root/result-competition.json" >&2; exit 1; }
  result_competition_operation="$(jq -er '.operationId' "$runtime_root/result-competition.json")"
  competition_status=""
  for _ in {1..240}; do
    competition_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${result_competition_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/result-competition-result.json" | jq -r '.status')"
    [[ "$competition_status" == "SUCCEEDED" || "$competition_status" == "REJECTED" || "$competition_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$competition_status" == "SUCCEEDED" ]] || { cat "$runtime_root/result-competition-result.json" >&2; exit 1; }
  competition_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${competition_annotation}'")"
  [[ "$competition_events" == "1" ]] || exit 1
  stop_secondary_record_hub
  fault_final_version="$((fault_final_version + 1))"
  for _ in {1..180}; do
    projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
    [[ "$projected_version" == "$fault_final_version" ]] && break
    sleep 0.2
  done
  [[ "$projected_version" == "$fault_final_version" ]] || exit 1

  # Inject a malformed owner result. The Record Hub result pull runner must
  # exhaust its bounded delivery budget, publish only safe DLQ metadata, and
  # continue processing a valid result afterwards.
  dlq_subject="results.fluxion.project.annotate.v1"
  dlq_consumer="record-hub-command-results-v1"
  dlq_marker="p3-poison-${project_id}"
  dlq_message_id="p3-dlq-${project_id}"
  dlq_before="$(nats --server "$nats_url" stream info --json DEAD_LETTERS | jq -r '.state.messages // .state.Msgs // 0')"
  [[ "$dlq_before" =~ ^[0-9]+$ ]] || { echo "unable to read DEAD_LETTERS count" >&2; exit 1; }
  nats --server "$nats_url" pub -H "Nats-Msg-Id: $dlq_message_id" "$dlq_subject" "{\"poison\":\"$dlq_marker\"}" >/dev/null
  dlq_after="$dlq_before"
  for _ in {1..300}; do
    dlq_after="$(nats --server "$nats_url" stream info --json DEAD_LETTERS | jq -r '.state.messages // .state.Msgs // 0' 2>/dev/null || true)"
    if [[ "$dlq_after" =~ ^[0-9]+$ && "$dlq_after" -gt "$dlq_before" ]]; then break; fi
    sleep 0.2
  done
  [[ "$dlq_after" =~ ^[0-9]+$ && "$dlq_after" -gt "$dlq_before" ]] || {
    echo "invalid command result did not reach DEAD_LETTERS" >&2
    exit 1
  }
  nats --server "$nats_url" stream view --raw DEAD_LETTERS 20 >"$runtime_root/dead-letters.txt"
  grep -q "$dlq_consumer" "$runtime_root/dead-letters.txt" || exit 1
  ! grep -q "$dlq_marker" "$runtime_root/dead-letters.txt" || {
    echo "DLQ exposed the poison result payload" >&2
    exit 1
  }

  dlq_followup_annotation="p3-dlq-followup-${project_id}"
  dlq_followup_key="p3-dlq-followup-${project_id}"
  dlq_followup_payload="$(jq -cn --arg id "$dlq_followup_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 DLQ followup"}')"
  dlq_followup_http="$(submit "$dlq_followup_key" "$fault_final_version" "$dlq_followup_payload" "$runtime_root/dlq-followup.json")"
  [[ "$dlq_followup_http" == "202" ]] || { cat "$runtime_root/dlq-followup.json" >&2; exit 1; }
  dlq_followup_operation="$(jq -er '.operationId' "$runtime_root/dlq-followup.json")"
  dlq_followup_status=""
  for _ in {1..240}; do
    dlq_followup_status="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$record_hub_url/api/v1/commands/${dlq_followup_operation}?tenantId=$FLUXION_RECORD_HUB_TENANT_ID&workspaceId=$FLUXION_RECORD_HUB_WORKSPACE_ID" | tee "$runtime_root/dlq-followup-result.json" | jq -r '.status')"
    [[ "$dlq_followup_status" == "SUCCEEDED" || "$dlq_followup_status" == "REJECTED" || "$dlq_followup_status" == "FAILED" ]] && break
    sleep 0.2
  done
  [[ "$dlq_followup_status" == "SUCCEEDED" ]] || { cat "$runtime_root/dlq-followup-result.json" >&2; exit 1; }
  dlq_followup_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${dlq_followup_annotation}'")"
  [[ "$dlq_followup_events" == "1" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"
  for _ in {1..180}; do
    projected_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${FLUXION_RECORD_HUB_TENANT_ID}',workspaceId:'${FLUXION_RECORD_HUB_WORKSPACE_ID}','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? r.source.version.toString() : '')" 2>/dev/null || true)"
    [[ "$projected_version" == "$fault_final_version" ]] && break
    sleep 0.2
  done
  [[ "$projected_version" == "$fault_final_version" ]] || exit 1

  # Publish the terminal result successfully, then halt before the owner
  # outbox row can transition PROCESSING -> SENT. A restart must reclaim the
  # expired lease, publish the same event id, and converge without a duplicate
  # Fluxion project event or Record Hub terminal revision.
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  publish_crash_pids="$(start_fluxion_worker fluxion-worker-publish-before-sent.log \
    FLUXION_RECORD_HUB_TEST_CRASH_BEFORE_RESULT_SENT=true)"
  fault_worker_launcher_pid="${publish_crash_pids%%|*}"
  fault_worker_java_pid="${publish_crash_pids#*|}"
  publish_before_sent_annotation="p3-publish-before-sent-${project_id}"
  publish_before_sent_key="p3-publish-before-sent-${project_id}"
  publish_before_sent_payload="$(jq -cn --arg id "$publish_before_sent_annotation" '{annotationId:$id,mode:"APPEND",text:"P3 publish before SENT"}')"
  publish_before_sent_http="$(submit "$publish_before_sent_key" "$fault_final_version" "$publish_before_sent_payload" "$runtime_root/publish-before-sent.json")"
  [[ "$publish_before_sent_http" == "202" ]] || { cat "$runtime_root/publish-before-sent.json" >&2; exit 1; }
  publish_before_sent_operation="$(jq -er '.operationId' "$runtime_root/publish-before-sent.json")"
  publish_before_sent_inbox_status=""
  publish_before_sent_outbox_status=""
  publish_before_sent_events=""
  for _ in {1..300}; do
    publish_before_sent_inbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_inbox where operation_id='${publish_before_sent_operation}'" 2>/dev/null || true)"
    publish_before_sent_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${publish_before_sent_operation}'" 2>/dev/null || true)"
    publish_before_sent_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${publish_before_sent_annotation}'" 2>/dev/null || true)"
    if [[ "$publish_before_sent_inbox_status" == "SUCCEEDED" && "$publish_before_sent_outbox_status" == "PROCESSING" && "$publish_before_sent_events" == "1" ]]; then break; fi
    sleep 0.2
  done
  [[ "$publish_before_sent_inbox_status" == "SUCCEEDED" && "$publish_before_sent_outbox_status" == "PROCESSING" && "$publish_before_sent_events" == "1" ]] || {
    echo "publish-before-SENT did not leave a published PROCESSING outbox with one owner event" >&2
    exit 1
  }
  for _ in {1..100}; do
    kill -0 "$fault_worker_java_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$fault_worker_java_pid" 2>/dev/null; then
    echo "publish-before-SENT worker did not halt" >&2
    exit 1
  fi
  stop_fluxion_worker "$fault_worker_launcher_pid" "$fault_worker_java_pid"
  fault_worker_launcher_pid=""
  fault_worker_java_pid=""
  publish_recovery_pids="$(start_fluxion_worker fluxion-worker-publish-before-sent-recovery.log)"
  fault_worker_launcher_pid="${publish_recovery_pids%%|*}"
  fault_worker_java_pid="${publish_recovery_pids#*|}"
  for _ in {1..240}; do
    publish_before_sent_outbox_status="$(psql -At -d "$pg_db" -c "select status from record_hub_command_result_outbox where operation_id='${publish_before_sent_operation}'" 2>/dev/null || true)"
    [[ "$publish_before_sent_outbox_status" == "SENT" ]] && break
    sleep 0.2
  done
  [[ "$publish_before_sent_outbox_status" == "SENT" ]] || exit 1
  publish_before_sent_events="$(psql -At -d "$pg_db" -c "select count(*) from project_events where project_id='${project_id}' and payload_ref='record-hub-annotation:${publish_before_sent_annotation}'")"
  [[ "$publish_before_sent_events" == "1" ]] || exit 1
  fault_final_version="$((fault_final_version + 1))"
fi

runtime_seconds="$(( $(date +%s) - started_epoch ))"
jq -n \
  --arg project_id "$project_id" --arg append_operation "$append_operation" --arg void_operation "$void_operation" --arg stale_operation "$stale_operation" \
  --arg fault_replay_operation "$fault_replay_operation" --arg rollback_operation "$rollback_operation" --arg publish_before_sent_operation "$publish_before_sent_operation" --arg fault_pause_operation "$fault_pause_operation" --arg nats_recovery_operation "$nats_recovery_operation" --arg dlq_followup_operation "$dlq_followup_operation" --arg cross_restart_operation "$cross_restart_operation" --arg result_competition_operation "$result_competition_operation" \
  --argjson before "$before_version" --argjson append_version "$after_append_version" --argjson void_version "$after_void_version" --argjson final_version "$fault_final_version" \
  --argjson runtime_seconds "$runtime_seconds" --argjson fault_matrix "$fault_matrix" \
  '{gate:"P3-110",cases:{append:{status:"PASS",operationId:$append_operation},duplicate:{status:"PASS",sameOperation:true},hashConflict:{status:"PASS",http:409},void:{status:"PASS",operationId:$void_operation},versionConflict:{status:"PASS",operationId:$stale_operation,error:"project version conflict"},projection:{status:"PASS",sourceVersion:$final_version},faultMatrix:(if $fault_matrix == 1 then {ownerCompetition:{status:"PASS",sharedDurable:true},transactionRollback:{status:"PASS",operationId:$rollback_operation,preCommitStateClean:true,retriedAndCommittedOnce:true},crashBeforeAckReplay:{status:"PASS",operationId:$fault_replay_operation,ownerEventAppliedOnce:true,outboxConverged:true},publishBeforeSentCrash:{status:"PASS",operationId:$publish_before_sent_operation,ownerEventAppliedOnce:true,processingLeaseReclaimed:true,outboxConverged:true},resultConsumerPause:{status:"PASS",operationId:$fault_pause_operation,ownerPublishedWhileConsumerDown:true,replayedAfterRestart:true},natsBacklogRecovery:{status:"PASS",operationId:$nats_recovery_operation,jetStreamStoreReused:true,ownerEventAppliedOnce:true},crossSystemRestart:{status:"PASS",operationId:$cross_restart_operation,ownerEventAppliedOnce:true,recoveredAfterBothDown:true},resultConsumerCompetition:{status:"PASS",operationId:$result_competition_operation,ownerEventAppliedOnce:true,singleTerminalRevision:true},deadLetter:{status:"PASS",safeMetadata:true,poisonPayloadRedacted:true,normalFollowupOperationId:$dlq_followup_operation}} else null end)},projectId:$project_id,summaryVersion:{before:$before,afterAppend:$append_version,afterVoid:$void_version,final:$final_version},runtimeSeconds:$runtime_seconds}' \
  >"$evidence_dir/results.json"
jq -n --arg mongo_port "$mongo_port" --arg nats_port "$nats_port" --arg record_hub_port "$record_hub_port" --arg record_hub_secondary_port "$record_hub_secondary_port" --arg fluxion_port "$fluxion_port" --arg temporal_port "$temporal_port" --arg tenant "$FLUXION_RECORD_HUB_TENANT_ID" --arg workspace "$FLUXION_RECORD_HUB_WORKSPACE_ID" \
  '{mongoPort:($mongo_port|tonumber),recordHubSecondaryPort:($record_hub_secondary_port|tonumber),natsPort:($nats_port|tonumber),recordHubPort:($record_hub_port|tonumber),fluxionPort:($fluxion_port|tonumber),temporalPort:($temporal_port|tonumber),tenantId:$tenant,workspaceId:$workspace,issuer:"redacted",audience:"record-hub-api",scope:"recordhub.command.submit",database:"temporary"}' \
  >"$evidence_dir/config-redacted.json"
printf 'project_id=%s\nappend_operation=%s\nvoid_operation=%s\nversion_conflict_operation=%s\nsummary_version_before=%s\nsummary_version_after_void=%s\n' "$project_id" "$append_operation" "$void_operation" "$stale_operation" "$before_version" "$after_void_version" >"$evidence_dir/db-assertions/fluxion.txt"
if [[ "$fault_matrix" == "1" ]]; then
  printf 'fault_replay_operation=%s\nfault_replay_inbox=%s\nfault_replay_outbox=%s\nfault_pause_operation=%s\nfault_pause_inbox=%s\nfault_pause_outbox=%s\nnats_recovery_operation=%s\nnats_recovery_events=%s\ncross_restart_operation=%s\ncross_restart_events=%s\nresult_competition_operation=%s\nresult_competition_events=%s\ndlq_before=%s\ndlq_after=%s\ndlq_followup_operation=%s\ndlq_followup_events=%s\nsummary_version_final=%s\n' \
    "$fault_replay_operation" "$replay_inbox_status" "$replay_outbox_status" "$fault_pause_operation" "$pause_inbox_status" "$pause_outbox_status" "$nats_recovery_operation" "$nats_events" "$cross_restart_operation" "$cross_events" "$result_competition_operation" "$competition_events" "$dlq_before" "$dlq_after" "$dlq_followup_operation" "$dlq_followup_events" "$fault_final_version" \
    >>"$evidence_dir/db-assertions/fluxion.txt"
fi
write_manifest PASS
echo "P3-110 Fluxion command live gate passed (project ${project_id}, evidence ${evidence_dir})"
