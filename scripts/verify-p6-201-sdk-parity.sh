#!/usr/bin/env bash
set -Eeuo pipefail

# P6-201: inventory source markers for SDK parity without compiling or
# publishing SDK artifacts. Missing facets are reported as a blocker.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
spec="$root_dir/deploy/local/p6/p6-201-sdk-parity-spec.json"
output="${RECORD_HUB_P6_SDK_PARITY_OUTPUT:-$root_dir/docs/phase-6-sdk-parity.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-201 missing command: $command" >&2; exit 2; }
done
jq -e '.version == 1 and .phase == "6" and .task == "P6-201" and .liveTraffic == false and .failClosed == true and .mode == "SOURCE_MARKER_INVENTORY_ONLY" and .missingAction == "BLOCKED_BY_SDK_GAP"' "$spec" >/dev/null

python3 - "$root_dir" "$approver_root" "$fluxion_root" "$bids_root" "$spec" "$output" <<'PY'
import json
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone

record_hub, approver, fluxion, bids, spec_path, output_path = map(pathlib.Path, sys.argv[1:])
spec = json.loads(spec_path.read_text())
roots = {"record-hub": record_hub, "approver": approver, "fluxion": fluxion, "bids": bids}
extensions = {"go": {".go"}, "java": {".java"}, "kotlin": {".kt", ".kts"}, "typescript": {".ts", ".tsx"}}
excluded = {"target", "build", "node_modules", ".git", ".next", "dist"}

def source_files(base: pathlib.Path, language: str):
    if not base.exists():
        return []
    return [
        path for path in sorted(base.rglob("*"))
        if path.is_file() and path.suffix in extensions[language] and not any(part in excluded for part in path.parts)
    ]

facets = spec["facets"]
rows = []
missing = []
for system in spec["systems"]:
    owner = system["owner"]
    language = system["language"]
    path = roots[owner] / system["path"]
    files = source_files(path, language)
    text = "\n".join(file.read_text(errors="ignore") for file in files)
    present = {}
    for facet, markers in facets.items():
        present[facet] = any(re.search(marker, text, re.IGNORECASE) for marker in markers)
        if system["required"] and not present[facet]:
            missing.append({"owner": owner, "language": language, "path": system["path"], "facet": facet})
    rows.append({
        "owner": owner,
        "language": language,
        "path": system["path"],
        "exists": path.exists(),
        "sourceFiles": len(files),
        "facets": present,
    })

source_commit = subprocess.check_output(["git", "-C", str(record_hub), "rev-parse", "HEAD"], text=True).strip()
status = "PASS_STATIC" if not missing else spec["missingAction"]
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-201",
    "status": status,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommit": source_commit,
    "mode": spec["mode"],
    "facets": list(facets),
    "systems": rows,
    "missing": missing,
    "decision": "DO_NOT_RELEASE_SDK_PARITY" if missing else "STATIC_PARITY_MARKERS_PRESENT",
    "prerequisite": {"phase5": "INDEPENDENT_GATE", "connectorEnablement": "NOT_GRANTED"},
    "next": "Add the missing typed-ref/command-result/receipt/error facets in owner SDKs, then rerun this inventory and add compile/contract evidence.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-201", "status": status, "missing": len(missing)}, ensure_ascii=False))
PY

rg -q 'SnapshotRequest|APIError|RecordHubBindingClient' "$root_dir/sdk/go/recordhub" "$root_dir/sdk/java/src/main/java"
rg -q 'RecordHubCommandResult|RecordHubCommandEnvelope' "$approver_root/approver-application/src/main/java"
rg -q 'CommandResultOutbox|CommandEnvelope|BindingClient' "$fluxion_root/server/src/main/kotlin/fluxion/recordhub"
rg -q 'command|binding|tender_summary' "$bids_root/backend/internal/recordhub"

cat "$output"
