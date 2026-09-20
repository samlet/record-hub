#!/usr/bin/env bash
set -Eeuo pipefail

# P6-201: compile/test the owner SDK parity surfaces without starting any
# service or connector. This gate records command results and source commits;
# it does not infer live connector readiness.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
approver_root="${APPROVER_ROOT:-/Users/xiaofeiwu/apps/approver}"
fluxion_root="${FLUXION_ROOT:-/Users/xiaofeiwu/portals/fluxion}"
bids_root="${BIDS_ROOT:-/Users/xiaofeiwu/apps/bids}"
output="${RECORD_HUB_P6_SDK_COMPILE_OUTPUT:-$root_dir/docs/phase-6-sdk-parity-compile.json}"
evidence_dir="${RECORD_HUB_P6_SDK_COMPILE_EVIDENCE_DIR:-$root_dir/build/evidence/phase6/p6-201-sdk-compile}"

for command in date git jq python3 go mvn; do
  command -v "$command" >/dev/null || { echo "P6-201 compile missing command: $command" >&2; exit 2; }
done
[[ -x "$fluxion_root/server/gradlew" ]] || { echo "P6-201 compile missing Fluxion Gradle wrapper" >&2; exit 2; }
[[ -x "$root_dir/web/node_modules/.bin/tsc" ]] || { echo "P6-201 compile missing TypeScript compiler" >&2; exit 2; }

mkdir -p "$evidence_dir" "$(dirname "$output")"
steps_file="$(mktemp "${TMPDIR:-/tmp}/record-hub-p6-201-compile.XXXXXX")"
trap 'rm -f "$steps_file"' EXIT

run_step() {
  local name="$1" workdir="$2" command_line="$3" log_file="$evidence_dir/$1.log"
  if (cd "$workdir" && bash -c "$command_line") >"$log_file" 2>&1; then
    printf '%s\tPASS\t%s\t%s\n' "$name" "$workdir" "$command_line" >>"$steps_file"
  else
    printf '%s\tFAIL\t%s\t%s\n' "$name" "$workdir" "$command_line" >>"$steps_file"
  fi
}

run_step record_hub_go "$root_dir" 'go test ./sdk/go/recordhub'
run_step record_hub_java "$root_dir" '(cd sdk/java && mvn -q test)'
run_step record_hub_typescript "$root_dir" 'web/node_modules/.bin/tsc --noEmit --target ES2020 --module ESNext sdk/typescript/index.ts'
run_step approver_java "$approver_root" 'mvn -q -pl approver-application -am test'
run_step bids_java "$bids_root/sdk/bids-java-sdk-core" 'mvn -q test'
run_step fluxion_kotlin "$fluxion_root" 'make test-server'

python3 - "$root_dir" "$output" "$steps_file" "$approver_root" "$fluxion_root" "$bids_root" <<'PY'
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone

root, output_path, steps_path, approver, fluxion, bids = map(pathlib.Path, sys.argv[1:])

def commit(path):
    return subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()

steps = []
for line in pathlib.Path(steps_path).read_text().splitlines():
    name, status, workdir, command = line.split("\t", 3)
    steps.append({"name": name, "status": status, "workdir": workdir, "command": command})
passed = all(step["status"] == "PASS" for step in steps)
result = {
    "schemaVersion": 1,
    "phase": "6",
    "task": "P6-201-COMPILE",
    "status": "PASS_STATIC" if passed else "BLOCKED_BY_SDK_COMPILE",
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "liveTraffic": False,
    "sourceCommits": {"record-hub": commit(root), "approver": commit(approver), "fluxion": commit(fluxion), "bids": commit(bids)},
    "steps": steps,
    "evidenceDir": "build/evidence/phase6/p6-201-sdk-compile",
    "decision": "STATIC_SDK_COMPILE_READY_FOR_LIVE_GATE" if passed else "DO_NOT_RELEASE_SDK_PARITY",
    "next": "Attach these owner commits and logs to the Phase 5/P6-000 evidence manifest; compile PASS does not enable live connectors.",
}
pathlib.Path(output_path).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"task": "P6-201-COMPILE", "status": result["status"], "failed": sum(step["status"] != "PASS" for step in steps)}, ensure_ascii=False))
if not passed:
    raise SystemExit(1)
PY

cat "$output"
