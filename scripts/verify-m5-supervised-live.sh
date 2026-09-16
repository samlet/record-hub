#!/usr/bin/env bash
set -Eeuo pipefail

# M5-059 supervised live gate. This is opt-in because it starts the three
# producer runtimes and creates temporary PostgreSQL/SQLite databases. In the
# default `auto` mode it also starts Bids API when native MinIO is healthy and
# drives all three business APIs before allowing a controlled Outbox fallback.
# Native MongoDB, NATS JetStream and Conductor daemons are never stopped.
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
bids_api_port="${RECORD_HUB_M5_SUPERVISED_BIDS_API_PORT:-18093}"
api_trigger_mode="${RECORD_HUB_M5_SUPERVISED_API_MODE:-auto}"
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
bids_api_pid=""
bids_api_available=0
created_databases=()

case "$api_trigger_mode" in
  off|auto|required) ;;
  *)
    echo "RECORD_HUB_M5_SUPERVISED_API_MODE must be off, auto or required" >&2
    exit 2
    ;;
esac

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
  for pid in "$bids_api_pid" "$bids_pid" "$fluxion_pid" "$approver_pid" "$record_hub_pid"; do
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
if [[ "$api_trigger_mode" != off ]]; then
  port_free "127.0.0.1:$bids_api_port"
fi

echo "M5 supervised live gate: building producer runtimes"
(cd "$approver_root" && mvn -q -DskipTests package)
(cd "$fluxion_root/server" && ./gradlew -q installDist)
(cd "$bids_root/backend" && go build -trimpath -o "$work_dir/bids-worker" ./cmd/worker)
if [[ "$api_trigger_mode" != off ]]; then
  (cd "$bids_root/backend" && go build -trimpath -o "$work_dir/bids-api" ./cmd/api)
fi
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

if [[ "$api_trigger_mode" != off ]]; then
  # The Bids API has a real MinIO startup dependency. Start it only when the
  # native daemon is healthy; auto mode can then fall back to the controlled
  # source-outbox seed without making the whole gate environment-sensitive.
  minio_ready=0
  curl -fsS "http://${MINIO_ENDPOINT:-127.0.0.1:9000}/minio/health/live" >/dev/null 2>&1 && minio_ready=1 || true
  if [[ "$minio_ready" == 1 ]]; then
    (
      cd "$bids_root/backend"
      exec env \
        HTTP_ADDR="127.0.0.1:$bids_api_port" \
        DB_DRIVER=sqlite \
        DATABASE_DSN="file:$bids_db?cache=shared&_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL" \
        CONDUCTOR_SERVER_URL="${CONDUCTOR_SERVER_URL:-http://127.0.0.1:8080/api}" \
        AUTH_SEED_ENABLED=true \
        AUTH_SEED_PASSWORD="${AUTH_SEED_PASSWORD:-demo-password}" \
        AUTH_COOKIE_SECURE=false \
        MINIO_ENDPOINT="${MINIO_ENDPOINT:-127.0.0.1:9000}" \
        MINIO_ACCESS_KEY="${MINIO_ACCESS_KEY:-minioadmin}" \
        MINIO_SECRET_KEY="${MINIO_SECRET_KEY:-minioadmin}" \
        MINIO_BUCKET="${MINIO_BUCKET:-bids-documents}" \
        RECORD_HUB_NATS_URL="$nats_url" \
        RECORD_HUB_WORKSPACE_ID="$workspace" \
        "$work_dir/bids-api"
    ) >"$work_dir/bids-api.log" 2>&1 &
    bids_api_pid=$!
    for _ in {1..160}; do
      curl -fsS "http://127.0.0.1:$bids_api_port/healthz" >/dev/null 2>&1 && break
      kill -0 "$bids_api_pid" 2>/dev/null || break
      sleep 0.25
    done
    if ! curl -fsS "http://127.0.0.1:$bids_api_port/healthz" >/dev/null 2>&1; then
      echo "M5 supervised: Bids API unavailable; controlled fallback remains eligible" >&2
      [[ "$api_trigger_mode" == required ]] && { tail -60 "$work_dir/bids-api.log" >&2; exit 1; }
    else
      bids_api_available=1
    fi
  elif [[ "$api_trigger_mode" == required ]]; then
    echo "M5 supervised: Bids API required but native MinIO is unavailable" >&2
    exit 1
  else
    echo "M5 supervised: native MinIO unavailable; Bids API trigger will use controlled fallback" >&2
  fi
