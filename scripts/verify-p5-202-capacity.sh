#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_CAPACITY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-202-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-202: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-202-capacity-slo-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-202" and .liveRequired == true and .capacityBaseline.commandOperations >= 10000 and .capacityBaseline.steadyMessagesPerSecond >= 50 and .capacityBaseline.burstMessagesPerSecond >= 200 and .recovery.noUnverifiedClaim == true' "$spec" >/dev/null

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

check capacity-runner bash -c "
  test -x '$root_dir/scripts/verify-p3-capacity-baseline.sh' &&
  rg -q 'publish.*rate|drain|p50|p95|p99|latency' '$root_dir/scripts/verify-p3-capacity-baseline.sh' '$root_dir/docs/phase-3-acceptance-plan.md' '$root_dir/docs/phase-4-batch5-p4-503.md'
"

check bounded-slo-metrics bash -c "
  rg -q 'record_hub_projection_(backlog|lag_age_seconds|slo_breach|error_budget_remaining)' '$root_dir/server/internal/modules/projection' '$root_dir/docs/phase-2-slo-design.md' &&
  rg -q 'low-cardinality|bounded|tenant.*label|payload.*label' '$root_dir/server/internal/observability' '$root_dir/docs/phase-2-slo-design.md' '$root_dir/docs/phase-5-design.md'
"

check recovery-contract bash -c "
  rg -q 'RPO|RTO|restore|backlog|drain|resource' '$root_dir/docs/phase-3-acceptance-plan.md' '$root_dir/docs/phase-4-batch5-p4-503.md' '$root_dir/docs/phase-5-acceptance-plan.md' &&
  rg -q 'UNVERIFIED|SKIPPED|no.*claim|不能.*宣称|不得.*宣称' '$root_dir/docs/phase-4-batch5-p4-503.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check owner-boundary bash -c "
  rg -q 'four owner|Approver|Fluxion|Bids|Record Hub' '$root_dir/docs/phase-3-acceptance-plan.md' '$root_dir/docs/phase-5-design.md' &&
  rg -q 'metrics|backlog|lag|dead|finding|recovery' '$root_dir/docs/phase-3-requirements.md' '$root_dir/docs/phase-5-requirements.md'
"

check phase5-contract rg -q 'P5-OPS-002|capacity|RPO/RTO|SLO|UNVERIFIED' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-acceptance-plan.md"

live_requested="${RECORD_HUB_P5_CAPACITY_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated four-owner mixed load, production-like Mongo/NATS topology, dashboards/metrics export, and signed RPO/RTO evidence"
required=(RECORD_HUB_P5_CAPACITY_TOPOLOGY_FILE RECORD_HUB_P5_CAPACITY_EVIDENCE_ROOT RECORD_HUB_P5_OWNER_CREDENTIALS_FILE)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="four-owner mixed-load runner is not installed in this workspace; execute the approved capacity/recovery harness and attach signed evidence"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg runner "$(static_value capacity-runner)" --arg metrics "$(static_value bounded-slo-metrics)" \
  --arg recovery "$(static_value recovery-contract)" --arg owners "$(static_value owner-boundary)" --arg phase5 "$(static_value phase5-contract)" \
  '{task:"P5-202",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-202-capacity-slo-spec.json",evidenceDir:$evidenceDir,static:{capacityRunner:$runner,boundedSloMetrics:$metrics,recoveryContract:$recovery,ownerBoundary:$owners,phase5Contract:$phase5},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision the four-owner mixed-load and recovery harness, then rerun with RECORD_HUB_P5_CAPACITY_LIVE=1"}' \
  | tee "$evidence_dir/p5-202.json"
echo "P5-202 report: $evidence_dir/p5-202.json"
[[ "$status" == "0" ]]
