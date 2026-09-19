#!/usr/bin/env bash
set -Eeuo pipefail

# P3-408: verify the compatibility contract which is safe to run in this
# repository, then record whether a real rolling upgrade/rollback can be run.
# The live part is deliberately opt-in: it must never invent an old worker or
# point a restore/rollback exercise at a user's ordinary services.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P3_UPGRADE_EVIDENCE_DIR:-}"
if [[ -z "$evidence_dir" ]]; then
  evidence_dir="$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-408-evidence.XXXXXX")"
fi
mkdir -p "$evidence_dir"

if [[ "${RECORD_HUB_P3_UPGRADE_LIVE:-0}" != "1" ]]; then
  echo "P3-408 upgrade/rollback: SKIPPED (set RECORD_HUB_P3_UPGRADE_LIVE=1)"
  exit 0
fi

contract_status="PASS"
run_contract_check() {
  local name="$1"
  shift
  if "$@" >"$evidence_dir/${name}.log" 2>&1; then
    printf '%s\n' PASS >"$evidence_dir/${name}.status"
  else
    printf '%s\n' FAIL >"$evidence_dir/${name}.status"
    contract_status="FAIL"
  fi
}

run_contract_check go-test go test ./...
run_contract_check command-contract-mirrors "$root_dir/scripts/verify-p3-contract-mirrors.sh"
run_contract_check approval-contract-mirrors "$root_dir/scripts/verify-p3-approval-contract-mirrors.sh"
run_contract_check acceptance-compatibility rg -n -e 'additive DB migration' -e 'feature flag' -e '旧 worker' -e '旧版本' "$root_dir/docs/phase-3-acceptance-plan.md"

if [[ "$contract_status" != "PASS" ]]; then
  jq -n --arg evidence "$evidence_dir" \
    '{gate:"P3-408",status:"FAIL",contractChecks:"FAIL",liveChecks:"NOT_RUN",evidenceDir:$evidence,retry:"Fix the failed compatibility checks before attempting a rolling upgrade."}' \
    | tee "$evidence_dir/upgrade-rollback.json" "$evidence_dir/p3-408-upgrade-rollback.json" >/dev/null
  echo "P3-408 upgrade/rollback: FAIL (contract checks; evidence: $evidence_dir)" >&2
  exit 1
fi

missing=(
  "previous-version worker binary/image"
  "new-version worker binary/image"
  "isolated Mongo/PostgreSQL/NATS upgrade targets"
  "rollback runner with feature-flag fixture"
)
jq -n --arg evidence "$evidence_dir" \
  --argjson missing "$(printf '%s\n' "${missing[@]}" | jq -R . | jq -s .)" \
  '{gate:"P3-408",status:"SKIPPED",contractChecks:"PASS",liveChecks:"SKIPPED",missingArtifacts:$missing,evidenceDir:$evidence,retry:"Provide immutable old/new worker artifacts, isolated upgrade targets, and a rollback runner; execute the F3 sequence before marking DONE."}' \
  | tee "$evidence_dir/upgrade-rollback.json" "$evidence_dir/p3-408-upgrade-rollback.json" >/dev/null
echo "P3-408 upgrade/rollback: SKIPPED live (contract checks PASS; evidence: $evidence_dir)"
