#!/usr/bin/env bash
set -Eeuo pipefail

# P6-402 native event evidence collector. It composes the existing native
# outage/recovery and owner fault-matrix gates, then validates the durable
# consumer/allowlisted subject evidence without changing the committed P6
# report or opening new event traffic.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P6_NATS_EVIDENCE_ROOT:-$root_dir/build/evidence/phase6/nats-event-live-$(date -u +%Y%m%dT%H%M%SZ)}"
recovery_dir="$evidence_dir/nats-recovery"
fault_dir="$evidence_dir/fault-matrix"

for command in date git jq rg; do
  command -v "$command" >/dev/null || { echo "P6 NATS evidence missing command: $command" >&2; exit 2; }
done

mkdir -p "$evidence_dir"
RECORD_HUB_P3_EVIDENCE_DIR="$recovery_dir" \
RECORD_HUB_P3_NATS_RECOVERY_LIVE=1 \
"$root_dir/scripts/verify-p3-nats-outage-recovery.sh"
RECORD_HUB_P3_RESTART_EVIDENCE_DIR="$fault_dir" \
RECORD_HUB_P3_RESTART_ACK_LIVE=1 \
"$root_dir/scripts/verify-p3-process-restart-ack-loss.sh"

recovery_report="$recovery_dir/nats-outage-recovery.json"
consumer_report="$recovery_dir/nats-outage/fluxion-consumer-final.json"
stream_report="$recovery_dir/nats-outage/domain-events-final.json"
fault_report="$fault_dir/restart-ack-loss.json"
for report in "$recovery_report" "$consumer_report" "$stream_report" "$fault_report"; do
  [[ -s "$report" ]] || { echo "P6 NATS evidence missing: $report" >&2; exit 1; }
done
! rg -n 'P3-WORKFLOW-SECRET-MARKER|poison' "$fault_dir/p3-110/dead-letters.txt" >/dev/null 2>&1 || {
  echo "P6 NATS evidence exposed a poison payload" >&2
  exit 1
}
jq -e '
  .status == "PASS" and
  .nats.outageObserved == true and
  .nats.restartedWithSameJetStreamStore == true and
  .outbox.unsentAfterRecovery == 0 and
  .projection.appliedInboxEvents == .outbox.totalAfterRecovery and
  .noDuplicateSideEffect == true
' "$recovery_report" >/dev/null
jq -e '
  .status == "PASS" and
  .cases.natsBacklogRecovery.jetStreamStoreReused == true and
  .cases.resultConsumerPause.replayedAfterRestart == true and
  .cases.deadLetter.safeMetadata == true and
  .cases.deadLetter.poisonPayloadRedacted == true and
  ([.cases | to_entries[].value.status] | all(. == "PASS"))
' "$fault_report" >/dev/null
jq -e '
  .config.ack_policy == "explicit" and
  .config.durable_name != null and
  .config.max_deliver >= 1 and
  .config.max_ack_pending >= 1 and
  .num_ack_pending == 0
' "$consumer_report" >/dev/null
jq -e '
  (.config.subjects | index("events.approver.>")) != null and
  (.config.subjects | index("events.fluxion.>")) != null and
  (.config.subjects | index("events.bids.>")) != null and
  (.config.subjects | index("events.record-hub.>")) != null
' "$stream_report" >/dev/null

recovery_json="$(jq -c . "$recovery_report")"
consumer_json="$(jq -c . "$consumer_report")"
stream_json="$(jq -c . "$stream_report")"
fault_json="$(jq -c . "$fault_report")"
jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --arg evidenceDir "$evidence_dir" \
  --argjson recovery "$recovery_json" \
  --argjson consumer "$consumer_json" \
  --argjson stream "$stream_json" \
  --argjson fault "$fault_json" \
  '{schemaVersion:1,phase:"6",task:"P6-402",status:"PASS_NATIVE_EVIDENCE",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,recovery:$recovery,consumer:$consumer,stream:$stream,faultMatrix:$fault,releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"RETAIN_P6_TASK_BLOCKER_UNTIL_PHASE4_PHASE5_GA"}' \
  >"$evidence_dir/p6-402-evidence.json"
jq -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --arg evidenceDir "$evidence_dir" \
  --argjson recovery "$recovery_json" \
  --argjson consumer "$consumer_json" \
  --argjson stream "$stream_json" \
  --argjson fault "$fault_json" \
  '{schemaVersion:1,phase:"6",task:"P6-402",status:"PASS",liveStatus:"PASS",generatedAt:$generatedAt,sourceCommit:$sourceCommit,evidenceDir:$evidenceDir,recovery:$recovery,consumer:$consumer,stream:$stream,faultMatrix:$fault,releaseStatus:"BLOCKED_BY_P4_P5_GA",decision:"EVIDENCE_READY_BUT_RETAIN_RELEASE_BLOCKER"}' \
  >"$evidence_dir/p6-402-live-report.json"

cat "$evidence_dir/p6-402-evidence.json"
echo "P6-402 native NATS outage/replay/DLQ evidence passed; committed task report remains blocked by the independent P4/P5 GA gate"
