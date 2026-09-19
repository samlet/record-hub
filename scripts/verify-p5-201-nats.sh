#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_NATS_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-201-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-201: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-201-nats-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-201" and .liveRequired == true and .productionTopology.streamReplicas >= 3 and .productionTopology.consumerReplicas >= 3 and .productionTopology.ackPolicy == "explicit" and .productionTopology.delivery == "pull" and (.productionTopology.streams | length) == 5 and (.productionTopology.consumers | length) == 7' "$spec" >/dev/null

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

check stream-contract bash -c "
  rg -q 'domainEventsStream|approvalCommandsStream|ownerCommandsStream|commandResultsStream|deadLettersStream' '$root_dir/tools/nats-init/main.go' &&
  rg -q 'jetstream.FileStorage|MaxAge|MaxMsgSize|Duplicates' '$root_dir/tools/nats-init/main.go' '$root_dir/deploy/local/nats/nats-server.conf' &&
  rg -q 'Replicas:\s+1' '$root_dir/tools/nats-init/main.go' &&
  jq -e '.localTopology.notProductionEvidence == true and .productionTopology.streamReplicas >= 3' '$spec' >/dev/null
"

check durable-consumer-contract bash -c "
  rg -q 'AckExplicitPolicy|DeliverAllPolicy|MaxDeliver|MaxAckPending|FilterSubject' '$root_dir/tools/nats-init/main.go' '$root_dir/server/internal/modules/projection/nats.go' &&
  rg -q 'approver-command-inbox-v1|fluxion-command-inbox-v1|bids-command-inbox-v1|record-hub-command-results-v1' '$root_dir/tools/nats-init/main.go' &&
  rg -q 'record-hub-approver-projection-v1|record-hub-fluxion-projection-v1|record-hub-bids-projection-v1' '$root_dir/tools/nats-init/main.go'
"

check dlq-and-replay-contract bash -c "
  rg -q 'NewNATSDeadLetterPublisher|DeadLetter|MaxDeliver|safe' '$root_dir/server/internal/modules/projection/nats.go' '$root_dir/server/internal/modules/projection/retry.go' &&
  rg -q 'duplicate|ACK|ack|replay|DLQ|dead' '$root_dir/server/internal/modules/projection/nats_test.go' '$root_dir/docs/event-contract.md' '$root_dir/docs/m8-recovery-runbook.md'
"

check owner-outbox-boundary bash -c "
  rg -q 'outbox|Outbox|NATS|JetStream' '$root_dir/docs/event-contract.md' '$root_dir/docs/phase-5-design.md' &&
  rg -q 'MaxDeliver|DLQ|durable|ACK' '$root_dir/deploy/local/nats/README.md' '$root_dir/docs/phase-3-requirements.md'
"

check phase5-contract rg -q 'P5-DATA-001|P5-OPS-002|duplicate|ACK|DLQ|replay' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md"

live_requested="${RECORD_HUB_P5_NATS_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated three-node NATS JetStream cluster, owner outbox processes, fault injection, and immutable evidence export"
required=(RECORD_HUB_P5_NATS_URL RECORD_HUB_P5_NATS_TOPOLOGY_FILE RECORD_HUB_P5_NATS_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="HA/replay runner is not installed in this workspace; execute the approved NATS cluster/outbox fault harness and attach evidence"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg streams "$(static_value stream-contract)" --arg consumers "$(static_value durable-consumer-contract)" \
  --arg dlq "$(static_value dlq-and-replay-contract)" --arg owners "$(static_value owner-outbox-boundary)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-201",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-201-nats-spec.json",evidenceDir:$evidenceDir,static:{streamContract:$streams,durableConsumerContract:$consumers,dlqAndReplayContract:$dlq,ownerOutboxBoundary:$owners,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision the isolated three-node JetStream/outbox fault harness, then rerun with RECORD_HUB_P5_NATS_LIVE=1"}' \
  | tee "$evidence_dir/p5-201.json"
echo "P5-201 report: $evidence_dir/p5-201.json"
[[ "$status" == "0" ]]
