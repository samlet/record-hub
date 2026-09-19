#!/usr/bin/env bash
set -Eeuo pipefail

# The topology supervisor invokes this file as a process boundary. Keep the
# hook marker explicit here instead of relying on shell assignment inheritance
# from the outer workflow gate; this prevents the gate from recursively
# starting another topology when the ready hook is called.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export RECORD_HUB_P3_WORKFLOW_E2E_HOOK=1
exec "$root_dir/scripts/verify-p3-workflow-e2e.sh"
