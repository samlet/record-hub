#!/usr/bin/env bash
set -Eeuo pipefail

# Explicit process boundary for the four-owner topology supervisor.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export RECORD_HUB_P3_NATS_RECOVERY_HOOK=1
exec "$root_dir/scripts/verify-p3-nats-outage-recovery.sh"
