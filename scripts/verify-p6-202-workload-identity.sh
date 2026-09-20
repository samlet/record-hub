#!/usr/bin/env bash
set -Eeuo pipefail

# P6-202: audit the existing exact workload policy boundary and identify the
# missing rotation/drain/revoke implementation. No credentials are generated.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-202-workload-identity-spec.json"
output="${RECORD_HUB_P6_WORKLOAD_IDENTITY_OUTPUT:-$root_dir/docs/phase-6-workload-identity.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-202 missing command: $command" >&2; exit 2; }
done
jq -e '.version == 1 and .phase == "6" and .task == "P6-202" and .liveTraffic == false and .failClosed == true and .principal.wildcards == "DENY" and .lifecycle.inFlightPolicy == "DRAIN_BEFORE_REVOKE" and .missingAction == "BLOCKED_BY_IDENTITY_LIFECYCLE_GAP"' "$spec" >/dev/null

static_status=0
check() {
  local id="$1"; shift
  if "$@" >/dev/null 2>&1; then
    printf 'PASS\n'
  else
    printf 'FAIL\n'
    static_status=1
  fi
}
issuer_status="$(check oidc-boundary rg -q 'NewOIDCVerifier|PrincipalService|Audience|SupportedSigningAlgs' "$root_dir/server/internal/modules/identity/verifier.go")"
policy_status="$(check exact-policy rg -q 'wildcard|without wildcards|CommandPolicyConfig|BindingMachinePolicyConfig' "$root_dir/server/internal/config/config.go" "$root_dir/server/internal/config/config_test.go")"
scope_status="$(check scoped-policy rg -q 'TenantID|WorkspaceID|OwnerSystem|ResourceType|Scope' "$root_dir/server/internal/config/config.go")"
rotation_status="$(check rotation-implementation bash -c "
  rg -q 'CANDIDATE' '$root_dir/server/internal' '$root_dir/contracts' &&
  rg -q 'DRAINING' '$root_dir/server/internal' '$root_dir/contracts' &&
  rg -q 'DRAIN_BEFORE_REVOKE' '$root_dir/server/internal' '$root_dir/contracts' &&
  rg -q 'REVOKE_OLD' '$root_dir/server/internal' '$root_dir/contracts'
")"

python3 - "$spec" "$root_dir" "$output" "$issuer_status" "$policy_status" "$scope_status" "$rotation_status" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

spec_path, root, output_path, issuer, policy, scope, rotation = sys.argv[1:]
spec = json.loads(pathlib.Path(spec_path).read_text())
static = {"oidcBoundary": issuer, "exactPolicy": policy, "scopedPolicy": scope, "rotationImplementation": rotation}
missing = []
for name, value in static.items():
    if value != "PASS":
        missing.append(name)
source_commit = subprocess.check_output(["git", "-C", root, "rev-parse", "HEAD"], text=True).strip()
status = "PASS_STATIC" if not missing else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-202",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "static": static,
    "missing": missing,
    "decision": "DO_NOT_ROTATE_OR_ENABLE_OWNER_CREDENTIALS" if missing else "ROTATION_POLICY_READY_FOR_ISOLATED_LIVE_TEST",
    "principalPolicy": spec["principal"],
    "lifecyclePolicy": spec["lifecycle"],
    "auditPolicy": spec["audit"],
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Implement owner-scoped candidate/dual-accept/drain/revoke state and audited fixtures, then rerun this gate.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-202", "status": status, "missing": missing}, ensure_ascii=False))
PY
cat "$output"
