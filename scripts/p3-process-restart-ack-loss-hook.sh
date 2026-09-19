#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export RECORD_HUB_P3_RESTART_ACK_LIVE=1
exec "$root_dir/scripts/verify-p3-process-restart-ack-loss.sh"