fi

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
approver_aggregate_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
fluxion_aggregate_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
bids_aggregate_id="${tenant}-tender"
approver_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
fluxion_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
bids_event_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"

approver_source="direct-outbox"
fluxion_source="direct-outbox"
bids_source="direct-outbox"

api_failed() {
  local producer="$1" detail="$2"
  echo "M5 supervised: $producer business API trigger unavailable ($detail)" >&2
  if [[ "$api_trigger_mode" == required ]]; then
    exit 1
  fi
}

if [[ "$api_trigger_mode" != off ]]; then
  echo "M5 supervised live gate: triggering producer business APIs (mode=$api_trigger_mode)"

  # Approver needs a published form and definition before an application draft
  # can be created. Both are temporary tenant-scoped setup facts and are
  # created through the same public API as a real operator would use.
  approver_form_key="m5-supervised-form"
  approver_admin_headers=(
    -H "Content-Type: application/json"
    -H "X-Tenant-Id: $tenant"
    -H "X-User-Id: m5-supervised-admin"
    -H "X-Roles: PROCESS_ADMIN"
  )
  approver_form_payload='{"key":"m5-supervised-form","name":"M5 supervised form","fields":[{"key":"title","label":"Title","type":"TEXT","required":false,"maxLength":200}]}'
  approver_definition_payload="$(jq -cn --arg form_key "$approver_form_key" '{key:"m5-supervised",name:"M5 supervised process",nodes:[{id:"start",type:"START",name:"start"},{id:"done",type:"END",name:"done",result:"DONE"}],edges:[{from:"start",to:"done"}],formKey:$form_key,formVersion:1}')"
  form_draft_status="$(curl -sS "http://127.0.0.1:$approver_port/api/v1/forms/$approver_form_key/draft" "${approver_admin_headers[@]}" -X PUT -d "$approver_form_payload" -o "$work_dir/approver-form-draft.json" -w '%{http_code}' || true)"
  form_publish_status="$(curl -sS "http://127.0.0.1:$approver_port/api/v1/forms/$approver_form_key/publish" "${approver_admin_headers[@]}" -X POST -o "$work_dir/approver-form-publish.json" -w '%{http_code}' || true)"
  definition_draft_status="$(curl -sS "http://127.0.0.1:$approver_port/api/v1/process-definitions/m5-supervised/draft" "${approver_admin_headers[@]}" -X PUT -d "$approver_definition_payload" -o "$work_dir/approver-definition-draft.json" -w '%{http_code}' || true)"
  definition_publish_status="$(curl -sS "http://127.0.0.1:$approver_port/api/v1/process-definitions/m5-supervised/publish" "${approver_admin_headers[@]}" -X POST -o "$work_dir/approver-definition-publish.json" -w '%{http_code}' || true)"
  if [[ "$form_draft_status" == 2* && "$form_publish_status" == 2* && "$definition_draft_status" == 2* && "$definition_publish_status" == 2* ]]; then
    approver_create_body="$(jq -cn --arg process_key m5-supervised --arg business_key "$tenant" '{processKey:$process_key,businessKey:$business_key}')"
    approver_create_status="$(curl -sS "http://127.0.0.1:$approver_port/api/v1/applications" \
      -H 'Content-Type: application/json' -H "X-Tenant-Id: $tenant" -H 'X-User-Id: m5-supervised-user' \
      -H 'X-Roles: PROCESS_STARTER' -H "Idempotency-Key: m5-supervised-$run_id" \
      -X POST -d "$approver_create_body" -o "$work_dir/approver-create.json" -w '%{http_code}' || true)"
    if [[ "$approver_create_status" == 2* ]] && approver_id="$(jq -er '.id' "$work_dir/approver-create.json" 2>/dev/null)"; then
      approver_aggregate_id="$approver_id"
      for _ in {1..80}; do
        approver_event_id="$(psql -X -At -U "$pg_user" -d "$approver_db" -c "SELECT id FROM integration_outbox WHERE aggregate_id = '$approver_aggregate_id' AND message_type = 'APPLICATION_SUMMARY_CHANGED' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
        [[ -n "$approver_event_id" ]] && break
        sleep 0.25
      done
      if [[ -n "$approver_event_id" ]]; then
        approver_source="business-api"
        echo "M5 supervised: Approver application API created $approver_aggregate_id"
      else
        api_failed "Approver" "application API returned no summary outbox row"
      fi
    else
      api_failed "Approver" "HTTP $approver_create_status; see $work_dir/approver-create.json"
    fi
  else
    api_failed "Approver" "setup HTTP statuses form=$form_draft_status/$form_publish_status definition=$definition_draft_status/$definition_publish_status"
  fi

  # Fluxion's operator login, customer creation and project creation are a
  # complete business path. The seeded service product is stable across its
  # migration versions; project creation itself writes the summary Outbox in
  # the same Exposed transaction as the project row.
  fluxion_cookie="$work_dir/fluxion.cookie"
  fluxion_login_status="$(curl -sS -c "$fluxion_cookie" -H 'Content-Type: application/json' \
    -X POST "http://127.0.0.1:$fluxion_port/api/auth/login" \
    -d "$(jq -cn --arg username "${FLUXION_API_USERNAME:-operator}" --arg password "${FLUXION_API_PASSWORD:-op123456}" '{username:$username,password:$password}')" \
    -o "$work_dir/fluxion-login.json" -w '%{http_code}' || true)"
  fluxion_customer_status=""
  fluxion_customer_id=""
  if [[ "$fluxion_login_status" == 2* ]]; then
    fluxion_customer_status="$(curl -sS -b "$fluxion_cookie" -H 'Content-Type: application/json' \
      -X POST "http://127.0.0.1:$fluxion_port/api/customers" \
      -d '{"name":"M5 supervised customer","contact":"m5-supervised"}' \
      -o "$work_dir/fluxion-customer.json" -w '%{http_code}' || true)"
    fluxion_customer_id="$(jq -er '.id' "$work_dir/fluxion-customer.json" 2>/dev/null || true)"
  fi
  if [[ "$fluxion_login_status" == 2* && "$fluxion_customer_status" == 2* && -n "$fluxion_customer_id" ]]; then
    fluxion_project_body="$(jq -cn --arg project_id "$fluxion_aggregate_id" --arg customer_id "$fluxion_customer_id" '{projectId:$project_id,customerId:$customer_id,serviceProductId:"44444444-4444-4444-4444-444444444441",scheduledDate:"2099-01-02"}')"
    fluxion_project_status="$(curl -sS -b "$fluxion_cookie" -H 'Content-Type: application/json' \
      -X POST "http://127.0.0.1:$fluxion_port/api/projects" -d "$fluxion_project_body" \
      -o "$work_dir/fluxion-project.json" -w '%{http_code}' || true)"
    if [[ "$fluxion_project_status" == 2* ]]; then
      for _ in {1..80}; do
        fluxion_event_id="$(psql -X -At -U "$pg_user" -d "$fluxion_db" -c "SELECT id FROM record_hub_outbox WHERE aggregate_id = '$fluxion_aggregate_id' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
        [[ -n "$fluxion_event_id" ]] && break
        sleep 0.25
      done
      if [[ -n "$fluxion_event_id" ]]; then
        fluxion_source="business-api"
        echo "M5 supervised: Fluxion project API created $fluxion_aggregate_id"
      else
        api_failed "Fluxion" "project API returned no summary outbox row"
      fi
    else
      api_failed "Fluxion" "project API HTTP $fluxion_project_status; see $work_dir/fluxion-project.json"
    fi
  else
    api_failed "Fluxion" "login/customer HTTP $fluxion_login_status/$fluxion_customer_status"
  fi

  # Bids requires the project onboarding workflow to reach READY before a
  # tender can be created. The worker started above advances the Conductor
  # workflow; the API approval call then exercises the normal command Outbox
  # before the tender transaction emits TENDER_SUMMARY_CHANGED.
  if [[ "$bids_api_available" == 1 ]]; then
    bids_cookie="$work_dir/bids.cookie"
    bids_login_status="$(curl -sS -c "$bids_cookie" -H 'Content-Type: application/json' \
      -X POST "http://127.0.0.1:$bids_api_port/api/v1/auth/login" \
      -d "$(jq -cn --arg password "${AUTH_SEED_PASSWORD:-demo-password}" '{username:"demo-user",password:$password}')" \
      -o "$work_dir/bids-login.json" -w '%{http_code}' || true)"
    bids_project_status=""
    if [[ "$bids_login_status" == 2* ]]; then
      bids_project_status="$(curl -sS -b "$bids_cookie" -H 'Content-Type: application/json' \
        -X POST "http://127.0.0.1:$bids_api_port/api/v1/projects" \
        -d '{"name":"M5 supervised project","description":"M5 supervised business API"}' \
        -o "$work_dir/bids-project.json" -w '%{http_code}' || true)"
    fi
    bids_project_id="$(jq -er '.id' "$work_dir/bids-project.json" 2>/dev/null || true)"
    if [[ "$bids_login_status" == 2* && "$bids_project_status" == 2* && -n "$bids_project_id" ]]; then
      bids_project_state=""
      for _ in {1..240}; do
        bids_project_state="$(curl -sS -b "$bids_cookie" "http://127.0.0.1:$bids_api_port/api/v1/projects/$bids_project_id" 2>/dev/null | jq -r '.status // empty' || true)"
        [[ "$bids_project_state" == READY || "$bids_project_state" == WAITING_FOR_APPROVAL ]] && break
        sleep 0.25
      done
      if [[ "$bids_project_state" == WAITING_FOR_APPROVAL ]]; then
        bids_approve_status="$(curl -sS -b "$bids_cookie" -H 'Content-Type: application/json' \
          -X POST "http://127.0.0.1:$bids_api_port/api/v1/projects/$bids_project_id/approve" \
          -d '{"approved_by":"m5-supervised-user","comment":"M5 supervised approval"}' \
          -o "$work_dir/bids-approve.json" -w '%{http_code}' || true)"
        [[ "$bids_approve_status" == 2* ]] || bids_project_state=""
      fi
      for _ in {1..240}; do
        [[ "$bids_project_state" == READY ]] && break
        bids_project_state="$(curl -sS -b "$bids_cookie" "http://127.0.0.1:$bids_api_port/api/v1/projects/$bids_project_id" 2>/dev/null | jq -r '.status // empty' || true)"
        [[ "$bids_project_state" == READY ]] && break
        sleep 0.25
      done
      if [[ "$bids_project_state" == READY ]]; then
        bids_tender_body='{"name":"M5 supervised tender","description":"M5 supervised business API","business_type":"RENOVATION_CONSTRUCTION","package_name":"M5 supervised package","package_description":""}'
        bids_tender_status="$(curl -sS -b "$bids_cookie" -H 'Content-Type: application/json' \
          -X POST "http://127.0.0.1:$bids_api_port/api/v1/projects/$bids_project_id/tenders" \
          -d "$bids_tender_body" -o "$work_dir/bids-tender.json" -w '%{http_code}' || true)"
        if [[ "$bids_tender_status" == 2* ]] && bids_tender_id="$(jq -er '.id' "$work_dir/bids-tender.json" 2>/dev/null)"; then
          bids_aggregate_id="$bids_tender_id"
          for _ in {1..80}; do
            bids_event_id="$(sqlite3 "$bids_db" "SELECT id FROM outbox_events WHERE event_type = 'TENDER_SUMMARY_CHANGED' AND aggregate_id = '$bids_aggregate_id' ORDER BY created_at DESC LIMIT 1")"
            [[ -n "$bids_event_id" ]] && break
            sleep 0.25
          done
          if [[ -n "$bids_event_id" ]]; then
            bids_source="business-api"
            echo "M5 supervised: Bids tender API created $bids_aggregate_id"
          else
            api_failed "Bids" "tender API returned no summary outbox row"
          fi
        else
          api_failed "Bids" "tender API HTTP $bids_tender_status; see $work_dir/bids-tender.json"
        fi
      else
        api_failed "Bids" "project did not reach READY (state=$bids_project_state)"
      fi
    else
      api_failed "Bids" "login/project HTTP $bids_login_status/$bids_project_status"
    fi
  else
    api_failed "Bids" "API process is not available"
  fi
