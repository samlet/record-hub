#!/usr/bin/env bash
set -Eeuo pipefail

# P3-400/P3-401: native four-owner topology supervisor.  This script is
# intentionally opt-in. It never stops a process it did not start and never
# writes business rows directly; migrations and fixtures are applied by the
# real applications after they start.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"

if [[ "${RECORD_HUB_P3_FOUR_OWNER_LIVE:-0}" != "1" ]]; then
  echo "P3-400 four-owner topology: SKIPPED (set RECORD_HUB_P3_FOUR_OWNER_LIVE=1)"
  exit 0
fi

evidence_dir="${RECORD_HUB_P3_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-four-owner-evidence.XXXXXX")}"
runtime_root="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-four-owner-runtime.XXXXXX")"
fixture_dir="$evidence_dir/fixtures"
mkdir -p "$evidence_dir/logs" "$evidence_dir/metrics" "$evidence_dir/db-assertions" "$fixture_dir"

mongo_port="${RECORD_HUB_P3_MONGO_PORT:-37018}"
nats_port="${RECORD_HUB_P3_NATS_PORT:-14223}"
nats_monitor_port="${RECORD_HUB_P3_NATS_MONITOR_PORT:-18223}"
dex_port="${RECORD_HUB_P3_DEX_PORT:-15566}"
dex_telemetry_port="${RECORD_HUB_P3_DEX_TELEMETRY_PORT:-15568}"
workload_port="${RECORD_HUB_P3_WORKLOAD_PORT:-15557}"
temporal_port="${RECORD_HUB_P3_TEMPORAL_PORT:-17233}"
temporal_ui_port="${RECORD_HUB_P3_TEMPORAL_UI_PORT:-18233}"
conductor_port="${RECORD_HUB_P3_CONDUCTOR_PORT:-18080}"
record_hub_port="${RECORD_HUB_P3_RECORD_HUB_PORT:-18081}"
approver_api_port="${RECORD_HUB_P3_APPROVER_API_PORT:-18090}"
approver_worker_port="${RECORD_HUB_P3_APPROVER_WORKER_PORT:-18091}"
fluxion_port="${RECORD_HUB_P3_FLUXION_PORT:-18092}"
bids_api_port="${RECORD_HUB_P3_BIDS_API_PORT:-18093}"
pg_user="${RECORD_HUB_P3_PG_USER:-$(id -un)}"
run_id="$(date +%s)-$$"
mongo_database="record_hub_p3_${run_id}"
approver_database="record_hub_p3_approver_${run_id}"
fluxion_database="record_hub_p3_fluxion_${run_id}"
bids_database="record_hub_p3_bids_${run_id}"
mongo_replica_set="record-hub-p3-rs"
mongo_uri="mongodb://127.0.0.1:${mongo_port}/${mongo_database}?replicaSet=${mongo_replica_set}&directConnection=true"
nats_url="nats://127.0.0.1:${nats_port}"
workload_issuer="http://127.0.0.1:${workload_port}/workload"
workload_audience="record-hub-api"
temporal_address="127.0.0.1:${temporal_port}"
conductor_url="http://127.0.0.1:${conductor_port}/api"
created_databases=()
pids=()
fluxion_worker_launcher_pid=""
fluxion_worker_java_pid=""

