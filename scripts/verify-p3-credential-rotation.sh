#!/usr/bin/env bash
set -Eeuo pipefail

# P3-405: prove the rotation primitives that are available locally, while
# refusing to claim a four-owner overlap gate until an issuer with reloadable
# signing keys and owner credential reload hooks is available.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P3_ROTATION_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-405-evidence.XXXXXX")}"
mkdir -p "$evidence_dir"

if [[ "${RECORD_HUB_P3_ROTATION_LIVE:-0}" != "1" ]]; then
  echo "P3-405 workload/JWKS/secret rotation: SKIPPED (set RECORD_HUB_P3_ROTATION_LIVE=1)"
  exit 0
fi

for command in go jq make; do
  command -v "$command" >/dev/null || {
    echo "P3-405 workload/JWKS/secret rotation: SKIPPED (missing command: $command)"
    exit 0
  }
done

set +e
go test ./server/internal/modules/identity -run 'TestOIDCVerifier(ValidTokenAndRotation|RejectsInvalidClaims|ConfigFailsClosed)' -count=1 >"$evidence_dir/oidc-rotation.log" 2>&1
oidc_status=$?
go test ./tools/workload-issuer -run TestIssuerReloadRotatesKeysAndReloadsClients -count=1 >"$evidence_dir/issuer-reload.log" 2>&1
issuer_status=$?
RECORD_HUB_P3_WORKLOAD_ISSUER_PORT="${RECORD_HUB_P3_WORKLOAD_ISSUER_PORT:-15577}" \
  make p2-workload-identity-smoke >"$evidence_dir/workload-secret-rotation.log" 2>&1
workload_status=$?
set -e

if [[ "$oidc_status" == "0" && "$issuer_status" == "0" && "$workload_status" == "0" ]]; then
  contract_status="PASS"
else
  contract_status="FAIL"
fi

jq -S -n \
  --arg contractStatus "$contract_status" --argjson oidcExit "$oidc_status" --argjson issuerExit "$issuer_status" --argjson workloadExit "$workload_status" \
  '{gate:"P3-405",status:"SKIPPED",contractChecks:{status:$contractStatus,oidcJwksRotation:{exitCode:$oidcExit},reloadableIssuer:{exitCode:$issuerExit},workloadSecretFailClosed:{exitCode:$workloadExit}},liveChecks:{status:"SKIPPED",reason:"The local workload issuer now supports SIGHUP key overlap and client-file reload, but the four-owner supervisor and owner clients still do not expose an in-place credential reload/revocation protocol."},retry:"Add owner credential reload hooks and revocation evidence, then rerun RECORD_HUB_P3_ROTATION_LIVE=1 ./scripts/verify-p3-credential-rotation.sh"}' \
  >"$evidence_dir/credential-rotation.json"
cp "$evidence_dir/credential-rotation.json" "$evidence_dir/p3-405-credential-rotation.json"

if [[ "$contract_status" != "PASS" ]]; then
  echo "P3-405 rotation contract checks failed (evidence: $evidence_dir)" >&2
  exit 1
fi
echo "P3-405 workload/JWKS/secret rotation: SKIPPED live overlap (contract checks PASS; evidence: $evidence_dir)"
