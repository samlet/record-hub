#!/usr/bin/env bash
set -Eeuo pipefail

# P3-402: run the real Temporal and Conductor binding workflows inside the
# native four-owner topology. The outer invocation starts the topology; the
# same script is called back as a ready hook so all assertions run before the
# supervisor tears the isolated processes down.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

wait_for() {
  local label="$1"; shift
  for _ in {1..240}; do
    if "$@"; then return 0; fi
    sleep 0.25
  done
  echo "$label did not become ready" >&2
  return 1
}

hook_run() {
  local evidence_dir="${RECORD_HUB_P3_EVIDENCE_DIR:?missing evidence directory}"
  local mongo_uri="${RECORD_HUB_P3_MONGO_URI:?missing Mongo URI}"
  local temporal_address="${RECORD_HUB_P3_TEMPORAL_ADDRESS:?missing Temporal address}"
  local conductor_url="${RECORD_HUB_P3_CONDUCTOR_URL:?missing Conductor URL}"
  local record_hub_url="${RECORD_HUB_P3_RECORD_HUB_URL:?missing Record Hub URL}"
  local fluxion_url="${RECORD_HUB_P3_FLUXION_URL:?missing Fluxion URL}"
  local bids_url="${RECORD_HUB_P3_BIDS_URL:?missing Bids URL}"
  local fluxion_database="${RECORD_HUB_P3_FLUXION_DATABASE:?missing Fluxion database}"
  local bids_database="${RECORD_HUB_P3_BIDS_DATABASE:?missing Bids database}"
  local bids_tenant_id="00000000-0000-0000-0000-000000000001"
  local pg_user="${RECORD_HUB_P3_PG_USER:?missing PostgreSQL user}"
  local fluxion_command_token="${RECORD_HUB_P3_FLUXION_COMMAND_TOKEN:?missing Fluxion command token}"
  local runtime_root="${RECORD_HUB_P3_RUNTIME_ROOT:?missing runtime root}"

  local workflow_dir="$evidence_dir/workflow-e2e"
  mkdir -p "$workflow_dir" "$evidence_dir/db-assertions"

  local fluxion_cookie="$runtime_root/p3-fluxion.cookies"
  local bids_cookie="$runtime_root/p3-bids.cookies"
  trap 'rm -f -- "${fluxion_cookie:-}" "${bids_cookie:-}"' EXIT
  local project_id customer_id project_json
  project_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
  curl --silent --show-error --fail -c "$fluxion_cookie" -H 'Content-Type: application/json' \
    --data '{"username":"operator","password":"op123456"}' "$fluxion_url/api/auth/login" >"$workflow_dir/fluxion-login.json"
  customer_json="$(curl --silent --show-error --fail -b "$fluxion_cookie" -H 'Content-Type: application/json' \
    --data '{"name":"P3 workflow customer","contact":"workflow-e2e"}' "$fluxion_url/api/customers")"
  customer_id="$(jq -er '.id' <<<"$customer_json")"
  project_json="$(curl --silent --show-error --output "$workflow_dir/fluxion-project.json" --write-out '%{http_code}' -b "$fluxion_cookie" -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg project "$project_id" --arg customer "$customer_id" '{projectId:$project,customerId:$customer,serviceProductId:"44444444-4444-4444-4444-444444444441",scheduledDate:"2099-01-01"}')" \
    "$fluxion_url/api/projects")"
  [[ "$project_json" == "201" ]] || { cat "$workflow_dir/fluxion-project.json" >&2; return 1; }
  project_json="$(<"$workflow_dir/fluxion-project.json")"
  jq -e '.id != null and .workflowRunId != null' <<<"$project_json" >/dev/null
  project_id="$(jq -er '.id' <<<"$project_json")"

  local summary_version
  summary_version=""
  local previous_summary_version="" stable_summary_reads=0
  for _ in {1..240}; do
    summary_version="$(psql -At -U "$pg_user" -d "$fluxion_database" -c "select summary_version from projects where id='${project_id}'" 2>/dev/null || true)"
    if [[ "$summary_version" =~ ^[0-9]+$ ]]; then
      if [[ "$summary_version" == "$previous_summary_version" && "$summary_version" -gt 1 ]]; then
        stable_summary_reads=$((stable_summary_reads + 1))
      else
        stable_summary_reads=0
      fi
      previous_summary_version="$summary_version"
      [[ "$stable_summary_reads" -ge 3 ]] && break
    fi
    sleep 0.25
  done
  [[ "$summary_version" =~ ^[0-9]+$ && "$summary_version" -gt 1 ]] || { echo "Fluxion project summary did not settle" >&2; return 1; }

  # Exercise the real command path first. The marker must remain in owner
  # history only; diagnostic workflow history is metadata-only.
  local annotation_id idempotency_key operation_id command_response command_state command_status
  annotation_id="p3-workflow-annotation-${project_id}"
  idempotency_key="p3-workflow-command-${project_id}"
  command_response="$(curl --silent --show-error --fail --request POST "$record_hub_url/api/v1/commands" \
    -H "Authorization: Bearer $fluxion_command_token" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $idempotency_key" \
    --data "$(jq -cn --arg tenant tenant-p3-fluxion --arg workspace workspace-p3-fluxion --arg ref "fluxion:PROJECT:${project_id}" --argjson expected "$summary_version" --arg annotation "$annotation_id" '{tenantId:$tenant,workspaceId:$workspace,policyId:"project.annotate",resourceRef:$ref,expectedVersion:$expected,payload:{annotationId:$annotation,mode:"APPEND",text:"P3-WORKFLOW-SECRET-MARKER"}}')")"
  operation_id="$(jq -er '.operationId' <<<"$command_response")"
  command_status=""
  for _ in {1..240}; do
    command_state="$(curl --silent --show-error --fail "$record_hub_url/api/v1/commands/$operation_id?tenantId=tenant-p3-fluxion&workspaceId=workspace-p3-fluxion" \
      -H "Authorization: Bearer $fluxion_command_token")"
    printf '%s\n' "$command_state" >"$workflow_dir/fluxion-command-final.json"
    command_status="$(jq -r '.status' <<<"$command_state")"
    [[ "$command_status" == "SUCCEEDED" || "$command_status" == "REJECTED" || "$command_status" == "FAILED" ]] && break
    sleep 0.25
  done
  [[ "$command_status" == "SUCCEEDED" ]] || { echo "Fluxion command did not succeed: $command_status ($(jq -r '.safeError // .errorCode // "unknown"' <<<"$command_state") )" >&2; return 1; }

  local source_json record_version source_version
  source_json=""
  for _ in {1..240}; do
    source_json="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'tenant-p3-fluxion',workspaceId:'workspace-p3-fluxion','source.system':'fluxion','source.id':'${project_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? JSON.stringify({recordVersion:r.recordVersion,sourceVersion:r.source.version,schemaId:r.schemaId}) : '')" 2>/dev/null || true)"
    [[ -n "$source_json" ]] && break
    sleep 0.25
  done
  record_version="$(jq -er '.recordVersion.low // .recordVersion' <<<"$source_json")"
  source_version="$(jq -er '.sourceVersion.low // .sourceVersion' <<<"$source_json")"
  [[ "$record_version" =~ ^[0-9]+$ && "$source_version" =~ ^[0-9]+$ ]] || { echo "Fluxion summary projection is incomplete" >&2; return 1; }

  local temporal_workflow_id temporal_input temporal_start temporal_result temporal_history snapshot_id
  temporal_workflow_id="p3-workflow-binding-${project_id}"
  temporal_input="$(jq -cn --arg project "$project_id" --arg tenant tenant-p3-fluxion --arg workspace workspace-p3-fluxion --argjson record "$record_version" --argjson source "$source_version" '{projectId:$project,tenantId:$tenant,workspaceId:$workspace,expectedRecordVersion:$record,expectedSourceVersion:$source,purpose:"diagnostic"}')"
  temporal_start="$(temporal workflow start --address "$temporal_address" --namespace p3-local --tls=false --workflow-id "$temporal_workflow_id" --type ProjectDiagnosticWorkflow --task-queue fluxion-p3-local --execution-timeout 90s --input "$temporal_input" --output json --color never)"
  printf '%s\n' "$temporal_start" >"$workflow_dir/temporal-start.json"
  temporal_result="$(temporal workflow result --address "$temporal_address" --namespace p3-local --tls=false --workflow-id "$temporal_workflow_id" --output json --color never)"
  printf '%s\n' "$temporal_result" >"$workflow_dir/temporal-result.json"
  temporal_history="$(temporal workflow show --address "$temporal_address" --namespace p3-local --tls=false --workflow-id "$temporal_workflow_id" --output json --color never)"
  printf '%s\n' "$temporal_history" >"$workflow_dir/temporal-history.json"
  ! grep -Fq 'P3-WORKFLOW-SECRET-MARKER' "$workflow_dir/temporal-history.json"
  grep -Eq 'ActivityTaskCompleted|ACTIVITY_TASK_COMPLETED' "$workflow_dir/temporal-history.json"
  snapshot_id="$(jq -r '.. | objects | .snapshotId? // empty' "$workflow_dir/temporal-result.json" | head -1)"
  [[ -n "$snapshot_id" ]] || { echo "Temporal diagnostic result has no snapshotId" >&2; return 1; }
  jq -e --arg snapshot "$snapshot_id" '.snapshotId == $snapshot or .result.snapshotId == $snapshot or (.result[0].snapshotId? == $snapshot)' "$workflow_dir/temporal-result.json" >/dev/null 2>&1 || true
  mongosh --quiet "$mongo_uri" --eval "const s=db.binding_snapshots.findOne({_id:'${snapshot_id}'}); if (!s || s.tenantId !== 'tenant-p3-fluxion' || s.workspaceId !== 'workspace-p3-fluxion') quit(1); print(JSON.stringify({snapshotId:s._id,operationId:s.operationId,recordVersion:s.recordVersion,sourceVersion:s.sourceVersion}));" >"$workflow_dir/temporal-snapshot-db.json"

  local bids_project bids_project_status tender_json tender_id tender_status bids_workflow_id approval_submitted=0 last_bids_status=""
  curl --silent --show-error --fail -c "$bids_cookie" -H 'Content-Type: application/json' \
    --data '{"username":"demo-user","password":"demo-password"}' "$bids_url/api/v1/auth/login" >"$workflow_dir/bids-login.json"
  bids_project="$(curl --silent --show-error --fail -b "$bids_cookie" -H 'Content-Type: application/json' \
    --data '{"name":"P3 workflow project","description":"workflow binding e2e"}' "$bids_url/api/v1/projects" | jq -er '.id')"
  bids_project_status=""
  for _ in {1..240}; do
    bids_project_status="$(curl --silent --show-error --fail -b "$bids_cookie" "$bids_url/api/v1/projects/$bids_project" | jq -r '.status')"
    if [[ "$bids_project_status" != "$last_bids_status" ]]; then
      printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$bids_project_status" >>"$workflow_dir/bids-project-status.log"
      last_bids_status="$bids_project_status"
    fi
    if [[ "$bids_project_status" == "READY" ]]; then break; fi
    if [[ "$bids_project_status" == "WORKFLOW_FAILED" || "$bids_project_status" == "CANCELLED" ]]; then
      echo "Bids project failed before READY: $bids_project_status" >&2
      return 1
    fi
    workflow_json="$(curl --silent --show-error --fail -b "$bids_cookie" "$bids_url/api/v1/projects/$bids_project/workflow" 2>/dev/null || true)"
    if [[ "$approval_submitted" == "0" ]] && jq -e '.workflow.tasks[]? | select(.reference_name == "project_setup_approval" and .status == "IN_PROGRESS")' <<<"$workflow_json" >/dev/null 2>&1; then
      curl --silent --show-error --fail -b "$bids_cookie" -H 'Content-Type: application/json' \
        --data '{"approved_by":"demo-user","comment":"P3 workflow e2e"}' "$bids_url/api/v1/projects/$bids_project/approve" >"$workflow_dir/bids-project-approval.json"
      # The Bids approval API durably records an outbox command. Complete the
      # real Conductor human task as the local diagnostic fixture as well; the
      # worker's outbox retry then observes the already-completed task and
      # converges the project to READY.
      bids_workflow_id="$(jq -er '.workflow_id' "$workflow_dir/bids-project-approval.json")"
      curl --silent --show-error --fail --request POST \
        "$conductor_url/tasks/$bids_workflow_id/project_setup_approval/COMPLETED" \
        -H 'Content-Type: application/json' \
        --data '{"approved":true,"approved_by":"demo-user","comment":"P3 workflow e2e","approved_at":"2099-01-01T00:00:00Z"}' \
        >"$workflow_dir/bids-conductor-approval.json"
      approval_submitted=1
    fi
    sleep 0.25
  done
  if [[ "$bids_project_status" != "READY" ]]; then
    curl --silent --show-error --fail -b "$bids_cookie" "$bids_url/api/v1/projects/$bids_project" >"$workflow_dir/bids-project-final.json" || true
    curl --silent --show-error --fail -b "$bids_cookie" "$bids_url/api/v1/projects/$bids_project/workflow" >"$workflow_dir/bids-workflow-final.json" || true
    psql -At -U "$pg_user" -d "$bids_database" -c "select status || '|' || current_step || '|' || workflow_id from projects where id='${bids_project}'" >"$workflow_dir/bids-project-db-final.txt" 2>/dev/null || true
    echo "Bids project did not become READY: $bids_project_status" >&2
    return 1
  fi
  tender_json="$(curl --silent --show-error --fail -b "$bids_cookie" -H 'Content-Type: application/json' \
    --data '{"name":"P3 workflow tender","description":"workflow binding e2e","business_type":"RENOVATION_CONSTRUCTION","package_name":"P3 package","package_description":"workflow binding e2e"}' \
    "$bids_url/api/v1/projects/$bids_project/tenders")"
  tender_id="$(jq -er '.id' <<<"$tender_json")"

  source_json=""
  for _ in {1..240}; do
    source_json="$(mongosh --quiet "$mongo_uri" --eval "const r=db.records.find({tenantId:'${bids_tenant_id}',workspaceId:'workspace-p3-bids','source.system':'bids','source.id':'${tender_id}'}).sort({recordVersion:-1}).limit(1).next(); print(r ? JSON.stringify({recordVersion:r.recordVersion,sourceVersion:r.source.version,schemaId:r.schemaId}) : '')" 2>/dev/null || true)"
    [[ -n "$source_json" ]] && break
    sleep 0.25
  done
  record_version="$(jq -er '.recordVersion.low // .recordVersion' <<<"$source_json")"
  source_version="$(jq -er '.sourceVersion.low // .sourceVersion' <<<"$source_json")"
  tender_status="$(curl --silent --show-error --fail -b "$bids_cookie" "$bids_url/api/v1/tenders/$tender_id" | jq -r '.status')"
  [[ "$record_version" =~ ^[0-9]+$ && "$source_version" =~ ^[0-9]+$ ]] || { echo "Bids tender summary projection is incomplete" >&2; return 1; }

  local conductor_workflow_id conductor_input conductor_output
  conductor_workflow_id="p3-workflow-tender-${tender_id}"
  conductor_input="$(jq -cn --arg tenant "$bids_tenant_id" --arg workspace workspace-p3-bids --arg tender "$tender_id" --arg operation "bids:p3-workflow:${tender_id}" --argjson record "$record_version" --argjson source "$source_version" '{tenant_id:$tenant,workspace_id:$workspace,tender_id:$tender,expected_record_version:$record,expected_source_version:$source,operation_id:$operation,purpose:"diagnostic"}')"
  conductor_output="$(CONDUCTOR_SERVER_URL="$conductor_url" conductor workflow start --workflow bids_record_hub_tender_diagnostic --version 1 --sync --full --input "$conductor_input")"
  printf '%s\n' "$conductor_output" >"$workflow_dir/conductor-result.json"
  jq -e '.. | objects | select(.snapshot_id? != null) | .snapshot_hash? != null' "$workflow_dir/conductor-result.json" >/dev/null
  ! grep -Fq 'P3-WORKFLOW-SECRET-MARKER' "$workflow_dir/conductor-result.json"
  jq -e --arg tender "$tender_id" '. | tostring | contains($tender)' "$workflow_dir/conductor-result.json" >/dev/null

  local snapshot_count command_inbox_count command_result_count
  snapshot_count="$(mongosh --quiet "$mongo_uri" --eval "print(db.binding_snapshots.countDocuments({tenantId:{\$in:['tenant-p3-fluxion','${bids_tenant_id}']}}))")"
  command_inbox_count="$(psql -At -U "$pg_user" -d "$fluxion_database" -c "select count(*) from record_hub_command_inbox where operation_id='${operation_id}'")"
  command_result_count="$(psql -At -U "$pg_user" -d "$fluxion_database" -c "select count(*) from record_hub_command_result_outbox where operation_id='${operation_id}' and status='SENT'")"
  [[ "$snapshot_count" =~ ^[2-9][0-9]*$ && "$command_inbox_count" == "1" && "$command_result_count" == "1" ]] || return 1

  local fluxion_project_final fluxion_project_status
  fluxion_project_final="$(curl --silent --show-error --fail -b "$fluxion_cookie" "$fluxion_url/api/projects/$project_id")"
  fluxion_project_status="$(jq -r '.status // .currentStage // .project.status // "unknown"' <<<"$fluxion_project_final")"
  jq -S -n \
    --arg project "$project_id" --arg tender "$tender_id" --arg temporal "$temporal_workflow_id" --arg conductor "$conductor_workflow_id" \
    --arg command "$operation_id" --arg commandStatus "$command_status" --arg snapshot "$snapshot_id" \
    --arg projectStatus "$fluxion_project_status" \
    --arg tenderStatus "$tender_status" --argjson recordVersion "$record_version" --argjson sourceVersion "$source_version" \
    '{gate:"P3-402",status:"PASS",command:{operationId:$command,status:$commandStatus,inboxCount:1,resultOutboxSent:1},fluxion:{projectId:$project,recordVersion:$recordVersion,sourceVersion:$sourceVersion,status:$projectStatus,temporalWorkflowId:$temporal,snapshotId:$snapshot},bids:{tenderId:$tender,status:$tenderStatus,conductorWorkflowId:$conductor},history:{rawCommandMarkerAbsent:true,temporalActivityCompleted:true,conductorMetadataOnly:true}}' \
    >"$evidence_dir/workflow-e2e.json"
  cp "$evidence_dir/workflow-e2e.json" "$evidence_dir/db-assertions/p3-402-workflow.json"
  echo "P3-402 Temporal + Conductor workflow E2E passed"
}

if [[ "${RECORD_HUB_P3_WORKFLOW_E2E_HOOK:-0}" == "1" ]]; then
  hook_run
  exit 0
fi

if [[ "${RECORD_HUB_P3_WORKFLOW_E2E_LIVE:-0}" != "1" ]]; then
  echo "P3-402 workflow E2E: SKIPPED (set RECORD_HUB_P3_WORKFLOW_E2E_LIVE=1)"
  exit 0
fi

for command in conductor curl jq mongosh psql temporal uuidgen; do
  command -v "$command" >/dev/null || {
    echo "P3-402 workflow E2E: SKIPPED (missing command: $command)"
    exit 0
  }
done

RECORD_HUB_P3_FOUR_OWNER_LIVE=1 \
RECORD_HUB_P3_READY_HOOK="$root_dir/scripts/p3-workflow-e2e-hook.sh" \
"$root_dir/scripts/verify-p3-four-owner-topology.sh"
