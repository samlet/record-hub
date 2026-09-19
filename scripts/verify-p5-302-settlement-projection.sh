#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_SETTLEMENT_PROJECTION_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-302-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-302: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-302-settlement-projection-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-302" and .liveRequired == true and .readModel == "settlement_associations" and (.allowedProjection | length) >= 14 and (.forbiddenProjection | length) >= 6' "$spec" >/dev/null

status=0
check() {
  local id="$1"; shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    status=1
  fi
}

check safe-model bash -c "
  go test ./server/internal/modules/projection -run 'TestSettlementAssociation' -count=1 &&
  rg -q 'SettlementAssociation|settlement_associations|SnapshotHash|ActionOutcome|ProjectionState' '$root_dir/server/internal/modules/projection/settlement_association.go' &&
  ! rg -n -i 'grossAmount|netAmount|bankAccount|invoiceAttachment|sealedData|rawSnapshot' '$root_dir/server/internal/modules/projection/settlement_association.go'
"

check scope-version-boundary bash -c "
  rg -q 'TenantID|WorkspaceID|Authorize|SourceVersion|ObservedVersion|AssociationGap|AssociationConflict' '$root_dir/server/internal/modules/projection/settlement_association.go' &&
  rg -q 'settlementRef|approvalRef|tenantId|workspaceId' '$root_dir/server/internal/modules/projection/settlement_association_http.go' '$root_dir/server/internal/app/runtime.go'
"

check mongo-index-bootstrap rg -q 'NewMongoSettlementAssociationRepository|settlement_association_scope_unique|EnsureIndexes' "$root_dir/server/internal/app/runtime.go" "$root_dir/server/internal/modules/projection/settlement_association.go"
check read-only-endpoint bash -c "
  rg -q 'GET /api/v1/associations/settlements' '$root_dir/server/internal/modules/projection/settlement_association_http.go' &&
  ! rg -n 'POST /api/v1/associations/settlements|PATCH /api/v1/associations/settlements|DELETE /api/v1/associations/settlements' '$root_dir/server/internal/modules/projection'
"
check phase5-contract rg -q 'P5-INT-003|safe association|Settlement|late result|金额|银行|附件|sealed' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md" "$root_dir/docs/phase-5-design.md"

live_requested="${RECORD_HUB_P5_SETTLEMENT_PROJECTION_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires live Settlement event publisher, Mongo projection/rebuild topology, late-result finding runner, and operator console evidence"
required=(RECORD_HUB_P5_SETTLEMENT_PROJECTION_TOPOLOGY_FILE RECORD_HUB_P5_SETTLEMENT_PROJECTION_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="Settlement projection live runner is not installed in this workspace; execute safe-projection/late-result/rebuild harness"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg model "$(static_value safe-model)" --arg scope "$(static_value scope-version-boundary)" --arg indexes "$(static_value mongo-index-bootstrap)" \
  --arg readOnly "$(static_value read-only-endpoint)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-302",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-302-settlement-projection-spec.json",evidenceDir:$evidenceDir,static:{safeModel:$model,scopeVersionBoundary:$scope,mongoIndexBootstrap:$indexes,readOnlyEndpoint:$readOnly,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision Settlement event/projection/rebuild topology and rerun with RECORD_HUB_P5_SETTLEMENT_PROJECTION_LIVE=1"}' \
  | tee "$evidence_dir/p5-302.json"
echo "P5-302 report: $evidence_dir/p5-302.json"
[[ "$status" == "0" ]]