write_manifest() {
  local status="$1" reason="${2:-}"
  jq -n \
    --arg status "$status" --arg reason "$reason" \
    --arg startedAt "${started_at:-}" --arg finishedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg host "$(uname -srm)" --arg recordHubCommit "$(git -C "$root_dir" rev-parse HEAD 2>/dev/null || true)" \
    --arg approverCommit "$(git -C "$approver_root" rev-parse HEAD 2>/dev/null || true)" \
    --arg fluxionCommit "$(git -C "$fluxion_root" rev-parse HEAD 2>/dev/null || true)" \
    --arg bidsCommit "$(git -C "$bids_root" rev-parse HEAD 2>/dev/null || true)" \
    --arg fixtureManifest "$fixture_dir/manifest.json" \
    --argjson ports "$(jq -n \
      --arg mongo "$mongo_port" --arg nats "$nats_port" --arg dex "$dex_port" --arg workload "$workload_port" \
      --arg temporal "$temporal_port" --arg conductor "$conductor_port" --arg recordHub "$record_hub_port" \
      --arg approverApi "$approver_api_port" --arg approverWorker "$approver_worker_port" --arg fluxion "$fluxion_port" --arg bidsApi "$bids_api_port" \
      '{mongo:($mongo|tonumber),nats:($nats|tonumber),dex:($dex|tonumber),workloadIssuer:($workload|tonumber),temporal:($temporal|tonumber),conductor:($conductor|tonumber),recordHub:($recordHub|tonumber),approverApi:($approverApi|tonumber),approverWorker:($approverWorker|tonumber),fluxion:($fluxion|tonumber),bidsApi:($bidsApi|tonumber)}')" \
    '{gate:"P3-400/P3-401",status:$status,reason:$reason,startedAt:$startedAt,finishedAt:$finishedAt,host:$host,commits:{recordHub:$recordHubCommit,approver:$approverCommit,fluxion:$fluxionCommit,bids:$bidsCommit},fixtureManifest:$fixtureManifest,ports:$ports}' \
    >"$evidence_dir/manifest.json"
}

skip_gate() {
  local reason="$1"
  write_manifest SKIPPED "$reason"
  echo "P3-400/P3-401: SKIPPED ($reason)"
  echo "evidence: $evidence_dir"
  exit 0
}

