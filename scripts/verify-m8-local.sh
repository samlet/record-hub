#!/usr/bin/env bash
set -euo pipefail

# M8-085 local smoke. By default it runs the dependency-free gates. Set
# RECORD_HUB_M8_LOCAL_LIVE=1 to run the dependency topology smoke. The
# runtime is Docker by default; RECORD_HUB_M8_RUNTIME=native connects to
# already-running local services and never stops or mutates their lifecycle.
record_hub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$record_hub_root"

make check
make m8-happy-path
make m8-failure-path

if [[ "${RECORD_HUB_M8_LOCAL_LIVE:-0}" != "1" ]]; then
  echo "M8-085 local dependency smoke: SKIPPED (set RECORD_HUB_M8_LOCAL_LIVE=1)"
  exit 0
fi

runtime="${RECORD_HUB_M8_RUNTIME:-docker}"
if [[ "$runtime" == "native" ]]; then
  for command in curl openssl mongosh nats; do
    command -v "$command" >/dev/null || { echo "$command is required for native local smoke" >&2; exit 2; }
  done

  export RECORD_HUB_NATS_URL="${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}"
  export RECORD_HUB_MONGODB_URI="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true}"
  RECORD_HUB_NATS_URL="$RECORD_HUB_NATS_URL" make nats-smoke
  RECORD_HUB_MONGODB_URI="$RECORD_HUB_MONGODB_URI" make mongo-smoke

  if [[ "${RECORD_HUB_M8_DEX_LIVE:-0}" == "1" ]]; then
    make dex-smoke
  else
    echo "M8-085 native Dex smoke: SKIPPED (set RECORD_HUB_M8_DEX_LIVE=1 with a compatible local Dex config)"
  fi

  export RECORD_HUB_MODE=api
  export RECORD_HUB_HTTP_ADDRESS="${RECORD_HUB_HTTP_ADDRESS:-127.0.0.1:18080}"
  export RECORD_HUB_SHUTDOWN_TIMEOUT=2s
  export RECORD_HUB_WEB_ENABLED="${RECORD_HUB_WEB_ENABLED:-false}"
  if [[ "$RECORD_HUB_WEB_ENABLED" == "true" ]]; then
    : "${RECORD_HUB_WEB_ISSUER:?RECORD_HUB_WEB_ISSUER is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_AUDIENCE:?RECORD_HUB_WEB_AUDIENCE is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT:?RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_TOKEN_ENDPOINT:?RECORD_HUB_WEB_TOKEN_ENDPOINT is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_CLIENT_ID:?RECORD_HUB_WEB_CLIENT_ID is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_CLIENT_SECRET:?RECORD_HUB_WEB_CLIENT_SECRET is required when native Web smoke is enabled}"
    : "${RECORD_HUB_WEB_REDIRECT_URL:?RECORD_HUB_WEB_REDIRECT_URL is required when native Web smoke is enabled}"
    export RECORD_HUB_WEB_SESSION_SECRET="${RECORD_HUB_WEB_SESSION_SECRET:-$(openssl rand -hex 32)}"
    export RECORD_HUB_WEB_SECURE_COOKIES="${RECORD_HUB_WEB_SECURE_COOKIES:-false}"
    export RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS="${RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS:-true}"
  fi

  build/record-hub serve >"${TMPDIR:-/tmp}/record-hub-m8-native.log" 2>&1 &
  server_pid=$!
  cleanup() {
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  }
  trap cleanup EXIT HUP INT TERM

  for _ in {1..50}; do
    if curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/healthz" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/healthz" >/dev/null
  curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/metrics" | grep -q 'record_hub_http_requests_total'
  if [[ "$RECORD_HUB_WEB_ENABLED" == "true" ]]; then
    curl --silent --show-error --fail --include "http://$RECORD_HUB_HTTP_ADDRESS/auth/login" | grep -q 'record_hub_oidc_state='
  fi
  echo "M8-085 native Mongo/NATS/API smoke passed (Dex/Web smoke is opt-in; native services were not stopped)"
  exit 0
fi

if [[ "$runtime" != "docker" ]]; then
  echo "RECORD_HUB_M8_RUNTIME must be docker or native; got $runtime" >&2
  exit 2
fi

for command in docker curl openssl; do
  command -v "$command" >/dev/null || { echo "$command is required for local smoke" >&2; exit 2; }
