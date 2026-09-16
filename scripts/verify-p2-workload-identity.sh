#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

for command in curl jq openssl lsof; do
  command -v "$command" >/dev/null || { echo "$command is required for the workload identity smoke" >&2; exit 2; }
done

issuer_port="${RECORD_HUB_WORKLOAD_ISSUER_PORT:-15557}"
issuer_address="127.0.0.1:${issuer_port}"
issuer="http://${issuer_address}/workload"
audience="record-hub-api-local"
scope="recordhub.binding.snapshot"

if lsof -nP -iTCP:"$issuer_port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "workload issuer port ${issuer_port} is already in use; choose RECORD_HUB_WORKLOAD_ISSUER_PORT" >&2
  exit 2
fi

fluxion_secret="$(openssl rand -hex 32)"
bids_secret="$(openssl rand -hex 32)"
clients="$(jq -cn \
  --arg fluxion "$fluxion_secret" \
  --arg bids "$bids_secret" \
  --arg scope "$scope" \
  '[{id:"fluxion-to-record-hub",secret:$fluxion,scopes:[$scope]},{id:"bids-to-record-hub",secret:$bids,scopes:[$scope]}]')"

mkdir -p build
go build -trimpath -o build/workload-issuer ./tools/workload-issuer
go build -trimpath -o build/workload-token-smoke ./server/cmd/workload-token-smoke

export RECORD_HUB_WORKLOAD_ISSUER_ADDRESS="$issuer_address"
export RECORD_HUB_WORKLOAD_ISSUER="$issuer"
export RECORD_HUB_WORKLOAD_AUDIENCE="$audience"
export RECORD_HUB_WORKLOAD_CLIENTS="$clients"
build/workload-issuer >"${TMPDIR:-/tmp}/record-hub-workload-issuer.log" 2>&1 &
issuer_pid=$!
cleanup() {
  kill -TERM "$issuer_pid" 2>/dev/null || true
  wait "$issuer_pid" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

for _ in {1..80}; do
  if curl --silent --show-error --fail "$issuer/.well-known/openid-configuration" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl --silent --show-error --fail "$issuer/.well-known/openid-configuration" | jq -e \
  --arg issuer "$issuer" \
  '.issuer == $issuer and .grant_types_supported == ["client_credentials"] and (.token_endpoint_auth_methods_supported | index("client_secret_basic")) != null' >/dev/null

verify_client() {
  local client_id="$1" client_secret="$2"
  RECORD_HUB_WORKLOAD_CLIENT_ID="$client_id" \
  RECORD_HUB_WORKLOAD_CLIENT_SECRET="$client_secret" \
  RECORD_HUB_WORKLOAD_SCOPE="$scope" \
  build/workload-token-smoke
}

verify_client "fluxion-to-record-hub" "$fluxion_secret"
verify_client "bids-to-record-hub" "$bids_secret"

bad_secret_status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --user "fluxion-to-record-hub:not-the-secret" \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode "scope=$scope" \
  "$issuer/token")"
[[ "$bad_secret_status" == "401" ]] || { echo "bad client secret returned HTTP $bad_secret_status, want 401" >&2; exit 1; }

bad_scope_status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --user "fluxion-to-record-hub:$fluxion_secret" \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'scope=recordhub.command.submit' \
  "$issuer/token")"
[[ "$bad_scope_status" == "400" ]] || { echo "bad scope returned HTTP $bad_scope_status, want 400" >&2; exit 1; }

echo "P2 workload identity live smoke passed (isolated issuer ${issuer}; ephemeral keys and secrets)"

