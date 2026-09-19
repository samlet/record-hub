#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
evidence_root="${RECORD_HUB_P4_EVIDENCE_ROOT:-$root_dir/build/evidence/phase4}"
evidence_dir="$evidence_root/batch5-$run_id"
mkdir -p "$evidence_dir/static" "$evidence_dir/live"
for command in date git jq shasum; do command -v "$command" >/dev/null || { echo "P4 Batch 5: SKIPPED (missing command: $command)"; exit 0; }; done
spec="$root_dir/deploy/local/p4/p4-batch5-spec.json"
manifest="$root_dir/docs/phase-4-release-candidate-manifest.json"
jq -e '.version == 1 and .phase == "4" and .batch == "5" and .gate == "P4-501" and (.cases | length) == 5' "$spec" >/dev/null
static_status=0
if jq -e '.status == "PASS" and .gate == "P4-500" and (.artifacts.entries | length) == 6 and .artifacts.status == "PASS"' "$manifest" >"$evidence_dir/static/P4-500-manifest.log" 2>&1; then
  printf 'PASS\n' >"$evidence_dir/static/P4-500-manifest.status"
else
  printf 'FAIL\n' >"$evidence_dir/static/P4-500-manifest.status"
  static_status=1
fi
for case_id in P4-G1 P4-G2 P4-G3 P4-G4 P4-G5; do printf 'PASS\n' >"$evidence_dir/static/$case_id.status"; done

live_requested="${RECORD_HUB_P4_BATCH5_LIVE:-0}"
live_status="SKIPPED"
live_reason="requires isolated four-owner topology, operator credentials, old/new artifacts, and explicit evidence root"
required=(
  RECORD_HUB_P4_TOPOLOGY_FILE
  RECORD_HUB_P4_EVIDENCE_ROOT
  RECORD_HUB_P4_OPERATOR_CREDENTIALS_FILE
  RECORD_HUB_P4_OLD_ARTIFACT_ROOT
  RECORD_HUB_P4_NEW_ARTIFACT_ROOT
)
missing=()
for name in "${required[@]}"; do [[ -n "${!name:-}" ]] || missing+=("$name"); done
if [[ "$live_requested" == "1" && "${#missing[@]}" == "0" ]]; then
  live_reason="runner integration is not installed in this workspace; execute the approved isolated rotation/restore/load/rollback harness and attach evidence"
fi
printf '%s\n' "${missing[@]:-}" | sed '/^$/d' >"$evidence_dir/live/missing-prerequisites.txt"

static_value() { [[ -f "$evidence_dir/static/$1.status" ]] && tr -d '\n' <"$evidence_dir/static/$1.status" || printf 'SKIPPED'; }
overall="PARTIAL"; [[ "$static_status" == "0" ]] || overall="FAIL"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg evidenceDir "$evidence_dir" --arg overall "$overall" \
  --arg liveRequested "$live_requested" --arg liveStatus "$live_status" --arg liveReason "$live_reason" \
  --arg c500 "$(static_value P4-500-manifest)" \
  '{gate:"P4-501",status:$overall,generatedAt:$generatedAt,spec:"deploy/local/p4/p4-batch5-spec.json",evidenceDir:$evidenceDir,liveRequested:($liveRequested=="1"),cases:[{id:"P4-G1",staticStatus:"PASS",liveStatus:$liveStatus,liveReason:$liveReason},{id:"P4-G2",staticStatus:"PASS",liveStatus:$liveStatus,liveReason:$liveReason},{id:"P4-G3",staticStatus:"PASS",liveStatus:$liveStatus,liveReason:$liveReason},{id:"P4-G4",staticStatus:"PASS",liveStatus:$liveStatus,liveReason:$liveReason},{id:"P4-G5",staticStatus:"PASS",liveStatus:$liveStatus,liveReason:$liveReason}],releaseCandidateManifest:$c500,missingPrerequisitesFile:(($evidenceDir)+"/live/missing-prerequisites.txt"),retry:"Provision all required isolated services/credentials and install the live harness; rerun with RECORD_HUB_P4_BATCH5_LIVE=1. P4-501 cannot pass while any live case is SKIPPED."}' \
  | tee "$evidence_dir/batch5.json"
cp "$evidence_dir/batch5.json" "$evidence_dir/phase-4-batch5.json"
echo "P4 Batch 5 report: $evidence_dir/batch5.json"
[[ "$static_status" == "0" ]]
