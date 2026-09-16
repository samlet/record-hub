#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

for command in conductor curl lsof temporal; do
  command -v "$command" >/dev/null || {
    echo "$command is required for the isolated workflow-engine smoke" >&2
    exit 2
  }
done

temporal_port="${RECORD_HUB_P2_TEMPORAL_PORT:-17233}"
temporal_ui_port="${RECORD_HUB_P2_TEMPORAL_UI_PORT:-18233}"
conductor_port="${RECORD_HUB_P2_CONDUCTOR_PORT:-18080}"

for port in "$temporal_port" "$temporal_ui_port" "$conductor_port"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "isolated workflow-engine port $port is already in use; override the corresponding RECORD_HUB_P2_*_PORT" >&2
    exit 2
  fi
done

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p2-engines.XXXXXX")"
pids=()

cleanup() {
  local exit_status=$?
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
  if [[ "$exit_status" == "0" ]]; then
    rm -rf -- "$temp_root"
  else
    echo "isolated workflow-engine foundation failed; bounded logs preserved at $temp_root" >&2
  fi
}
trap cleanup EXIT HUP INT TERM

wait_http() {
  local url="$1" label="$2"
  for _ in {1..180}; do
    if curl --silent --show-error --fail "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  echo "$label did not become ready at $url" >&2
  return 1
}

temporal server start-dev \
  --ip 127.0.0.1 \
  --port "$temporal_port" \
  --ui-ip 127.0.0.1 \
  --ui-port "$temporal_ui_port" \
  --db-filename "$temp_root/temporal.db" \
  --namespace default \
  >"$temp_root/temporal.log" 2>&1 &
pids+=("$!")

for _ in {1..180}; do
  if temporal operator namespace describe \
      --address "127.0.0.1:${temporal_port}" \
      --namespace default \
      --tls=false \
      >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
temporal operator namespace describe \
  --address "127.0.0.1:${temporal_port}" \
  --namespace default \
  --tls=false \
  >/dev/null
wait_http "http://127.0.0.1:${temporal_ui_port}" "isolated Temporal UI"
echo "isolated Temporal foundation: passed (gRPC ${temporal_port}, UI ${temporal_ui_port})"

(
  cd "$temp_root"
  exec conductor server start --oss --port "$conductor_port" --foreground
) >"$temp_root/conductor.log" 2>&1 &
pids+=("$!")

conductor_health_url=""
for candidate in \
  "http://127.0.0.1:${conductor_port}/health" \
  "http://127.0.0.1:${conductor_port}/api/health"; do
  for _ in {1..180}; do
    if curl --silent --show-error --fail "$candidate" >/dev/null 2>&1; then
      conductor_health_url="$candidate"
      break 2
    fi
    sleep 0.2
  done
done
[[ -n "$conductor_health_url" ]] || {
  echo "isolated Conductor did not become ready on port $conductor_port" >&2
  exit 1
}
curl --silent --show-error --fail "$conductor_health_url" >/dev/null
echo "isolated Conductor foundation: passed (HTTP ${conductor_port})"

echo "P2-0-006a engine foundation passed; business process integration remains a separate gate"
