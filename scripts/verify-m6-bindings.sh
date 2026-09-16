#!/usr/bin/env bash
set -euo pipefail

# Cross-repository M6 Binding gate. The default path is deterministic and does
# not pretend that an unavailable Mongo/Record Hub process is a live E2E pass.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUXION_DIR="${FLUXION_DIR:-/Users/xiaofeiwu/portals/fluxion}"
BIDS_DIR="${BIDS_DIR:-/Users/xiaofeiwu/apps/bids}"

echo "M6 Binding contract gate"
echo "- Fluxion Temporal tests"
(cd "$FLUXION_DIR/server" && ./gradlew test --tests 'fluxion.recordhub.BindingClientTest' --tests 'fluxion.workflow.ProjectDiagnosticWorkflowTest')
echo "- Bids Conductor tests"
(cd "$BIDS_DIR/backend" && go test ./internal/recordhub ./internal/conductor ./internal/workers)

if [[ "${RECORD_HUB_M6_LIVE:-0}" != "1" ]]; then
  echo "M6-067 live smoke: SKIPPED (set RECORD_HUB_M6_LIVE=1 after Record Hub API, Mongo and Dex are running)"
  exit 0
fi

command -v curl >/dev/null || { echo "curl is required for live smoke" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required for live smoke" >&2; exit 2; }
: "${RECORD_HUB_URL:?RECORD_HUB_URL is required for live smoke}"
: "${RECORD_HUB_TOKEN:?RECORD_HUB_TOKEN is required for live smoke}"
: "${RECORD_HUB_TENANT_ID:?RECORD_HUB_TENANT_ID is required for live smoke}"
: "${RECORD_HUB_WORKSPACE_ID:?RECORD_HUB_WORKSPACE_ID is required for live smoke}"
: "${RECORD_HUB_FLUXION_PROJECT_ID:?RECORD_HUB_FLUXION_PROJECT_ID is required for live smoke}"
: "${RECORD_HUB_BIDS_TENDER_ID:?RECORD_HUB_BIDS_TENDER_ID is required for live smoke}"

record_hub_url="${RECORD_HUB_URL%/}"
post_snapshot() {
  local ref="$1" schema="$2" record_version="$3" source_version="$4" operation_id="$5"
  local payload
  payload="$(jq -cn \
    --arg tenant "$RECORD_HUB_TENANT_ID" \
    --arg workspace "$RECORD_HUB_WORKSPACE_ID" \
    --arg ref "$ref" \
    --arg schema "$schema" \
    --arg purpose "diagnostic" \
    --argjson record "$record_version" \
    --argjson source "$source_version" \
    '{tenantId:$tenant,workspaceId:$workspace,recordRef:$ref,schemaId:$schema,schemaVersion:1,expectedRecordVersion:$record,expectedSourceVersion:$source,purpose:$purpose}')"
  curl -fsS "$record_hub_url/api/v1/bindings/snapshots" \
    -H "Authorization: Bearer $RECORD_HUB_TOKEN" \
    -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $operation_id" \
    --data "$payload"
}

verify_pair() {
  local label="$1" ref="$2" schema="$3" record_version="$4" source_version="$5" operation_id="$6"
  local first second first_id second_id
  first="$(post_snapshot "$ref" "$schema" "$record_version" "$source_version" "$operation_id")"
  first_id="$(jq -er '.snapshotId' <<<"$first")"
  jq -e --arg ref "$ref" --arg schema "$schema" \
    '.recordRef == $ref and .schemaId == $schema and (.snapshotHash | startswith("sha256:"))' <<<"$first" >/dev/null
  second="$(post_snapshot "$ref" "$schema" "$record_version" "$source_version" "$operation_id")"
  second_id="$(jq -er '.snapshotId' <<<"$second")"
  [[ "$first_id" == "$second_id" ]] || { echo "$label idempotent replay changed snapshotId" >&2; exit 1; }
  echo "$label snapshot/replay: passed ($first_id)"
  if [[ "${RECORD_HUB_M6_VERIFY_RESTART:-0}" == "1" ]]; then
    curl -fsS "$record_hub_url/api/v1/bindings/snapshots/$first_id?tenantId=$(printf '%s' "$RECORD_HUB_TENANT_ID" | jq -sRr @uri)&workspaceId=$(printf '%s' "$RECORD_HUB_WORKSPACE_ID" | jq -sRr @uri)" \
      -H "Authorization: Bearer $RECORD_HUB_TOKEN" | jq -e --arg id "$first_id" '.snapshotId == $id' >/dev/null
    echo "$label snapshot GET after operator restart: passed"
  fi
}

verify_pair "Fluxion" \
  "fluxion:PROJECT:${RECORD_HUB_FLUXION_PROJECT_ID}" \
  "urn:record-hub:summary:project:v1" \
  "${RECORD_HUB_FLUXION_RECORD_VERSION:-1}" \
  "${RECORD_HUB_FLUXION_SOURCE_VERSION:-0}" \
  "${RECORD_HUB_FLUXION_OPERATION_ID:-m6-live-fluxion-${RECORD_HUB_FLUXION_PROJECT_ID}}"
verify_pair "Bids" \
  "bids:TENDER:${RECORD_HUB_BIDS_TENDER_ID}" \
  "urn:record-hub:summary:tender:v1" \
  "${RECORD_HUB_BIDS_RECORD_VERSION:-1}" \
  "${RECORD_HUB_BIDS_SOURCE_VERSION:-0}" \
  "${RECORD_HUB_BIDS_OPERATION_ID:-m6-live-bids-${RECORD_HUB_BIDS_TENDER_ID}}"
echo "M6-067 live Binding smoke: passed"