fi

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

if [[ "$approver_source" == direct-outbox ]]; then
  approver_envelope="$(make_envelope approver approver.application.summary-changed Application "$approver_aggregate_id" "$approver_event_id" urn:record-hub:summary:application:v1 "$approver_payload")"
  psql -X -q -v ON_ERROR_STOP=1 -U "$pg_user" -d "$approver_db" \
    -v event_id="$approver_event_id" -v tenant_id="$tenant" -v aggregate_id="$approver_aggregate_id" -v payload="$approver_envelope" <<'SQL'
INSERT INTO integration_outbox (id, tenant_id, aggregate_type, aggregate_id, message_type, payload)
VALUES (:'event_id'::uuid, :'tenant_id', 'APPLICATION', :'aggregate_id'::uuid,
        'APPLICATION_SUMMARY_CHANGED', :'payload'::jsonb);
SQL
fi
if [[ "$fluxion_source" == direct-outbox ]]; then
  fluxion_envelope="$(make_envelope fluxion fluxion.project.summary-changed Project "$fluxion_aggregate_id" "$fluxion_event_id" urn:record-hub:summary:project:v1 "$fluxion_payload")"
  psql -X -q -v ON_ERROR_STOP=1 -U "$pg_user" -d "$fluxion_db" \
    -v event_id="$fluxion_event_id" -v tenant_id="$tenant" -v aggregate_id="$fluxion_aggregate_id" -v payload="$fluxion_envelope" <<'SQL'
