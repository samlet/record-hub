#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

for command in curl dex htpasswd jq lsof mongod mongosh nats-server openssl; do
  command -v "$command" >/dev/null || { echo "$command is required for the isolated core smoke" >&2; exit 2; }
done

mongo_port="${RECORD_HUB_P2_MONGO_PORT:-37018}"
nats_port="${RECORD_HUB_P2_NATS_PORT:-14223}"
nats_monitor_port="${RECORD_HUB_P2_NATS_MONITOR_PORT:-18223}"
dex_port="${RECORD_HUB_P2_DEX_PORT:-15566}"
dex_telemetry_port="${RECORD_HUB_P2_DEX_TELEMETRY_PORT:-15568}"
workload_port="${RECORD_HUB_P2_WORKLOAD_PORT:-15557}"
api_port="${RECORD_HUB_P2_API_PORT:-18081}"

for port in "$mongo_port" "$nats_port" "$nats_monitor_port" "$dex_port" "$dex_telemetry_port" "$workload_port" "$api_port"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "isolated topology port $port is already in use; override the corresponding RECORD_HUB_P2_*_PORT" >&2
    exit 2
  fi
done

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p2-core.XXXXXX")"
mkdir -p "$temp_root/mongo" "$temp_root/nats"
pids=()

cleanup() {
  local status=$?
  set +e
  for pid in "${pids[@]}"; do
    kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in {1..80}; do
    local running=0
    for pid in "${pids[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        running=1
      fi
    done
    [[ "$running" == "0" ]] && break
    sleep 0.1
  done
  for pid in "${pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
    wait "$pid" 2>/dev/null || true
  done
  if [[ "$status" == "0" ]]; then
    rm -rf -- "$temp_root"
  else
    echo "isolated topology failed; bounded logs preserved at $temp_root" >&2
  fi
}
trap cleanup EXIT HUP INT TERM

wait_http() {
  local url="$1" label="$2"
  for _ in {1..100}; do
    if curl --silent --show-error --fail "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "$label did not become ready at $url" >&2
  return 1
}

replica_set="record-hub-p2-rs"
mongo_uri="mongodb://127.0.0.1:${mongo_port}/record_hub_p2?replicaSet=${replica_set}&directConnection=true"
mongod \
  --bind_ip 127.0.0.1 \
  --port "$mongo_port" \
  --dbpath "$temp_root/mongo" \
  --replSet "$replica_set" \
  --nounixsocket \
  --logpath "$temp_root/mongod.log" \
  --logappend &
pids+=("$!")

for _ in {1..100}; do
  if mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'db.adminCommand({ping:1})' >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval \
  "rs.initiate({_id:'${replica_set}',members:[{_id:0,host:'127.0.0.1:${mongo_port}'}]})" >/dev/null
for _ in {1..150}; do
  if mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
mongosh --quiet --host "127.0.0.1:${mongo_port}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)' >/dev/null
RECORD_HUB_MONGODB_URI="$mongo_uri" make mongo-smoke >/dev/null
echo "isolated MongoDB replica set: passed"

nats_url="nats://127.0.0.1:${nats_port}"
nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$temp_root/nats" -n record-hub-p2-isolated >"$temp_root/nats.log" 2>&1 &
pids+=("$!")
wait_http "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true" "NATS"
RECORD_HUB_NATS_URL="$nats_url" make nats-smoke >/dev/null
echo "isolated NATS JetStream topology: passed"

export DEX_ISSUER="http://127.0.0.1:${dex_port}/dex"
export DEX_ISOLATED_DATABASE="$temp_root/dex.db"
export DEX_ISOLATED_WEB_ADDRESS="127.0.0.1:${dex_port}"
export DEX_ISOLATED_TELEMETRY_ADDRESS="127.0.0.1:${dex_telemetry_port}"
export DEX_RECORD_HUB_API_REDIRECT="http://127.0.0.1:${api_port}/auth/callback"
export DEX_LOCAL_EMAIL="record-hub-dev@example.test"
export DEX_LOCAL_PASSWORD="$(openssl rand -base64 24 | tr -d '\n')"
export DEX_LOCAL_PASSWORD_HASH="$(htpasswd -bnBC 10 '' "$DEX_LOCAL_PASSWORD" | cut -d: -f2)"
export DEX_APPROVER_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_FLUXION_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_BIDS_WEB_SECRET="$(openssl rand -hex 32)"
export DEX_RECORD_HUB_WEB_SECRET="$(openssl rand -hex 32)"
# Dex's container entrypoint renders gomplate templates, but the native
# binary intentionally does not. Resolve only the non-secret topology values
# into the disposable config under temp_root; client secrets remain env-only.
sed \
  -e "s|\${DEX_ISSUER}|${DEX_ISSUER}|g" \
  -e "s|\${DEX_ISOLATED_DATABASE}|${DEX_ISOLATED_DATABASE}|g" \
  -e "s|\${DEX_ISOLATED_WEB_ADDRESS}|${DEX_ISOLATED_WEB_ADDRESS}|g" \
  -e "s|\${DEX_ISOLATED_TELEMETRY_ADDRESS}|${DEX_ISOLATED_TELEMETRY_ADDRESS}|g" \
  -e "s|\${DEX_RECORD_HUB_API_REDIRECT}|${DEX_RECORD_HUB_API_REDIRECT}|g" \
  deploy/local/isolated/dex.yaml >"$temp_root/dex.yaml"
dex serve "$temp_root/dex.yaml" >"$temp_root/dex.log" 2>&1 &
pids+=("$!")
wait_http "$DEX_ISSUER/.well-known/openid-configuration" "Dex"
go run ./tools/dex-smoke >/dev/null
echo "isolated human Dex four-client PKCE contract: passed"

