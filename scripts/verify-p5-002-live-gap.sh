#!/usr/bin/env bash
set -Eeuo pipefail

# P5-002: fail-closed re-audit of P4-501 before Phase 5 can advance. This
# script never starts a service, changes a gate report, or clears a blocker.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_P4_REAUDIT_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-002-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date find git jq rg sort; do
  command -v "$command" >/dev/null || { echo "P5-002: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-002-live-gap-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-002" and .prerequisite == "P4-501" and .failClosed == true and (.requiredCases | length) == 5 and (.forbiddenStatuses | sort) == ["PARTIAL", "PENDING", "SKIPPED", "UNVERIFIED"]' "$spec" >/dev/null

static_status=0
check() {
  local id="$1"; shift
  if "$@" >"$evidence_dir/static/$id.log" 2>&1; then
    printf 'PASS\n' >"$evidence_dir/static/$id.status"
  else
    printf 'FAIL\n' >"$evidence_dir/static/$id.status"
    static_status=1
  fi
}

check phase4-contract bash -c "
  rg -q 'P4-501|P4-G1|P4-G5|SKIPPED' '$root_dir/docs/phase-4-batch5-p4-501.md' '$root_dir/docs/phase-4-batch5-closure.md' &&
  jq -e '.version == 1 and .phase == \"4\" and .batch == \"5\" and .gate == \"P4-501\" and (.cases | length) == 5' '$root_dir/deploy/local/p4/p4-batch5-spec.json' >/dev/null
"
check fail-closed-contract bash -c "
  rg -q 'BLOCKED_BY_P4|fail.closed|P4-501' '$root_dir/docs/phase-5-task-breakdown.md' '$root_dir/docs/phase-5-acceptance-plan.md' '$spec' &&
  ! rg -n '(^|[[:space:]])(kubectl|helm|launchctl|systemctl)([[:space:]]|$)' '$root_dir/scripts/verify-p5-002-live-gap.sh'
"
check owner-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}' rev-parse --verify HEAD >/dev/null &&
  git -C '${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}' rev-parse --verify HEAD >/dev/null &&
  git -C '${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}' rev-parse --verify HEAD >/dev/null
"

report="${RECORD_HUB_P5_P4_REPORT:-}"
if [[ -z "$report" ]]; then
  report="$(find "${RECORD_HUB_P4_EVIDENCE_ROOT:-$root_dir/build/evidence/phase4}" -type f \( -name 'batch5.json' -o -name 'phase-4-batch5.json' \) -print 2>/dev/null | sort | tail -1 || true)"
fi
live_status="BLOCKED"
live_reason="no P4-501 evidence report supplied; all five live cases remain unverified"
if [[ -n "$report" && -f "$report" ]]; then
  cp "$report" "$evidence_dir/live/p4-501-report.json"
  if jq -e '.gate == "P4-501" and (.status == "PASS" or .status == "DONE") and (.cases | length) == 5 and all(.cases[]; (.liveStatus == "PASS" or .status == "PASS"))' "$report" >/dev/null 2>&1; then
    live_status="PASS"
    live_reason="P4-501 report proves all five live cases PASS"
  else
    live_reason="P4-501 report exists but at least one live case is not PASS"
  fi
else
  printf '%s\n' "$live_reason" >"$evidence_dir/live/missing-report.txt"
fi

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="BLOCKED_BY_P4"
[[ "$static_status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg report "$report" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg contract "$(static_value phase4-contract)" --arg failClosed "$(static_value fail-closed-contract)" --arg owner "$(static_value owner-boundary)" \
  '{task:"P5-002",status:$overall,decision:(if $overall == "BLOCKED_BY_P4" then "DO_NOT_CLEAR_P4_BLOCKER" else "STOP" end),generatedAt:$generatedAt,spec:"deploy/local/p5/p5-002-live-gap-spec.json",evidenceDir:$evidenceDir,static:{phase4Contract:$contract,failClosedContract:$failClosed,ownerBoundary:$owner},p4Report:$report,liveStatus:$liveStatus,liveReason:$liveReason,retry:"Complete and attach an immutable P4-501 report with P4-G1..G5 liveStatus PASS, then rerun this re-audit"}' \
  | tee "$evidence_dir/p5-002.json"
echo "P5-002 report: $evidence_dir/p5-002.json"
[[ "$static_status" == "0" ]]
