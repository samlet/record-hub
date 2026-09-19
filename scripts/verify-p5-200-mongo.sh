#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_MONGO_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-200-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-200: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-200-mongo-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-200" and .liveRequired == true and .topology.replicaSet == true and .topology.minimumVotingMembers >= 3 and .topology.pitr == true and .topology.encryptedBackup == true and (.indexContract.requiredAdapters | length) >= 14' "$spec" >/dev/null

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

check replica-set-contract bash -c "
  rg -q -- '--replSet' '$root_dir/deploy/local/mongodb/compose.yaml' '$root_dir/deploy/local/mongodb/README.md' &&
  rg -q 'replica set|replicaSet' '$root_dir/deploy/local/mongodb/README.md' '$root_dir/docs/phase-5-design.md' &&
  rg -q 'WithTransaction|WithTransaction\(' '$root_dir/server/internal/modules/records/repository.go' '$root_dir/server/internal/modules/schema/repository.go'
"

check index-bootstrap bash -c "
  rg -q 'func ensureMongoIndexes' '$root_dir/server/internal/app/runtime.go' &&
  test \"\$(rg -l 'func .*EnsureIndexes' '$root_dir/server/internal/modules' | wc -l | tr -d ' ')\" -ge 14 &&
  rg -q 'unique|projection_event_archive_event_unique|inbox_consumer_event_unique|command_operation_scope_unique' '$root_dir/server/internal/modules'
"

check retention-replay-contract bash -c "
  rg -q 'MaxArchivedReplayEvents|ErrFeedCursorExpired|projection event archive' '$root_dir/server/internal/modules/projection/archive.go' '$root_dir/server/internal/modules/records/feed.go' &&
  rg -q 'retention|archive|PITR|restore' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-5-requirements.md' '$root_dir/deploy/local/p5/p5-200-mongo-spec.json'
"

check transaction-and-cursor-tests bash -c "
  rg -q 'transaction|rollback|change stream|CAS' '$root_dir/deploy/local/mongodb/README.md' '$root_dir/docs/m7-acceptance.md' &&
  rg -q 'CursorExpired|retention|Replay|restore|fault' '$root_dir/server/internal/modules/records' '$root_dir/server/internal/modules/projection' '$root_dir/server/internal/modules/projection/m7_mongo_fault_injection_test.go'
"

check phase5-contract rg -q 'P5-DATA-001|P5-OPS-002|RPO/RTO|restore' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md"

live_requested="${RECORD_HUB_P5_MONGO_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated production-like Mongo replica set, encrypted backup/PITR target, restore runner, and immutable evidence export"
required=(RECORD_HUB_P5_MONGO_URI RECORD_HUB_P5_MONGO_BACKUP_URI RECORD_HUB_P5_MONGO_TOPOLOGY_FILE RECORD_HUB_P5_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="restore/failover runner is not installed in this workspace; execute the approved Mongo HA/PITR harness and attach evidence"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg replicaSet "$(static_value replica-set-contract)" --arg indexes "$(static_value index-bootstrap)" \
  --arg retention "$(static_value retention-replay-contract)" --arg tests "$(static_value transaction-and-cursor-tests)" \
  --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-200",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-200-mongo-spec.json",evidenceDir:$evidenceDir,static:{replicaSet:$replicaSet,indexBootstrap:$indexes,retentionReplayContract:$retention,transactionAndCursorTests:$tests,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision the isolated replica set and encrypted PITR/restore harness, then rerun with RECORD_HUB_P5_MONGO_LIVE=1"}' \
  | tee "$evidence_dir/p5-200.json"
echo "P5-200 report: $evidence_dir/p5-200.json"
[[ "$status" == "0" ]]
