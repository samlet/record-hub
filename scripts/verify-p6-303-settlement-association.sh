#!/usr/bin/env bash
set -Eeuo pipefail

# P6-303: validate Settlement safe association and owner-only Apply boundary.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
spec="$root_dir/deploy/local/p6/p6-303-settlement-association-spec.json"
output="${RECORD_HUB_P6_SETTLEMENT_OUTPUT:-$root_dir/docs/phase-6-settlement-association.json}"
live_report="${RECORD_HUB_P6_SETTLEMENT_LIVE_REPORT:-}"
for command in date git jq rg python3; do command -v "$command" >/dev/null || { echo "P6-303 missing command: $command" >&2; exit 2; }; done
jq -e '.version == 1 and .phase == "6" and .task == "P6-303" and .liveTraffic == false and .failClosed == true and .applyBoundary.recordHubRole == "ASSOCIATION_AND_RECEIPT_ONLY" and .applyBoundary.ownerRole == "SETTLEMENT_APPLY" and .missingAction == "BLOCKED_BY_LIVE_CONNECTOR"' "$spec" >/dev/null

python3 - "$root_dir" "$approver_root" "$spec" "$output" "$live_report" <<'PY'
import json, pathlib, subprocess, sys
from datetime import datetime, timezone
root, approver, spec_path, output_path = map(pathlib.Path, sys.argv[1:5])
live_path = pathlib.Path(sys.argv[5]) if sys.argv[5] else None
spec = json.loads(spec_path.read_text())
def has(pattern, base):
    return subprocess.run(["rg", "-q", pattern, str(base)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
checks = {
    "settlementManifest": (root / "contracts/settlement/manifest.json").is_file(),
    "applySchema": (root / "contracts/settlement/confirmation/v1/apply-evidence.schema.json").is_file(),
    "safeAssociation": has("SettlementAssociation|snapshotHash|Apply", root / "server/internal/modules/projection"),
    "ownerOnlyEvidence": has("ownerOnly|forbiddenProjectionFields|sensitiveFieldsExcluded", root / "contracts/settlement/manifest.json"),
    "approverApplyBoundary": has("ApplySettlement|settlement.confirm|idempotency", approver / "approver-application/src/main/java"),
}
missing = [name for name, ok in checks.items() if not ok]
live_status, live_reason = "BLOCKED", "no immutable Settlement native Apply report supplied"
if live_path is not None and live_path.is_file():
    try:
        report = json.loads(live_path.read_text())
        if report.get("status") in {"PASS", "DONE"} and report.get("liveStatus") == "PASS": live_status, live_reason = "PASS", "Settlement native report is PASS"
        else: live_reason = "Settlement report exists but is not live PASS"
    except Exception as error: live_reason = f"Settlement report is invalid: {error}"
status = "PASS_STATIC" if not missing and live_status == "PASS" else spec["missingAction"]
result = {"schemaVersion":1,"phase":"6","task":"P6-303","status":status,"generatedAt":datetime.now(timezone.utc).isoformat().replace("+00:00","Z"),"liveTraffic":False,"sourceCommit":subprocess.check_output(["git","-C",str(root),"rev-parse","HEAD"],text=True).strip(),"static":checks,"missingStatic":missing,"live":{"status":live_status,"reason":live_reason,"report":str(live_path) if live_path else ""},"contract":spec["contract"],"safeAssociation":spec["safeAssociation"],"forbidden":spec["forbidden"],"applyBoundary":spec["applyBoundary"],"reconciliation":spec["reconciliation"],"decision":"DO_NOT_APPLY_SETTLEMENT_FACTS" if status != "PASS_STATIC" else "STATIC_READY_FOR_RELEASE_GATE","prerequisite":{"phase5":"INDEPENDENT_GATE","connectorEnablement":"NOT_GRANTED"},"next":"Run cross-process Settlement request/result/Apply with idempotency, rollback and redaction evidence."}
pathlib.Path(output_path).write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"task":"P6-303","status":status,"liveStatus":live_status,"missingStatic":missing},ensure_ascii=False))
PY
cat "$output"
