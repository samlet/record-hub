#!/usr/bin/env bash
set -euo pipefail

# P3-110: real Record Hub -> NATS -> Fluxion owner command gate.  The target
# project is created through Fluxion's HTTP API; this script never writes the
# owner PostgreSQL business tables directly.  PostgreSQL/Mongo reads below are
# bounded assertions only.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
cd "$record_hub_root"

for command in curl createdb dropdb go jq lsof mongod mongosh nats-server nc openssl psql temporal uuidgen; do
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
temporal_port="${RECORD_HUB_P3_TEMPORAL_PORT:-17233}"
temporal_ui_port="${RECORD_HUB_P3_TEMPORAL_UI_PORT:-18233}"
fluxion_port="${RECORD_HUB_P3_FLUXION_PORT:-18091}"

for port in "$mongo_port" "$nats_port" "$nats_monitor_port" "$workload_port" "$record_hub_port" "$temporal_port" "$temporal_ui_port" "$fluxion_port"; do
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
  for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${pids[@]}"; do
    [[ -n "$pid" ]] && kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in {1..100}; do
    local running=0
    for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${pids[@]}"; do
      [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && running=1
    done
    [[ "$running" == "0" ]] && break
    sleep 0.1
  done
  for pid in "${fluxion_api_java_pid:-}" "${fluxion_worker_java_pid:-}" "${pids[@]}"; do
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
nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$runtime_root/nats" -n record-hub-p3-fluxion >"$evidence_dir/logs/nats.log" 2>&1 &
pids+=("$!")
wait_http "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" NATS
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
export RECORD_HUB_MONGODB_URI="$mongo_uri"
export RECORD_HUB_MONGODB_DATABASE=record_hub_p3
export RECORD_HUB_NATS_URL="$nats_url"
export RECORD_HUB_PROJECTION_WORKSPACE_ID=workspace-p3-fluxion
export RECORD_HUB_OIDC_ISSUER="$workload_issuer"
export RECORD_HUB_OIDC_AUDIENCE="$workload_audience"
export RECORD_HUB_OIDC_PRINCIPAL_KIND=service
export RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER=true
export RECORD_HUB_COMMAND_POLICIES="$(jq -c --arg issuer "$workload_issuer" --arg audience "$workload_audience" '.policies | map(.issuer=$issuer | .audience=$audience)' deploy/local/p3/fluxion-project-annotate-policy.json)"
"$runtime_root/record-hub" serve >"$evidence_dir/logs/record-hub.log" 2>&1 &
pids+=("$!")
record_hub_url="http://127.0.0.1:${record_hub_port}"
wait_http "$record_hub_url/healthz" "Record Hub API"
curl --silent --show-error --fail "$record_hub_url/readyz" | jq -e '.status == "ready"' >/dev/null

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
(
  cd "$fluxion_root/server"
  exec ./gradlew --no-daemon -q runWorker
) >"$evidence_dir/logs/fluxion-worker.log" 2>&1 &
fluxion_worker_launcher_pid="$!"
pids+=("$fluxion_worker_launcher_pid")
for _ in {1..180}; do
  fluxion_api_java_pid="$(pgrep -f 'fluxion\.ApiKt' | head -1 || true)"
  fluxion_worker_java_pid="$(pgrep -f 'fluxion\.WorkerKt' | head -1 || true)"
  [[ -n "$fluxion_worker_java_pid" ]] && break
  sleep 0.2
done
[[ -n "$fluxion_worker_java_pid" ]] || { tail -80 "$evidence_dir/logs/fluxion-worker.log" >&2; exit 1; }

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

runtime_seconds="$(( $(date +%s) - started_epoch ))"
jq -n \
  --arg project_id "$project_id" --arg append_operation "$append_operation" --arg void_operation "$void_operation" --arg stale_operation "$stale_operation" \
  --argjson before "$before_version" --argjson append_version "$after_append_version" --argjson void_version "$after_void_version" \
  --argjson runtime_seconds "$runtime_seconds" \
  '{gate:"P3-110",cases:{append:{status:"PASS",operationId:$append_operation},duplicate:{status:"PASS",sameOperation:true},hashConflict:{status:"PASS",http:409},void:{status:"PASS",operationId:$void_operation},versionConflict:{status:"PASS",operationId:$stale_operation,error:"project version conflict"},projection:{status:"PASS",sourceVersion:$void_version}},projectId:$project_id,summaryVersion:{before:$before,afterAppend:$append_version,afterVoid:$void_version},runtimeSeconds:$runtime_seconds}' \
  >"$evidence_dir/results.json"
jq -n --arg mongo_port "$mongo_port" --arg nats_port "$nats_port" --arg record_hub_port "$record_hub_port" --arg fluxion_port "$fluxion_port" --arg temporal_port "$temporal_port" --arg tenant "$FLUXION_RECORD_HUB_TENANT_ID" --arg workspace "$FLUXION_RECORD_HUB_WORKSPACE_ID" \
  '{mongoPort:($mongo_port|tonumber),natsPort:($nats_port|tonumber),recordHubPort:($record_hub_port|tonumber),fluxionPort:($fluxion_port|tonumber),temporalPort:($temporal_port|tonumber),tenantId:$tenant,workspaceId:$workspace,issuer:"redacted",audience:"record-hub-api",scope:"recordhub.command.submit",database:"temporary"}' \
  >"$evidence_dir/config-redacted.json"
printf 'project_id=%s\nappend_operation=%s\nvoid_operation=%s\nversion_conflict_operation=%s\nsummary_version_before=%s\nsummary_version_after_void=%s\n' "$project_id" "$append_operation" "$void_operation" "$stale_operation" "$before_version" "$after_void_version" >"$evidence_dir/db-assertions/fluxion.txt"
write_manifest PASS
echo "P3-110 Fluxion command live gate passed (project ${project_id}, evidence ${evidence_dir})"
