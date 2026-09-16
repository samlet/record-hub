#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

go test ./server/internal/modules/identity -run 'TestOIDCVerifier(ValidTokenAndRotation|RejectsInvalidClaims|ConfigFailsClosed)' -count=1

if [ "${RECORD_HUB_M7_DEX_LIVE:-0}" = "1" ]; then
  make dex-smoke
  echo "M7-075 live Dex discovery/PKCE smoke: PASS"
else
  echo "M7-075 live Dex smoke: SKIPPED (set RECORD_HUB_M7_DEX_LIVE=1 after make dex-up)"
fi

echo "M7-075 JWKS rotation/cache/outage gate passed"
