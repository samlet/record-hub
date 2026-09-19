#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_SECURITY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/p5-400-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5-400: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-400-security-scan-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-400" and .liveRequired == true and (.repositories | sort) == ["approver", "bids", "fluxion", "record-hub"] and (.scanClasses.gitTree | length) >= 5 and (.scanClasses.safeProjection | length) >= 5 and (.scanClasses.supplyChain | length) >= 3 and (.scanClasses.runtimeArtifacts | length) >= 6 and (.liveEvidence | length) >= 5' "$spec" >/dev/null

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

check source-secret-scan bash -c "
  ! rg -n --hidden -I \
    -g '!.git/**' -g '!build/**' -g '!target/**' -g '!node_modules/**' -g '!web/.next/**' \
    -g '!c123.db' -g '!*.sum' -g '!*.lock' -g '!scripts/verify-p5-400-security.sh' \
    -e 'BEGIN (RSA|OPENSSH|EC) PRIVATE KEY' \
    -e 'AKIA[0-9A-Z]{16}' \
    -e 'gh[pousr]_[A-Za-z0-9]{20,}' \
    -e 'xox[baprs]-[A-Za-z0-9-]{20,}' \
    -e '(?i)bearer[[:space:]]+[A-Za-z0-9._~-]{24,}' \
    '$root_dir' '$approver_root' '$fluxion_root' '$bids_root'
"

check safe-projection-scan bash -c "
  ! rg -n -i 'grossAmount|netAmount|amountCents|bankAccount|bank_account|invoiceAttachment|invoice_attachment|sealedBid|sealed_bid|rawSnapshot|raw_snapshot|workflowSnapshot' \
    '$root_dir/server/internal/modules/projection/settlement_association.go' '$root_dir/server/internal/modules/projection/association.go' '$root_dir/server/internal/modules/projection/tender_association.go' 2>/dev/null &&
  rg -q 'safe projection|allowlist|redact|DisallowUnknownFields|SafeReason|payloadHash' \
    '$root_dir/docs/phase-5-design.md' '$root_dir/docs/phase-4-batch4-p4-403.md' '$approver_root/approver-application/src/main/java' '$fluxion_root/server/src/main/kotlin' '$bids_root/backend/internal/approval'
"

check owner-redaction-boundary bash -c "
  rg -q 'allowlist|safe.*projection|redact|forbidden' '$approver_root/approver-application/src/main/java' '$approver_root/approver-application/src/test/java' &&
  rg -q 'payloadHash|redact|safe|allowlist' '$fluxion_root/server/src/main/kotlin/fluxion/approval' &&
  rg -q 'DisallowUnknownFields|SafeReason|payloadHash|safe' '$bids_root/backend/internal/approval'
"

check supply-chain-contract bash -c "
  test -f '$root_dir/.gitleaks.toml' && test -x '$root_dir/scripts/scan-secrets.sh' &&
  test -f '$root_dir/go.sum' && git -C '$root_dir' ls-files --error-unmatch .gitleaks.toml scripts/scan-secrets.sh go.sum >/dev/null &&
  rg -q 'gitleaks|redact|no secret|secret scan' '$root_dir/scripts/scan-secrets.sh' '$root_dir/docs/phase-4-batch4-p4-403.md' '$root_dir/docs/phase-5-requirements.md'
"

check evidence-contract bash -c "
  test -d '$root_dir/contracts/evidence' &&
  rg -q 'hash|redact|scope|status|evidence' '$root_dir/contracts/evidence' '$root_dir/docs/phase-4-acceptance-plan.md' &&
  rg -q 'P5-SEC-003|P5-SEC-004|P5-OPS-003|secret|PII|sealed' '$root_dir/docs/phase-5-requirements.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check owner-commit-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_SECURITY_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated four-owner runtime histories/logs/metrics/DLQ/evidence exports, redaction scanner, and immutable artifact manifest"
required=(RECORD_HUB_P5_SECURITY_TOPOLOGY_FILE RECORD_HUB_P5_SECURITY_EVIDENCE_ROOT)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="runtime security scan runner is not installed in this workspace; export redacted histories/logs/metrics/DLQ/evidence and execute the approved scanner"
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
  --arg secrets "$(static_value source-secret-scan)" \
  --arg projection "$(static_value safe-projection-scan)" \
  --arg ownerRedaction "$(static_value owner-redaction-boundary)" \
  --arg supplyChain "$(static_value supply-chain-contract)" \
  --arg evidence "$(static_value evidence-contract)" \
  --arg commits "$(static_value owner-commit-boundary)" \
  '{task:"P5-400",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-400-security-scan-spec.json",evidenceDir:$evidenceDir,static:{sourceSecretScan:$secrets,safeProjectionScan:$projection,ownerRedactionBoundary:$ownerRedaction,supplyChainContract:$supplyChain,evidenceContract:$evidence,ownerCommitBoundary:$commits},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision the isolated runtime export/evidence scanner and rerun with RECORD_HUB_P5_SECURITY_LIVE=1"}' \
  | tee "$evidence_dir/p5-400.json"
echo "P5-400 report: $evidence_dir/p5-400.json"
[[ "$status" == "0" ]]
