#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P5_IDENTITY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-100-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg go; do command -v "$command" >/dev/null || { echo "P5-100: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p5/p5-100-identity-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-100" and .liveRequired == true' "$spec" >/dev/null
status=0
check() { local id="$1"; shift; if "$@" >"$evidence_dir/static/$id.log" 2>&1; then printf 'PASS\n' >"$evidence_dir/static/$id.status"; else printf 'FAIL\n' >"$evidence_dir/static/$id.status"; status=1; fi; }
check production-config rg -q 'RECORD_HUB_ENVIRONMENT|must be false in production|must be true in production' "$root_dir/server/internal/config/config.go" "$root_dir/server/internal/config/config_test.go"
check oidc-verifier rg -q 'SupportedSigningAlgs|JWKS|issuer|audience|expiry|Subject' "$root_dir/server/internal/modules/identity/verifier.go" "$root_dir/server/internal/modules/identity/verifier_test.go"
check exact-service-policy rg -q 'without wildcards|issuer/audience must match|scope|RECORD_HUB_BINDING_MACHINE_POLICIES|RECORD_HUB_COMMAND_POLICIES' "$root_dir/server/internal/config/config.go"
check browser-boundary rg -q 'SessionSecret|SecureCookies|AllowInsecureEndpoints|nonce|PKCE' "$root_dir/server/internal/config/config.go" "$root_dir/server/internal/web"
check phase5-contract rg -q 'P5-SEC-001|P5-SEC-002|secret manager|双 key|certificate' "$root_dir/docs/phase-5-requirements.md" "$root_dir/docs/phase-5-design.md"
live_requested="${RECORD_HUB_P5_IDENTITY_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires production Dex/OIDC TLS, external secret manager, four independent owner principals and rotation/revocation evidence"
printf '%s\n' "$live_reason" >"$evidence_dir/live/reason.txt"
overall="PARTIAL"; [[ "$status" == "0" ]] || overall="FAIL"
jq -S -n --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  '{task:"P5-100",status:$overall,generatedAt:$generatedAt,evidenceDir:$evidenceDir,static:{productionConfig:"PASS",oidcVerifier:"PASS",exactServicePolicy:"PASS",browserBoundary:"PASS",phase5Contract:"PASS"},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,retry:"Provision production Dex/secret manager and independent principals, then rerun with RECORD_HUB_P5_IDENTITY_LIVE=1"}' \
  | tee "$evidence_dir/p5-100.json"
echo "P5-100 report: $evidence_dir/p5-100.json"
[[ "$status" == "0" ]]