cleanup() {
  local status=$?
  set +e
  if [[ "$status" != "0" ]]; then
    write_manifest FAIL "topology process or readiness check failed"
  fi
  for pid in "$fluxion_worker_java_pid" "$fluxion_worker_launcher_pid" "${pids[@]}"; do
    [[ -n "$pid" ]] && kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in {1..100}; do
    local running=0
    for pid in "$fluxion_worker_java_pid" "$fluxion_worker_launcher_pid" "${pids[@]}"; do
      [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && running=1
    done
    [[ "$running" == "0" ]] && break
    sleep 0.1
  done
  for pid in "$fluxion_worker_java_pid" "$fluxion_worker_launcher_pid" "${pids[@]}"; do
    [[ -n "$pid" ]] && kill -KILL "$pid" 2>/dev/null || true
    [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  done
  for db_name in "${created_databases[@]}"; do
    dropdb --if-exists -U "$pg_user" "$db_name" >/dev/null 2>&1 || true
  done
  if [[ "$status" == "0" ]]; then
    rm -rf -- "$runtime_root"
  else
    echo "P3-400 failed; runtime/logs preserved at $runtime_root" >&2
    echo "evidence: $evidence_dir" >&2
  fi
}
trap cleanup EXIT HUP INT TERM

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
write_manifest IN_PROGRESS

required_commands=(curl createdb dropdb go htpasswd java jq lsof mongod mongosh nats-server nc openssl psql temporal conductor mvn shasum)
for command in "${required_commands[@]}"; do
  command -v "$command" >/dev/null || skip_gate "missing command: $command"
done
[[ -d "$approver_root" && -d "$fluxion_root" && -d "$bids_root" ]] || skip_gate "owner repository path is missing"
[[ -x "$fluxion_root/server/gradlew" ]] || skip_gate "Fluxion Gradle wrapper is missing"

port_list=("$mongo_port" "$nats_port" "$nats_monitor_port" "$dex_port" "$dex_telemetry_port" "$workload_port" "$temporal_port" "$temporal_ui_port" "$conductor_port" "$record_hub_port" "$approver_api_port" "$approver_worker_port" "$fluxion_port" "$bids_api_port")
for port in "${port_list[@]}"; do
  lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && skip_gate "TCP port $port is already in use"
done

minio_endpoint="${MINIO_ENDPOINT:-127.0.0.1:9000}"
[[ "$minio_endpoint" == http://* || "$minio_endpoint" == https://* ]] || minio_endpoint="http://$minio_endpoint"
curl --silent --show-error --fail "$minio_endpoint/minio/health/live" >/dev/null 2>&1 || skip_gate "Bids API requires a reachable native MinIO endpoint ($minio_endpoint)"

RECORD_HUB_P3_FIXTURE_DIR="$fixture_dir" "$root_dir/scripts/bootstrap-p3-fixtures.sh" >"$evidence_dir/fixture-bootstrap.log"
policy_json="$(jq -c --arg issuer "$workload_issuer" --arg audience "$workload_audience" '. | map(.issuer=$issuer | .audience=$audience)' "$fixture_dir/command-policies.json")"

echo "P3-400 building isolated owner runtimes"
(cd "$approver_root" && mvn -q -DskipTests package)
(cd "$fluxion_root/server" && ./gradlew -q installDist)
(cd "$bids_root/backend" && go build -trimpath -o "$runtime_root/bids-api" ./cmd/api && go build -trimpath -o "$runtime_root/bids-worker" ./cmd/worker)
(cd "$root_dir" && go build -trimpath -o "$runtime_root/record-hub" ./server/cmd/record-hub && go build -trimpath -o "$runtime_root/workload-issuer" ./tools/workload-issuer)
approver_api_jar="$(find "$approver_root/approver-api/target" -maxdepth 1 -type f -name 'approver-api-*.jar' ! -name '*-plain.jar' | head -1)"
approver_worker_jar="$(find "$approver_root/approver-worker/target" -maxdepth 1 -type f -name 'approver-worker-*.jar' ! -name '*-plain.jar' | head -1)"
fluxion_bin="$fluxion_root/server/build/install/fluxion-server/bin/fluxion-server"
[[ -f "$approver_api_jar" && -f "$approver_worker_jar" && -x "$fluxion_bin" ]] || { write_manifest FAIL "owner build artifacts were not created"; exit 1; }

wait_http() {
  local url="$1" label="$2"
  for _ in {1..240}; do
    curl --silent --show-error --fail "$url" >/dev/null 2>&1 && return 0
    sleep 0.25
  done
  echo "$label did not become ready: $url" >&2
  return 1
}
wait_tcp() {
  local host="$1" port="$2" label="$3"
  for _ in {1..240}; do
    nc -z "$host" "$port" >/dev/null 2>&1 && return 0
    sleep 0.25
  done
  echo "$label did not become ready: $host:$port" >&2
  return 1
}
start_background() {
  local log_file="$1"
  shift
  "$@" >"$evidence_dir/logs/$log_file" 2>&1 &
  pids+=("$!")
}

mkdir -p "$runtime_root/mongo" "$runtime_root/nats"
mongod --bind_ip 127.0.0.1 --port "$mongo_port" --dbpath "$runtime_root/mongo" --replSet "$mongo_replica_set" --nounixsocket --logpath "$evidence_dir/logs/mongod.log" --logappend &
pids+=("$!")
wait_tcp 127.0.0.1 "$mongo_port" MongoDB
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval "rs.initiate({_id:'${mongo_replica_set}',members:[{_id:0,host:'127.0.0.1:${mongo_port}'}]})" >/dev/null
for _ in {1..180}; do
  mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null 2>&1 && break
  sleep 0.2
done
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null

start_background nats.log nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$runtime_root/nats" -n record-hub-p3-four-owner
wait_http "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" NATS
(cd "$root_dir" && RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init) >"$evidence_dir/logs/nats-init.log" 2>&1

export DEX_ISSUER="http://127.0.0.1:${dex_port}/dex"
export DEX_ISOLATED_DATABASE="$runtime_root/dex.db"
export DEX_ISOLATED_WEB_ADDRESS="127.0.0.1:${dex_port}"
export DEX_ISOLATED_TELEMETRY_ADDRESS="127.0.0.1:${dex_telemetry_port}"
export DEX_RECORD_HUB_API_REDIRECT="http://127.0.0.1:${record_hub_port}/auth/callback"
export DEX_LOCAL_PASSWORD_HASH="$(htpasswd -bnBC 10 '' "$(openssl rand -base64 24)" | cut -d: -f2)"
export DEX_APPROVER_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_FLUXION_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_BIDS_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_RECORD_HUB_WEB_SECRET="$(openssl rand -hex 32)"
sed \
  -e "s|\${DEX_ISSUER}|${DEX_ISSUER}|g" \
  -e "s|\${DEX_ISOLATED_DATABASE}|${DEX_ISOLATED_DATABASE}|g" \
  -e "s|\${DEX_ISOLATED_WEB_ADDRESS}|${DEX_ISOLATED_WEB_ADDRESS}|g" \
  -e "s|\${DEX_ISOLATED_TELEMETRY_ADDRESS}|${DEX_ISOLATED_TELEMETRY_ADDRESS}|g" \
  -e "s|\${DEX_RECORD_HUB_API_REDIRECT}|${DEX_RECORD_HUB_API_REDIRECT}|g" \
  "$root_dir/deploy/local/isolated/dex.yaml" >"$runtime_root/dex.yaml"
start_background dex.log dex serve "$runtime_root/dex.yaml"
wait_http "$DEX_ISSUER/.well-known/openid-configuration" Dex

export RECORD_HUB_WORKLOAD_ISSUER_ADDRESS="127.0.0.1:${workload_port}"
export RECORD_HUB_WORKLOAD_ISSUER="$workload_issuer"
export RECORD_HUB_WORKLOAD_AUDIENCE="$workload_audience"
approver_workload_secret="$(openssl rand -hex 32)"
fluxion_workload_secret="$(openssl rand -hex 32)"
bids_workload_secret="$(openssl rand -hex 32)"
export RECORD_HUB_WORKLOAD_CLIENTS="$(jq -cn --arg scope recordhub.command.submit --arg approver "$approver_workload_secret" --arg fluxion "$fluxion_workload_secret" --arg bids "$bids_workload_secret" '[{id:"approver",secret:$approver,scopes:[$scope]},{id:"fluxion",secret:$fluxion,scopes:[$scope]},{id:"bids",secret:$bids,scopes:[$scope]}]')"
start_background workload-issuer.log "$runtime_root/workload-issuer"
wait_http "$workload_issuer/.well-known/openid-configuration" "workload issuer"

start_background temporal.log temporal server start-dev --headless --ip 127.0.0.1 --port "$temporal_port" --ui-port "$temporal_ui_port" --db-filename "$runtime_root/temporal.sqlite" --namespace p3-local
wait_tcp 127.0.0.1 "$temporal_port" Temporal
for _ in {1..240}; do
  temporal operator namespace describe --address "$temporal_address" --namespace p3-local --tls=false >/dev/null 2>&1 && break
  sleep 0.25
done
temporal operator namespace describe --address "$temporal_address" --namespace p3-local --tls=false >/dev/null
start_background conductor.log conductor server start --oss --port "$conductor_port" --foreground
wait_http "http://127.0.0.1:${conductor_port}/health" Conductor || wait_http "http://127.0.0.1:${conductor_port}/api/health" Conductor

for db_name in "$approver_database" "$fluxion_database" "$bids_database"; do
  createdb -U "$pg_user" "$db_name"
  created_databases+=("$db_name")
done

record_hub_env=(
  env RECORD_HUB_MODE=all RECORD_HUB_HTTP_ADDRESS="127.0.0.1:${record_hub_port}" RECORD_HUB_SHUTDOWN_TIMEOUT=5s
  RECORD_HUB_MONGODB_URI="$mongo_uri" RECORD_HUB_MONGODB_DATABASE="$mongo_database" RECORD_HUB_NATS_URL="$nats_url"
  RECORD_HUB_OIDC_ISSUER="$workload_issuer" RECORD_HUB_OIDC_AUDIENCE="$workload_audience" RECORD_HUB_OIDC_PRINCIPAL_KIND=service
  RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER=true RECORD_HUB_COMMAND_POLICIES="$policy_json" "$runtime_root/record-hub" serve
)
start_background record-hub.log "${record_hub_env[@]}"
wait_http "http://127.0.0.1:${record_hub_port}/healthz" "Record Hub"
curl --silent --show-error --fail "http://127.0.0.1:${record_hub_port}/readyz" | jq -e '.status == "ready"' >/dev/null

approver_common=(APPROVER_SECURITY_MODE=HEADER APPROVER_DB_URL="jdbc:postgresql://127.0.0.1:5432/${approver_database}" APPROVER_DB_USER="$pg_user" APPROVER_DB_PASSWORD="${APPROVER_DB_PASSWORD:-}" TEMPORAL_TARGET="$temporal_address" TEMPORAL_NAMESPACE=p3-local TEMPORAL_TASK_QUEUE=approver-p3-local APPROVER_RECORD_HUB_NATS_URL="$nats_url" APPROVER_RECORD_HUB_COMMAND_ENABLED=true)
start_background approver-api.log env "${approver_common[@]}" APPROVER_API_PORT="$approver_api_port" java -jar "$approver_api_jar"
start_background approver-worker.log env "${approver_common[@]}" APPROVER_WORKER_PORT="$approver_worker_port" java -jar "$approver_worker_jar"
wait_http "http://127.0.0.1:${approver_api_port}/actuator/health" "Approver API"
wait_http "http://127.0.0.1:${approver_worker_port}/actuator/health" "Approver worker"

fluxion_common=(FLUXION_SERVER_PORT="$fluxion_port" FLUXION_PG_URL="jdbc:postgresql://127.0.0.1:5432/${fluxion_database}" FLUXION_PG_USER="$pg_user" FLUXION_PG_PASSWORD="" FLUXION_TEMPORAL_ADDRESS="$temporal_address" FLUXION_TEMPORAL_NAMESPACE=p3-local FLUXION_TEMPORAL_TASK_QUEUE=fluxion-p3-local FLUXION_RECORD_HUB_NATS_URL="$nats_url" FLUXION_RECORD_HUB_COMMAND_RUNNER_ENABLED=true FLUXION_RECORD_HUB_RESULT_RELAY_ENABLED=true)
start_background fluxion-api.log bash -c "cd '$fluxion_root/server' && exec env ${fluxion_common[*]} ./gradlew --no-daemon -q runApi"
wait_http "http://127.0.0.1:${fluxion_port}/api/health" "Fluxion API"
existing_worker_pids=" $(pgrep -f 'fluxion\.WorkerKt' 2>/dev/null || true) "
(cd "$fluxion_root/server" && env "${fluxion_common[@]}" ./gradlew --no-daemon -q runWorker) >"$evidence_dir/logs/fluxion-worker.log" 2>&1 &
fluxion_worker_launcher_pid="$!"
pids+=("$fluxion_worker_launcher_pid")
for _ in {1..240}; do
  for candidate in $(pgrep -f 'fluxion\.WorkerKt' 2>/dev/null || true); do
    [[ "$existing_worker_pids" == *" $candidate "* ]] || { fluxion_worker_java_pid="$candidate"; break; }
  done
  [[ -n "$fluxion_worker_java_pid" ]] && break
  sleep 0.25
done
[[ -n "$fluxion_worker_java_pid" ]] || { write_manifest FAIL "Fluxion worker did not start"; exit 1; }

bids_common=(DB_DRIVER=postgres DATABASE_DSN="postgres://$pg_user@127.0.0.1:5432/${bids_database}?sslmode=disable" CONDUCTOR_SERVER_URL="$conductor_url" RECORD_HUB_NATS_URL="$nats_url" RECORD_HUB_COMMANDS_ENABLED=true)
start_background bids-worker.log env "${bids_common[@]}" "$runtime_root/bids-worker"
start_background bids-api.log env "${bids_common[@]}" HTTP_ADDR="127.0.0.1:${bids_api_port}" AUTH_SEED_ENABLED=true AUTH_COOKIE_SECURE=false MINIO_ENDPOINT="${minio_endpoint#http://}" MINIO_ACCESS_KEY="${MINIO_ACCESS_KEY:-minioadmin}" MINIO_SECRET_KEY="${MINIO_SECRET_KEY:-minioadmin}" MINIO_BUCKET="${MINIO_BUCKET:-bids-documents}" "$runtime_root/bids-api"
wait_http "http://127.0.0.1:${bids_api_port}/healthz" "Bids API"

write_manifest PASS "all native infrastructure and four owner API/worker processes became ready"
echo "P3-400/P3-401 four-owner topology passed"
echo "evidence: $evidence_dir"
