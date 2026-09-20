#!/usr/bin/env bash
set -Eeuo pipefail

# P6-501: inventory connector onboarding policy and implementation facets. No
# connector is registered, enabled, or sent traffic by this static gate.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-501-connector-onboarding-spec.json"
output="${RECORD_HUB_P6_ONBOARDING_OUTPUT:-$root_dir/docs/phase-6-connector-onboarding.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-501 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-501" and
  .liveTraffic == false and .failClosed == true and
  .manifest.exactKey == ["connector", "event", "schemaVersion"] and
  .manifest.hashAlgorithm == "sha256" and
  .lifecycle.casRequired == true and
  .lifecycle.unknownTransitionAction == "REJECT" and
  .compatibility.outsideWindowAction == "REJECT" and
  .evidence.rawPayloadAction == "FORBIDDEN" and
  .missingAction == "BLOCKED_BY_ONBOARDING_API_GAP"
' "$spec" >/dev/null

python3 - "$root_dir" "$spec" "$output" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, spec_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())

def has(pattern, *paths):
    result = subprocess.run(
        ["rg", "-q", pattern, *map(str, paths)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return result.returncode == 0

registry = root / "server/internal/modules/connector"
manifest = root / "contracts/connectors/manifest.json"
api = root / "api/openapi.yaml"
console = root / "web/src/components/console.tsx"
evidence = root / "docs/phase-6-evidence-contract.json"
p200 = root / "deploy/local/p6/p6-200-connector-registry-spec.json"

checks = {
    "registryImplementation": has(r"Manifest|Transition|Resolve", registry),
    "manifestInventory": manifest.is_file() and has(r"contractHash|compatibilityMin|allowedFields", manifest),
    "exactKeyLifecycle": has(r"exact|DRAFT|ENABLED|DISABLED|optimistic-cas", p200),
    "evidenceContract": evidence.is_file() and has(r"sourceCommit|redaction|rollback|audit", evidence),
    "uploadApi": has(r"connector.*(onboard|register|review)|/connectors", api),
    "onboardingConsole": has(r"onboard|connector.*review|connector.*approval", console),
    "approvalAudit": has(r"ownerSignoff|reviewApproval|operatorAudit|APPEND_IMMUTABLE", spec_path),
}

missing = [name for name, value in checks.items() if not value]
source_commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
status = "PASS_STATIC" if not missing else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-501",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": checks,
    "missingStatic": missing,
    "identity": spec["identity"],
    "manifest": spec["manifest"],
    "lifecycle": spec["lifecycle"],
    "compatibility": spec["compatibility"],
    "evidence": spec["evidence"],
    "decision": "DO_NOT_ENABLE_SELF_SERVICE_ONBOARDING" if missing else "STATIC_ONBOARDING_READY_FOR_RELEASE_GATE",
    "prerequisite": spec["prerequisite"],
    "next": "Implement scoped connector onboarding API/console with upload validation, independent review, owner approval, CAS lifecycle and immutable redacted evidence.",
}
output_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-501", "status": status, "missingStatic": missing}, ensure_ascii=False))
PY

cat "$output"
