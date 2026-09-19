#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
evidence_dir="${RECORD_HUB_P5_TOPOLOGY_EVIDENCE_DIR:-$root_dir/build/evidence/phase5/native-topology-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date jq rg git; do
  command -v "$command" >/dev/null || { echo "P5 native topology: SKIPPED (missing command: $command)"; exit 0; }
done

spec="$root_dir/deploy/local/p5/p5-native-topology-spec.json"
jq -e '.version == 1 and .phase == "5" and .task == "P5-NATIVE-TOPOLOGY" and .liveRequired == true and .mode == "native-isolated-preflight" and (.owners | length) == 4 and (.dependencies | length) >= 8 and (.isolatedDefaults | length) >= 12 and (.invariants | length) >= 6 and (.notProductionEvidence | length) >= 4' "$spec" >/dev/null

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

check native-toolchain bash -c "
  for command in curl createdb dropdb go htpasswd java jq lsof mongod mongosh nats-server nc openssl psql temporal conductor mvn shasum; do command -v \"\$command\" >/dev/null || exit 1; done
"

check owner-repositories bash -c "
  test -d '$root_dir' && test -d '$approver_root' && test -d '$fluxion_root' && test -d '$bids_root' &&
  test -x '$fluxion_root/server/gradlew' && test -f '$approver_root/pom.xml' && test -f '$bids_root/backend/go.mod' &&
  test -x '$root_dir/scripts/verify-p3-four-owner-topology.sh' && test -x '$root_dir/scripts/bootstrap-p3-fixtures.sh'
"

check isolated-contract bash -c "
  jq -e '.isolatedDefaults.mongo == 37018 and .isolatedDefaults.nats == 14223 and .isolatedDefaults.temporal == 17233 and .isolatedDefaults.recordHub == 18081 and .isolatedDefaults.approverApi == 18090 and .isolatedDefaults.fluxion == 18092 and .isolatedDefaults.bidsApi == 18093' '$spec' >/dev/null &&
  rg -q 'temporary|never stops a process it did not start|never.*writes business rows directly' '$root_dir/scripts/verify-p3-four-owner-topology.sh' '$root_dir/docs/phase-3-batch4a-topology.md'
"

check artifact-and-minio-readiness bash -c "
  test -d '$root_dir/build/p4-release-candidate' && test \"\$(find '$root_dir/build/p4-release-candidate' -type f | wc -l | tr -d ' ')\" -ge 6 &&
  curl --silent --show-error --fail '${MINIO_ENDPOINT:-http://127.0.0.1:9000}/minio/health/live' >/dev/null
"

check no-ga-overclaim bash -c "
  rg -q 'notProductionEvidence|readiness-only|no-four-owner-fault-matrix|no-ga-signoff' '$spec' &&
  rg -q '不等价|不能.*通过|不得.*GA|SKIPPED' '$root_dir/docs/phase-5-batch5-closure.md' '$root_dir/docs/phase-5-batch4-closure.md' '$root_dir/docs/phase-5-acceptance-plan.md'
"

check owner-commit-boundary bash -c "
  git -C '$root_dir' rev-parse --verify HEAD >/dev/null &&
  git -C '$approver_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$fluxion_root' rev-parse --verify HEAD >/dev/null &&
  git -C '$bids_root' rev-parse --verify HEAD >/dev/null
"

live_requested="${RECORD_HUB_P5_TOPOLOGY_LIVE:-0}"
live_status="SKIPPED"
live_reason="set RECORD_HUB_P5_TOPOLOGY_LIVE=1 to run the native P3 four-owner supervisor on isolated ports; readiness is not GA evidence"
topology_evidence="$evidence_dir/p3-four-owner"
mkdir -p "$topology_evidence"
required=(RECORD_HUB_P5_TOPOLOGY_EVIDENCE_ROOT RECORD_HUB_P5_TOPOLOGY_MINIO_ENDPOINT)
missing=()
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if [[ "$live_requested" == "1" ]]; then
  live_reason="native supervisor readiness result is not installed in this workspace"
  if [[ "${#missing[@]}" == "0" ]]; then
    if RECORD_HUB_P3_FOUR_OWNER_LIVE=1 RECORD_HUB_P3_EVIDENCE_DIR="$topology_evidence" MINIO_ENDPOINT="$RECORD_HUB_P5_TOPOLOGY_MINIO_ENDPOINT" \
      "$root_dir/scripts/verify-p3-four-owner-topology.sh" >"$evidence_dir/live/native-topology.log" 2>&1; then
      if jq -e '.status == "PASS"' "$topology_evidence/manifest.json" >/dev/null 2>&1; then
        live_status="PASS"
        live_reason="native isolated four-owner readiness passed; this is a topology preflight only and not P4-501/P5 GA evidence"
      else
        live_status="FAIL"
        live_reason="native supervisor exited successfully without a PASS manifest"
      fi
    else
      live_status="FAIL"
      live_reason="native four-owner supervisor failed; inspect live/native-topology.log and preserved evidence"
    fi
  fi
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() {
  [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'
}
overall="PARTIAL"
[[ "$status" == "0" ]] || overall="FAIL"
if [[ "$live_status" == "FAIL" ]]; then overall="FAIL"; fi
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg tools "$(static_value native-toolchain)" \
  --arg repos "$(static_value owner-repositories)" \
  --arg isolated "$(static_value isolated-contract)" \
  --arg artifact "$(static_value artifact-and-minio-readiness)" \
  --arg noOverclaim "$(static_value no-ga-overclaim)" \
  --arg commits "$(static_value owner-commit-boundary)" \
  '{task:"P5-NATIVE-TOPOLOGY",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p5/p5-native-topology-spec.json",evidenceDir:$evidenceDir,static:{nativeToolchain:$tools,ownerRepositories:$repos,isolatedContract:$isolated,artifactAndMinioReadiness:$artifact,noGAOverclaim:$noOverclaim,ownerCommitBoundary:$commits},liveRequested:($liveRequested=="1"),liveStatus:$liveStatus,liveReason:$liveReason,topologyEvidenceDir:($evidenceDir+"/p3-four-owner"),missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Run with RECORD_HUB_P5_TOPOLOGY_LIVE=1 plus evidence root and MinIO endpoint; then attach the readiness manifest to the P4/P5 live harness"}' \
  | tee "$evidence_dir/p5-native-topology.json"
echo "P5 native topology report: $evidence_dir/p5-native-topology.json"
[[ "$status" == "0" && "$live_status" != "FAIL" ]]