done
docker info >/dev/null

if [[ ! -f deploy/local/dex/.env.local ]]; then
  make dex-env
fi
make dex-up

export MONGODB_ROOT_USERNAME="${MONGODB_ROOT_USERNAME:-record_hub_root}"
export MONGODB_ROOT_PASSWORD="${MONGODB_ROOT_PASSWORD:-$(openssl rand -hex 24)}"
export RECORD_HUB_MONGODB_USERNAME="${RECORD_HUB_MONGODB_USERNAME:-record_hub}"
export RECORD_HUB_MONGODB_PASSWORD="${RECORD_HUB_MONGODB_PASSWORD:-$(openssl rand -hex 24)}"
make mongo-up

export NATS_ADMIN_PASSWORD="${NATS_ADMIN_PASSWORD:-$(openssl rand -hex 24)}"
export NATS_APPROVER_PASSWORD="${NATS_APPROVER_PASSWORD:-$(openssl rand -hex 24)}"
export NATS_FLUXION_PASSWORD="${NATS_FLUXION_PASSWORD:-$(openssl rand -hex 24)}"
export NATS_BIDS_PASSWORD="${NATS_BIDS_PASSWORD:-$(openssl rand -hex 24)}"
export NATS_RECORD_HUB_PASSWORD="${NATS_RECORD_HUB_PASSWORD:-$(openssl rand -hex 24)}"
make nats-up
export RECORD_HUB_NATS_URL="${RECORD_HUB_NATS_URL:-nats://record-hub-admin:${NATS_ADMIN_PASSWORD}@127.0.0.1:4222}"
make nats-init

web_secret="$(sed -n 's/^DEX_RECORD_HUB_WEB_SECRET=//p' deploy/local/dex/.env.local)"
if [[ -z "$web_secret" ]]; then
  echo "DEX_RECORD_HUB_WEB_SECRET is missing; run make dex-env again" >&2
  exit 2
fi
export RECORD_HUB_MODE=api
export RECORD_HUB_HTTP_ADDRESS="${RECORD_HUB_HTTP_ADDRESS:-127.0.0.1:8080}"
export RECORD_HUB_SHUTDOWN_TIMEOUT=2s
export RECORD_HUB_WEB_ENABLED=true
export RECORD_HUB_WEB_ISSUER="${RECORD_HUB_WEB_ISSUER:-http://127.0.0.1:5556/dex}"
export RECORD_HUB_WEB_AUDIENCE="${RECORD_HUB_WEB_AUDIENCE:-record-hub-web-local}"
export RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT="${RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT:-http://127.0.0.1:5556/dex/auth}"
export RECORD_HUB_WEB_TOKEN_ENDPOINT="${RECORD_HUB_WEB_TOKEN_ENDPOINT:-http://127.0.0.1:5556/dex/token}"
export RECORD_HUB_WEB_CLIENT_ID="${RECORD_HUB_WEB_CLIENT_ID:-record-hub-web-local}"
export RECORD_HUB_WEB_CLIENT_SECRET="${RECORD_HUB_WEB_CLIENT_SECRET:-$web_secret}"
export RECORD_HUB_WEB_REDIRECT_URL="${RECORD_HUB_WEB_REDIRECT_URL:-http://127.0.0.1:8080/auth/callback}"
export RECORD_HUB_WEB_SESSION_SECRET="${RECORD_HUB_WEB_SESSION_SECRET:-$(openssl rand -hex 32)}"
export RECORD_HUB_WEB_SECURE_COOKIES=false
export RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS=true

build/record-hub serve >"${TMPDIR:-/tmp}/record-hub-m8-local.log" 2>&1 &
server_pid=$!
cleanup() {
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  make nats-down mongo-down dex-down >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

for _ in {1..50}; do
  if curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/healthz" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/healthz" >/dev/null
curl --silent --show-error --fail "http://$RECORD_HUB_HTTP_ADDRESS/metrics" | grep -q 'record_hub_http_requests_total'
curl --silent --show-error --fail --include "http://$RECORD_HUB_HTTP_ADDRESS/auth/login" | grep -q 'record_hub_oidc_state='
echo "M8-085 local Dex/Mongo/NATS/API smoke passed (interactive browser login remains manual)"