workload_issuer="http://127.0.0.1:${workload_port}/workload"
workload_audience="record-hub-api-local"
workload_scope="recordhub.binding.snapshot"
fluxion_secret="$(openssl rand -hex 32)"
bids_secret="$(openssl rand -hex 32)"
export RECORD_HUB_WORKLOAD_ISSUER_ADDRESS="127.0.0.1:${workload_port}"
export RECORD_HUB_WORKLOAD_ISSUER="$workload_issuer"
export RECORD_HUB_WORKLOAD_AUDIENCE="$workload_audience"
export RECORD_HUB_WORKLOAD_CLIENTS="$(jq -cn --arg fluxion "$fluxion_secret" --arg bids "$bids_secret" --arg scope "$workload_scope" '[{id:"fluxion-to-record-hub",secret:$fluxion,scopes:[$scope]},{id:"bids-to-record-hub",secret:$bids,scopes:[$scope]}]')"
go build -trimpath -o build/workload-issuer ./tools/workload-issuer
build/workload-issuer >"$temp_root/workload-issuer.log" 2>&1 &
pids+=("$!")
wait_http "$workload_issuer/.well-known/openid-configuration" "workload issuer"

go build -trimpath -o build/record-hub ./server/cmd/record-hub
export RECORD_HUB_MODE=api
export RECORD_HUB_HTTP_ADDRESS="127.0.0.1:${api_port}"
export RECORD_HUB_SHUTDOWN_TIMEOUT=3s
export RECORD_HUB_MONGODB_URI="$mongo_uri"
export RECORD_HUB_MONGODB_DATABASE=record_hub_p2
export RECORD_HUB_NATS_URL="$nats_url"
export RECORD_HUB_OIDC_ISSUER="$workload_issuer"
export RECORD_HUB_OIDC_AUDIENCE="$workload_audience"
export RECORD_HUB_OIDC_PRINCIPAL_KIND=service
export RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER=true
export RECORD_HUB_BINDING_MACHINE_POLICIES="$(jq -cn --arg issuer "$workload_issuer" --arg audience "$workload_audience" --arg scope "$workload_scope" '[{issuer:$issuer,subject:"fluxion-to-record-hub",audience:$audience,scope:$scope,tenantId:"tenant-p2",workspaceId:"workspace-p2",purpose:"diagnostic",resourceSystem:"fluxion",resourceType:"PROJECT"}]')"
export RECORD_HUB_WEB_ENABLED=true
export RECORD_HUB_WEB_ISSUER="$DEX_ISSUER"
export RECORD_HUB_WEB_AUDIENCE=record-hub-web-local
export RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT="$DEX_ISSUER/auth"
export RECORD_HUB_WEB_TOKEN_ENDPOINT="$DEX_ISSUER/token"
export RECORD_HUB_WEB_CLIENT_ID=record-hub-web-local
export RECORD_HUB_WEB_CLIENT_SECRET="$DEX_RECORD_HUB_WEB_SECRET"
export RECORD_HUB_WEB_REDIRECT_URL="$DEX_RECORD_HUB_API_REDIRECT"
export RECORD_HUB_WEB_SESSION_SECRET="$(openssl rand -hex 32)"
export RECORD_HUB_WEB_SECURE_COOKIES=false
export RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS=true
build/record-hub serve >"$temp_root/record-hub.log" 2>&1 &
pids+=("$!")

api_url="http://127.0.0.1:${api_port}"
wait_http "$api_url/healthz" "Record Hub API"
curl --silent --show-error --fail "$api_url/readyz" | jq -e '.status == "ready"' >/dev/null
curl --silent --show-error --fail --include "$api_url/auth/login" | grep -q 'record_hub_oidc_state='

token="$(curl --silent --show-error --fail \
  --user "fluxion-to-record-hub:$fluxion_secret" \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode "scope=$workload_scope" \
  "$workload_issuer/token" | jq -er '.access_token')"
snapshot_payload='{"tenantId":"tenant-p2","workspaceId":"workspace-p2","recordRef":"fluxion:PROJECT:missing","schemaId":"urn:record-hub:summary:project:v1","schemaVersion":1,"expectedRecordVersion":1,"expectedSourceVersion":0,"purpose":"diagnostic"}'
allowed_response="$temp_root/allowed-response.json"
allowed_status="$(curl --silent --show-error --output "$allowed_response" --write-out '%{http_code}' \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: p2-isolated-allowed' \
  --data "$snapshot_payload" \
  "$api_url/api/v1/bindings/snapshots")"
[[ "$allowed_status" == "404" ]] || { echo "allowed machine policy returned HTTP $allowed_status" >&2; exit 1; }
jq -e '.error.code == "RECORD_NOT_FOUND"' "$allowed_response" >/dev/null

denied_payload="$(jq -c '.workspaceId="workspace-other"' <<<"$snapshot_payload")"
denied_response="$temp_root/denied-response.json"
denied_status="$(curl --silent --show-error --output "$denied_response" --write-out '%{http_code}' \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: p2-isolated-denied' \
  --data "$denied_payload" \
  "$api_url/api/v1/bindings/snapshots")"
[[ "$denied_status" == "403" ]] || { echo "cross-workspace machine policy returned HTTP $denied_status" >&2; exit 1; }
jq -e '.error.code == "FORBIDDEN"' "$denied_response" >/dev/null

echo "P2 isolated core topology passed (Mongo ${mongo_port}, NATS ${nats_port}, Dex ${dex_port}, workload issuer ${workload_port}, API ${api_port})"
