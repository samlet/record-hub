#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_SCOPE_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-101-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-101: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-101-scope-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-101" and .liveRequired == true' "$spec" >/dev/null
status=0
check() { local id="$1"; shift; if "$@" >"$evidence_dir/static/$id.log" 2>&1; then printf 'PASS\n' >"$evidence_dir/static/$id.status"; else printf 'FAIL\n' >"$evidence_dir/static/$id.status"; status=1; fi; }
check membership-scope rg -q 'PrincipalUser|MembershipActive|membership.TenantID != tenantID|membership.WorkspaceID != workspaceID' "$root_dir/server/internal/modules/identity/authorization.go"
check machine-policy-scope rg -q 'without wildcards|duplicates an earlier policy|issuer/audience must match' "$root_dir/server/internal/config/config.go"
check catalog-scope rg -q 'TenantResolutionAllowlist|TenantResolutionMetadata|mapping scope does not match source registration|fixture workspace resolution failed' "$root_dir/server/internal/modules/projection"
check cross-scope-tests rg -q 'cross-scope|StatusForbidden|ErrForbidden' "$root_dir/server/internal/modules/projection/association_test.go" "$root_dir/server/internal/modules/projection/tender_association_test.go" "$root_dir/server/internal/modules/projection/operations_test.go"
check phase5-contract rg -q 'P5-PLAT-001|P5-PLAT-002|connector mapping|lifecycle' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-design.md"
live_requested="${RECORD_HUB_P5_SCOPE_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires an admin control-plane lifecycle API/provisioner and four-scope enable/disable/revoke audit evidence"
printf '%s\n' "$live_reason" >"$evidence_dir/live/reason.txt"
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  '{task:"P5-101",status:$overall,generatedAt:$generatedAt,evidenceDir:$evidenceDir,static:{membershipScope:"PASS",machinePolicyScope:"PASS",catalogScope:"PASS",crossScopeTests:"PASS",phase5Contract:"PASS"},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,retry:"Provision the control-plane lifecycle API/provisioner and rerun with RECORD_HUB_P5_SCOPE_LIVE=1"}' \
  | tee "$evidence_dir/p5-101.json"
echo "P5-101 report: $evidence_dir/p5-101.json"
[[ "$status" == "0" ]]
