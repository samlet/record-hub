#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_CANARY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-401-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-401: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-401-canary-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-401" and .liveRequired == true and .prerequisite == "P4-501-live-all-gates-pass" and .canary.scope == "one-low-sensitivity-tenant-workspace" and .canary.newTrafficOnly == true and .canary.oldPathRetained == true and (.canary.observationWindow | length) == 3 and (.signals | length) >= 8 and (.stopConditions | length) >= 6 and (.liveEvidence | length) >= 5' "$spec" >/dev/null

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

check prerequisite-and-no-implicit-ga bash -c "
  rg -q 'P4-501|live gate|SKIPPED|不得.*GA|不得.*生产' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-4-batch5-closure.md' &&
  rg -q 'P4-501.*全部.*PASS|P4-501 live gate' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check canary-scope-and-retention bash -c "
  rg -q '一个低敏 tenant/workspace|一个明确 tenant/workspace|old worker|old artifact|local fallback|OFF|回退' '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-4-batch5-p4-502.md' '$root_dir/docs/phase-4-batch5-p4-504.md' &&
  rg -q 'retry|retention|reconciliation|每 1 分钟|观察窗口' '$root_dir/docs/phase-4-batch5-p4-502.md'
"

check observation-signals bash -c "
  jq -e '(.sloSignals | index(\"terminal-latency-p50-p95-p99\")) and (.sloSignals | index(\"projection-backlog\")) and (.sloSignals | index(\"delivery-retry-and-dead\")) and (.sloSignals | index(\"reconciliation-findings\")) and (.requiredEvidence | index(\"owner-signoff\"))' '$root_dir/deploy/local/p5/p5-202-capacity-slo-spec.json' >/dev/null &&
  rg -q 'terminal outcome|materialization lag|delivery retry/dead|projection backlog/DLQ|finding/recovery|safe-payload scan' '$root_dir/docs/phase-4-batch5-p4-502.md'
"

check stop-and-rollback bash -c "
  rg -q '停止 publisher|sealed-data|重复 terminal side effect|DLQ|回滚|保留旧 worker|old artifact' '$root_dir/docs/phase-4-batch4-runbook.md' '$root_dir/docs/phase-4-batch5-p4-502.md' &&
  rg -q 'stop|rollback|reconciliation|owner' '$root_dir/docs/phase-4-batch4-runbook.md'
"

check evidence-and-commit-boundary bash -c "
  rg -q 'evidence|metrics|redaction|retention|checksum|签字' '$root_dir/docs/phase-5-acceptance-plan.md' '$root_dir/docs/phase-5-requirements.md' &&
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_CANARY_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires P4-501 all-live PASS, one tenant/workspace allowlist, four-owner runtime, observation exporter, rollback harness, and owner signoff"
required=(RECORD_HUB_P5_CANARY_TOPOLOGY_FILE RECORD_HUB_P5_CANARY_EVIDENCE_ROOT RECORD_HUB_P5_CANARY_TENANT_WORKSPACE)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="canary/observation runner is not installed in this workspace; execute the retention/retry/reconciliation window and stop/rollback drill"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() {
  [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'
}
overall="PARTIAL"
[[ "$status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg prerequisite "$(static_value prerequisite-and-no-implicit-ga)" \
  --arg scope "$(static_value canary-scope-and-retention)" \
  --arg signals "$(static_value observation-signals)" \
  --arg rollback "$(static_value stop-and-rollback)" \
  --arg evidence "$(static_value evidence-and-commit-boundary)" \
  '{task:"P5-401",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-401-canary-spec.json",evidenceDir:$evidenceDir,static:{prerequisiteAndNoImplicitGA:$prerequisite,canaryScopeAndRetention:$scope,observationSignals:$signals,stopAndRollback:$rollback,evidenceAndCommitBoundary:$evidence},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Complete P4-501 live first, then provision canary topology and rerun with RECORD_HUB_P5_CANARY_LIVE=1"}' \
  | tee "$evidence_dir/p5-401.json"
echo "P5-401 report: $evidence_dir/p5-401.json"
[[ "$status" == "0" ]]
