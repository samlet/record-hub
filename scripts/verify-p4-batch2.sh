#!/usr/bin/env bash
set -Eeuo pipefail

# P4-205: static contract/fault-matrix gate for the Fluxion Approval Beta.
# Live execution is deliberately opt-in and remains SKIPPED until the caller
# supplies an isolated four-owner topology and a dedicated evidence runner.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
evidence_dir="${RECORD_HUB_P4_BATCH2_EVIDENCE_DIR:-$root_dir/build/evidence/phase4/batch2-$run_id}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"

for command in date git jq rg; do
  command -v "$command" >/dev/null || { echo "P4 Batch 2: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p4/p4-batch2-spec.json"
jq -e '.version == 1 and .phase == "4" and .batch == "2" and .liveRequired == true and (.cases | length) == 5' "$spec" >/dev/null

static_status=0
run_static() {
  local id="$1"
  shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    static_status=1
  fi
}

run_static P4-205-response-loss bash -c "rg -q 'insertIgnore|stableKey|externalApprovalRequest' '$fluxion_root/server/src/main/kotlin/fluxion/approval/ExternalApproval.kt' && rg -q 'externalRequestId|decisionVersion' '$approver_root/approver-application/src/main/java'"
run_static P4-205-restart bash -c "test -x '$root_dir/scripts/verify-p3-process-restart-ack-loss.sh' && rg -q 'Inbox|restart|ACK' '$root_dir/scripts/verify-p3-process-restart-ack-loss.sh' && rg -q 'insertIgnore|Inbox' '$fluxion_root/server/src/main/kotlin/fluxion/approval/ExternalApproval.kt'"
run_static P4-205-nats-outage bash -c "test -x '$root_dir/scripts/verify-p3-nats-outage-recovery.sh' && rg -q 'JetStream|durable|outbox' '$root_dir/scripts/verify-p3-nats-outage-recovery.sh' && rg -q 'outbox|NATS|JetStream' '$fluxion_root/server/src/main/kotlin/fluxion/approval'"
run_static P4-205-late-result bash -c "rg -q 'VERSION_DRIFT|generation' '$fluxion_root/server/src/main/kotlin/fluxion/approval/ExternalApproval.kt' && rg -q 'VERSION_DRIFT|LATE_RESULT' '$approver_root/approver-application/src/main/java/com/xiaofeiwu/approver/application/integration'"
run_static P4-205-flag-rollback bash -c "rg -q 'externalApprovalEnabled|feature.*flag|rollout' '$fluxion_root/server/src/main/kotlin/fluxion' && rg -q 'drain|in.flight|inflight' '$fluxion_root/server/src/main/kotlin/fluxion'"
run_static P4-205-cross-repo-commits bash -c "git -C '$root_dir' rev-parse --verify HEAD >/dev/null && git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null && git -C '$approver_root' rev-parse --verify HEAD >/dev/null"

live_requested="${RECORD_HUB_P4_BATCH2_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated Record Hub/Fluxion/Approver/PostgreSQL/MongoDB/NATS/Temporal topology and an explicit evidence runner; ordinary local services are not accepted"

static_value() {
  local id="$1"
  if [[ -f "$evidence_dir/static/$id.status" ]]; then
    tr -d '\n' <"$evidence_dir/static/$id.status"
  else
    printf 'SKIPPED'
  fi
}

overall="PARTIAL"
if [[ "$static_status" != "0" ]]; then overall="FAIL"; fi
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg responseLoss "$(static_value P4-205-response-loss)" \
  --arg restart "$(static_value P4-205-restart)" \
  --arg natsOutage "$(static_value P4-205-nats-outage)" \
  --arg lateResult "$(static_value P4-205-late-result)" \
  --arg flagRollback "$(static_value P4-205-flag-rollback)" \
  --arg commits "$(static_value P4-205-cross-repo-commits)" \
  '{gate:"P4-205",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p4/p4-batch2-spec.json",evidenceDir:$evidenceDir,liveRequested:($liveRequested == "1"),cases:[
    {id:"response-loss",staticStatus:$responseLoss,liveStatus:$liveStatus,liveReason:$liveReason},
    {id:"restart",staticStatus:$restart,liveStatus:$liveStatus,liveReason:$liveReason},
    {id:"nats-outage",staticStatus:$natsOutage,liveStatus:$liveStatus,liveReason:$liveReason},
    {id:"late-result",staticStatus:$lateResult,liveStatus:$liveStatus,liveReason:$liveReason},
    {id:"flag-rollback",staticStatus:$flagRollback,liveStatus:$liveStatus,liveReason:$liveReason}],
    crossRepoCommitCheck:$commits,retry:"Provide the isolated topology and a dedicated runner, then rerun with RECORD_HUB_P4_BATCH2_LIVE=1"}' \
  | tee "$evidence_dir/batch2.json"
cp "$evidence_dir/batch2.json" "$evidence_dir/phase-4-batch2.json"
echo "P4 Batch 2 report: $evidence_dir/batch2.json"
[[ "$static_status" == "0" ]]
