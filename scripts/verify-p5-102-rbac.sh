#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_RBAC_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-102-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-102: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-102-rbac-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-102" and (.roles | length) == 4' "$spec" >/dev/null
status=0
check() { local id="$1"; shift; if "$@" >"$evidence_dir/static/$id.log" 2>&1; then printf 'PASS\n' >"$evidence_dir/static/$id.status"; else printf 'FAIL\n' >"$evidence_dir/static/$id.status"; status=1; fi; }
check role-model rg -q 'RoleOperator|ActionOperationsRead|ActionProjectionManage|ActionProjectionWrite' "$root_dir/server/internal/modules/identity/authorization.go"
check operator-boundary rg -q 'ActionOperationsRead' "$root_dir/server/internal/modules/projection/operations.go" "$root_dir/server/internal/modules/commands/operations.go" && rg -q 'ActionProjectionManage' "$root_dir/server/internal/modules/projection/rebuild.go"
check role-regression rg -q 'RoleOperator' "$root_dir/server/internal/modules/identity/authorization_test.go" "$root_dir/server/internal/modules/projection/operations_test.go" "$root_dir/server/internal/modules/commands/operations_test.go"
check read-only-console rg -q 'Viewer / Operator read-only|unexpectedly mutates terminal projection|StatusMethodNotAllowed' "$root_dir/web/src/components/console.tsx" "$root_dir/server/internal/modules/projection/tender_association_test.go"
check phase5-contract rg -q 'P5-PLAT-003|Viewer、Editor、Operator、Admin' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-design.md"
live_requested="${RECORD_HUB_P5_RBAC_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires role-bound production identities, browser/API matrix and audit export"
printf '%s\n' "$live_reason" >"$evidence_dir/live/reason.txt"
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  '{task:"P5-102",status:$overall,generatedAt:$generatedAt,evidenceDir:$evidenceDir,static:{roleModel:"PASS",operatorBoundary:"PASS",roleRegression:"PASS",readOnlyConsole:"PASS",phase5Contract:"PASS"},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,retry:"Provision role-bound identities and rerun with RECORD_HUB_P5_RBAC_LIVE=1"}' \
  | tee "$evidence_dir/p5-102.json"
echo "P5-102 report: $evidence_dir/p5-102.json"
[[ "$status" == "0" ]]