INSERT INTO record_hub_outbox (id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, attempts)
VALUES (:'event_id'::uuid, :'tenant_id', 'Project', :'aggregate_id'::uuid,
        'fluxion.project.summary-changed', :'payload', 'PENDING', 0);
SQL
fi
if [[ "$bids_source" == direct-outbox ]]; then
  bids_envelope="$(make_envelope bids bids.tender.summary-changed Tender "$bids_aggregate_id" "$bids_event_id" urn:record-hub:summary:tender:v1 "$bids_payload")"
  sqlite3 "$bids_db" <<SQL
.parameter init
.parameter set :payload '$bids_envelope'
INSERT INTO outbox_events (id, organization_id, event_type, aggregate_type, aggregate_id, deduplication_key, payload, status, attempts, available_at, created_at, updated_at)
VALUES ('$bids_event_id', '$tenant', 'TENDER_SUMMARY_CHANGED', 'TENDER', '$bids_aggregate_id', 'record-hub:m5-supervised:$bids_aggregate_id', :payload, 'PENDING', 0, datetime('now'), datetime('now'), datetime('now'));
SQL
fi
echo "M5 supervised live gate: source triggers approver=$approver_source fluxion=$fluxion_source bids=$bids_source"

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
  projected="$(mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); print(d.records.countDocuments({workspaceId:'$workspace','source.id':{\$in:['$approver_aggregate_id','$fluxion_aggregate_id','$bids_aggregate_id']},projection:{\$exists:true}}))" | tr -d '[:space:]')"
  [[ "$projected" == 3 ]] && break
  sleep 0.25
done
[[ "$projected" == 3 ]] || { echo "expected 3 projected records, got $projected" >&2; exit 1; }
mongosh --quiet "$mongo_uri" --eval "const d=db.getSiblingDB('$database'); const rows=d.records.find({workspaceId:'$workspace','source.id':{\$in:['$approver_aggregate_id','$fluxion_aggregate_id','$bids_aggregate_id']}}).toArray(); if(rows.length!==3 || rows.some(r=>r.projection==null || r.projection.status!=='CURRENT')) quit(1); print('M5 supervised source outbox -> relay -> projection: PASS');"
echo "M5-059 supervised live producer -> relay -> projection gate: PASS (business API preferred; fallback mode=$api_trigger_mode)"
