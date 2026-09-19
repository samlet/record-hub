#!/usr/bin/env bash
set -Eeuo pipefail

# P3-403: exercise the real Fluxion summary outbox across a complete NATS
# outage inside the native four-owner topology.  The owner API and Temporal
# worker create the project; this gate only observes PostgreSQL/Mongo/JetStream
# state and never writes business rows or publishes a synthetic event.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
recovery_pid=""

wait_for() {
  local label="$1"; shift
  for _ in {1..300}; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  echo "$label did not become ready" >&2
  return 1
}

hook_run() {
  local evidence_dir="${RECORD_HUB_P3_EVIDENCE_DIR:?missing evidence directory}"
  local mongo_uri="${RECORD_HUB_P3_MONGO_URI:?missing Mongo URI}"
  local fluxion_url="${RECORD_HUB_P3_FLUXION_URL:?missing Fluxion URL}"
  local fluxion_database="${RECORD_HUB_P3_FLUXION_DATABASE:?missing Fluxion database}"
  local pg_user="${RECORD_HUB_P3_PG_USER:?missing PostgreSQL user}"
  local runtime_root="${RECORD_HUB_P3_RUNTIME_ROOT:?missing runtime root}"
  local nats_url="${RECORD_HUB_P3_NATS_URL:?missing NATS URL}"
  local nats_port="${RECORD_HUB_P3_NATS_PORT:?missing NATS port}"
  local nats_monitor_port="${RECORD_HUB_P3_NATS_MONITOR_PORT:?missing NATS monitor port}"
  local nats_pid="${RECORD_HUB_P3_NATS_PID:?missing NATS pid}"
  local nats_store="${RECORD_HUB_P3_NATS_STORE:?missing NATS store}"
  local outage_dir="$evidence_dir/nats-outage"
  mkdir -p "$outage_dir"

  cleanup_recovery() {
    if [[ -n "${recovery_pid:-}" ]]; then
      kill -TERM "$recovery_pid" 2>/dev/null || true
      wait "$recovery_pid" 2>/dev/null || true
      recovery_pid=""
    fi
  }
  trap cleanup_recovery EXIT HUP INT TERM

  local cookie="$runtime_root/p3-nats-fluxion.cookies"
  trap 'rm -f -- "${cookie:-}"; cleanup_recovery' EXIT HUP INT TERM
  local customer_json customer_id project_http project_json project_id
  curl --silent --show-error --fail -c "$cookie" -H 'Content-Type: application/json' \
    --data '{"username":"operator","password":"op123456"}' "$fluxion_url/api/auth/login" >"$outage_dir/fluxion-login.json"
  customer_json="$(curl --silent --show-error --fail -b "$cookie" -H 'Content-Type: application/json' \
    --data '{"name":"P3 NATS outage customer","contact":"nats-outage"}' "$fluxion_url/api/customers")"
  printf '%s\n' "$customer_json" >"$outage_dir/customer.json"
  customer_id="$(jq -er '.id' <<<"$customer_json")"
  project_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"

  # The customer creates no summary event.  Stop the exact NATS process owned
  # by the topology before creating the project so every project summary is
  # forced through the durable owner outbox while the broker is unavailable.
  kill -TERM "$nats_pid" 2>/dev/null || true
  for _ in {1..100}; do
    kill -0 "$nats_pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$nats_pid" 2>/dev/null || true
  wait "$nats_pid" 2>/dev/null || true
  if lsof -nP -iTCP:"$nats_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "NATS did not stop cleanly" >&2
    return 1
  fi
  jq -n --arg stoppedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg natsUrl "$nats_url" \
    '{status:"DOWN",stoppedAt:$stoppedAt,natsUrl:$natsUrl}' >"$outage_dir/nats-down.json"

  project_http="$(curl --silent --show-error --output "$outage_dir/project-create.json" --write-out '%{http_code}' \
    -b "$cookie" -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg project "$project_id" --arg customer "$customer_id" '{projectId:$project,customerId:$customer,serviceProductId:"44444444-4444-4444-4444-444444444441",scheduledDate:"2099-01-01"}')" \
    "$fluxion_url/api/projects")"
  [[ "$project_http" == "201" ]] || { cat "$outage_dir/project-create.json" >&2; return 1; }
  project_json="$(<"$outage_dir/project-create.json")"
  [[ "$(jq -er '.id' <<<"$project_json")" == "$project_id" ]] || return 1

  local outbox_total=0 outbox_pending=0 outbox_attempts=0
  for _ in {1..300}; do
    IFS='|' read -r outbox_total outbox_pending outbox_attempts < <(
      psql -At -U "$pg_user" -d "$fluxion_database" -c \
        "select count(*), count(*) filter (where status in ('PENDING','PROCESSING')), coalesce(sum(attempts),0) from record_hub_outbox where aggregate_id='${project_id}'" \
        2>/dev/null || printf '0|0|0\n'
    )
    if [[ "$outbox_total" =~ ^[0-9]+$ && "$outbox_pending" =~ ^[0-9]+$ && "$outbox_total" -gt 0 && "$outbox_pending" -gt 0 && "$outbox_attempts" -gt 0 ]]; then
      break
    fi
    sleep 0.2
  done
  [[ "$outbox_total" -gt 0 && "$outbox_pending" -gt 0 ]] || {
    echo "Fluxion summary outbox did not expose an outage backlog" >&2
    return 1
  }
  psql -At -U "$pg_user" -d "$fluxion_database" -c \
    "select id,status,attempts,(payload::jsonb->>'aggregateVersion')::bigint from record_hub_outbox where aggregate_id='${project_id}' order by created_at,id" \
    >"$outage_dir/outbox-during-outage.tsv"

  # Restart with the same JetStream file store.  nats-init is deliberately
  # rerun so durable streams/consumers are verified rather than recreated.
  nats-server -js -a 127.0.0.1 -p "$nats_port" -m "$nats_monitor_port" -sd "$nats_store" -n record-hub-p3-four-owner-recovery \
    >"$evidence_dir/logs/nats-recovery.log" 2>&1 &
  recovery_pid="$!"
  wait_for "NATS recovery" curl --silent --show-error --fail "http://127.0.0.1:${nats_monitor_port}/healthz?js-enabled-only=true"
  (cd "$root_dir" && RECORD_HUB_NATS_URL="$nats_url" go run ./tools/nats-init) >"$evidence_dir/logs/nats-init-recovery.log" 2>&1
  nats --server "$nats_url" stream info --json DOMAIN_EVENTS >"$outage_dir/domain-events-after-recovery.json"
  nats --server "$nats_url" consumer info --json DOMAIN_EVENTS record-hub-fluxion-projection-v1 >"$outage_dir/fluxion-consumer-after-recovery.json"

  local unsent=1 sent=0 applied=0 record_version=0 max_version=0 outbox_total_after=0 stable_total=0 previous_total=-1
  for _ in {1..360}; do
    unsent="$(psql -At -U "$pg_user" -d "$fluxion_database" -c \
      "select count(*) from record_hub_outbox where aggregate_id='${project_id}' and status <> 'SENT'" 2>/dev/null || echo 1)"
    sent="$(psql -At -U "$pg_user" -d "$fluxion_database" -c \
      "select count(*) from record_hub_outbox where aggregate_id='${project_id}' and status='SENT'" 2>/dev/null || echo 0)"
    outbox_total_after="$(psql -At -U "$pg_user" -d "$fluxion_database" -c \
      "select count(*) from record_hub_outbox where aggregate_id='${project_id}'" 2>/dev/null || echo 0)"
    max_version="$(psql -At -U "$pg_user" -d "$fluxion_database" -c \
      "select coalesce(max((payload::jsonb->>'aggregateVersion')::bigint),0) from record_hub_outbox where aggregate_id='${project_id}'" 2>/dev/null || echo 0)"
    applied="$(mongosh --quiet "$mongo_uri" --eval "print(db.inbox_events.countDocuments({consumer:'record-hub-fluxion-projection-v1',tenantId:'tenant-p3-fluxion',workspaceId:'workspace-p3-fluxion',subject:'events.fluxion.project.summary-changed.v1',status:'APPLIED'}))" 2>/dev/null || echo 0)"
    record_version="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.findOne({tenantId:'tenant-p3-fluxion',workspaceId:'workspace-p3-fluxion','source.system':'fluxion','source.id':'${project_id}'}); print(r ? (r.source.version.low ?? r.source.version) : 0)" 2>/dev/null || echo 0)"
    if [[ "$unsent" == "0" && "$outbox_total_after" == "$previous_total" ]]; then
      stable_total=$((stable_total + 1))
    else
      stable_total=0
    fi
    previous_total="$outbox_total_after"
    [[ "$unsent" == "0" && "$stable_total" -ge 8 && "$applied" == "$outbox_total_after" && "$record_version" == "$max_version" ]] && break
    sleep 0.25
  done

  # Mongo's projection inbox is keyed by the NATS event id.  Use a bounded
  # aggregate query instead of trusting only the owner status transition.
  nats --server "$nats_url" stream info --json DOMAIN_EVENTS >"$outage_dir/domain-events-final.json"
  nats --server "$nats_url" consumer info --json DOMAIN_EVENTS record-hub-fluxion-projection-v1 >"$outage_dir/fluxion-consumer-final.json"

  jq -S -n \
    --arg project "$project_id" --argjson outboxDuringOutage "$outbox_total" --argjson outboxTotal "$outbox_total_after" --argjson outboxAttempts "$outbox_attempts" \
    --argjson sent "$sent" --argjson unsent "$unsent" --argjson applied "$applied" \
    --argjson recordVersion "$record_version" --argjson maxVersion "$max_version" \
    --arg stoppedAt "$(jq -r '.stoppedAt' "$outage_dir/nats-down.json")" \
    '{gate:"P3-403",status:(if ($unsent == 0 and $sent == $outboxTotal and $applied == $outboxTotal and $recordVersion == $maxVersion) then "PASS" else "FAIL" end),projectId:$project,nats:{outageObserved:true,restartedWithSameJetStreamStore:true},outbox:{totalDuringOutage:$outboxDuringOutage,totalAfterRecovery:$outboxTotal,attemptsDuringOutage:$outboxAttempts,unsentAfterRecovery:$unsent,sentAfterRecovery:$sent},projection:{appliedInboxEvents:$applied,currentRecordVersion:$recordVersion,maxOutboxVersion:$maxVersion},noDuplicateSideEffect:($recordVersion == $maxVersion),stoppedAt:$stoppedAt}' \
    >"$evidence_dir/nats-outage-recovery.json"
  cp "$evidence_dir/nats-outage-recovery.json" "$evidence_dir/db-assertions/p3-403-nats-outage.json"
  jq -e '.status == "PASS"' "$evidence_dir/nats-outage-recovery.json" >/dev/null || {
    cat "$evidence_dir/nats-outage-recovery.json" >&2
    return 1
  }
  echo "P3-403 NATS outage/backlog/recovery passed"
}

if [[ "${RECORD_HUB_P3_NATS_RECOVERY_HOOK:-0}" == "1" ]]; then
  hook_run
  exit 0
fi

if [[ "${RECORD_HUB_P3_NATS_RECOVERY_LIVE:-0}" != "1" ]]; then
  echo "P3-403 NATS outage/recovery: SKIPPED (set RECORD_HUB_P3_NATS_RECOVERY_LIVE=1)"
  exit 0
fi

for command in curl go jq lsof mongosh nats nats-server psql uuidgen; do
  command -v "$command" >/dev/null || {
    echo "P3-403 NATS outage/recovery: SKIPPED (missing command: $command)"
    exit 0
  }
done

RECORD_HUB_P3_FOUR_OWNER_LIVE=1 \
RECORD_HUB_P3_READY_HOOK="$root_dir/scripts/p3-nats-outage-recovery-hook.sh" \
"$root_dir/scripts/verify-p3-four-owner-topology.sh"
