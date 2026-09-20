#!/usr/bin/env bash
set -Eeuo pipefail

# P6-403 event-to-table evidence collector. The integration tests below use
# the locally installed MongoDB replica set and NATS JetStream; no Docker or
# synthetic business rows are used. The output is evidence-only and does not
# rewrite the committed P6-403 gate report.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P6_EVENT_TABLE_EVIDENCE_ROOT:-$root_dir/build/evidence/phase6/event-table-live-$(date -u +%Y%m%dT%H%M%SZ)}"
mongo_uri="${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}"
nats_url="${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}"

for command in date git go jq rg; do
  command -v "$command" >/dev/null || { echo "P6 event-table evidence missing command: $command" >&2; exit 2; }
done
mkdir -p "$evidence_dir"

run_test() {
  local name="$1" pattern="$2"
  (cd "$root_dir" && env RECORD_HUB_MONGODB_URI="$mongo_uri" RECORD_HUB_NATS_URL="$nats_url" \
    go test ./server/internal/modules/projection -run "$pattern" -count=1 -v) \
    >"$evidence_dir/$name.log" 2>&1
  grep -Eq -- '--- PASS:|^PASS$' "$evidence_dir/$name.log"
}

run_test mapping-recovery 'TestPublishedMappingJetStreamRecovery'
run_test rebuild-cas 'TestMongoRebuildRepositoryCASAndReceipt|TestProjectionRebuild'
run_test staged-replay 'TestMongoProjectionReplayStagingReadPointerAndLiveContinuation'
run_test mapping-contract 'TestMappingGenerationBuildResolveAndMapExactPublishedContract|TestMappingGenerationActivationRemovesRevokedEntryAtomically|TestMappingGenerationRejectsBuiltInOverrideAndRetainsLastGoodOnRefreshFailure|TestSummaryProjectorAppliesCatalogMappingAndRejectsAfterGenerationSwitch'
run_test gap-conflict 'TestAssociationProjectionIsMonotonicAndRepairsGaps|TestAssociationProjectionDetectsSameVersionConflict|TestInboxClaimIsIdempotentAndDetectsPayloadConflict'

for log in "$evidence_dir"/*.log; do
  ! rg -n 'P3-WORKFLOW-SECRET-MARKER|access_token|private_key|sealedBid' "$log" >/dev/null 2>&1 || {
    echo "P6 event-table evidence contains a forbidden secret marker: $log" >&2
    exit 1
  }
done

mapping_log="$(jq -Rs . "$evidence_dir/mapping-recovery.log")"
rebuild_log="$(jq -Rs . "$evidence_dir/rebuild-cas.log")"
replay_log="$(jq -Rs . "$evidence_dir/staged-replay.log")"
contract_log="$(jq -Rs . "$evidence_dir/mapping-contract.log")"
gap_log="$(jq -Rs . "$evidence_dir/gap-conflict.log")"
jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --arg evidenceDir "$evidence_dir" \
  --arg mongoUri "redacted-local-replica-set" \
  --arg natsUrl "redacted-local-jetstream" \
  --argjson mappingLog "$mapping_log" \
  --argjson rebuildLog "$rebuild_log" \
  --argjson replayLog "$replay_log" \
  --argjson contractLog "$contract_log" \
  --argjson gapLog "$gap_log" \
  '{schemaVersion:1,phase:"6",task:"P6-403",status:"PASS_NATIVE_EVIDENCE",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,environment:{mongo:$mongoUri,nats:$natsUrl},checks:{mappingRecovery:"PASS",rebuildCASAndReceipt:"PASS",stagedReplayAndPointerSwitch:"PASS",mappingGenerationContract:"PASS",gapConflictAndInboxPayloadConflict:"PASS"},logs:{mappingRecovery:$mappingLog,rebuildCAS:$rebuildLog,stagedReplay:$replayLog,mappingContract:$contractLog,gapConflict:$gapLog},releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"RETAIN_P6_TASK_BLOCKER_UNTIL_PHASE4_PHASE5_GA"}' \
  >"$evidence_dir/p6-403-evidence.json"
jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --arg evidenceDir "$evidence_dir" \
  --argjson evidence "$(jq -c . "$evidence_dir/p6-403-evidence.json")" \
  '{schemaVersion:1,phase:"6",task:"P6-403",status:"PASS",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,evidence:$evidence,releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"EVIDENCE_READY_BUT_RETAIN_RELEASE_BLOCKER"}' \
  >"$evidence_dir/p6-403-live-report.json"

cat "$evidence_dir/p6-403-evidence.json"
echo "P6-403 native mapping/rebuild/gap/conflict evidence passed; committed task report remains blocked by the independent P4/P5 GA gate"
