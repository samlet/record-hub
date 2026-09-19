#!/usr/bin/env bash
set -Eeuo pipefail

# P3-404: promote the existing real owner command fault matrix to a Batch 4
# gate.  The underlying gate injects failures at owner commit-before-ACK,
# result publish-before-SENT, consumer pause, cross-process restart, durable
# consumer competition, and poison-result/DLQ boundaries.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${RECORD_HUB_P3_RESTART_ACK_LIVE:-0}" != "1" ]]; then
  echo "P3-404 process restart/ACK-loss: SKIPPED (set RECORD_HUB_P3_RESTART_ACK_LIVE=1)"
  exit 0
fi

for command in curl go jq lsof mongod mongosh nats nats-server nc openssl psql temporal uuidgen; do
  command -v "$command" >/dev/null || {
    echo "P3-404 process restart/ACK-loss: SKIPPED (missing command: $command)"
    exit 0
  }
done

evidence_dir="${RECORD_HUB_P3_RESTART_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-404-evidence.XXXXXX")}"
mkdir -p "$evidence_dir"
underlying_evidence="$evidence_dir/p3-110"
mkdir -p "$underlying_evidence"

set +e
RECORD_HUB_P3_FAULT_MATRIX=1 \
RECORD_HUB_P3_EVIDENCE_DIR="$underlying_evidence" \
"$root_dir/scripts/verify-p3-fluxion-command-live.sh" >"$evidence_dir/p3-110.log" 2>&1
underlying_status=$?
set -e

if [[ -f "$underlying_evidence/results.json" ]]; then
  jq -S '{sourceGate:"P3-110",status:(if ((.cases.faultMatrix | type == "object") and all(.cases.faultMatrix[]; .status == "PASS")) then "PASS" else "FAIL" end),cases:.cases.faultMatrix,projectId:.projectId,runtimeSeconds:.runtimeSeconds}' \
    "$underlying_evidence/results.json" >"$evidence_dir/restart-ack-loss.json"
else
  jq -n --arg status "$underlying_status" \
    '{sourceGate:"P3-110",status:"FAIL",underlyingExitCode:($status|tonumber),note:"underlying fault matrix did not produce results.json"}' \
    >"$evidence_dir/restart-ack-loss.json"
fi
cp "$evidence_dir/restart-ack-loss.json" "$evidence_dir/p3-404-restart-ack-loss.json"

if [[ "$underlying_status" != "0" ]] || ! jq -e '.status == "PASS"' "$evidence_dir/restart-ack-loss.json" >/dev/null; then
  echo "P3-404 process restart/ACK-loss: FAIL (evidence: $evidence_dir)" >&2
  exit 1
fi
echo "P3-404 process restart/ACK-loss matrix passed (evidence: $evidence_dir)"
