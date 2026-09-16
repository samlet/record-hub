#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="$script_dir/.env.local"

if [[ -e "$env_file" ]]; then
  echo "$env_file already exists; remove it explicitly to rotate local credentials"
  exit 0
fi

command -v openssl >/dev/null || {
  echo "openssl is required" >&2
  exit 1
}
command -v htpasswd >/dev/null || {
  echo "htpasswd is required (install Apache httpd tools)" >&2
  exit 1
}

umask 077
local_password="$(openssl rand -base64 24 | tr -d '\n')"
local_password_hash="$(htpasswd -bnBC 10 '' "$local_password" | cut -d: -f2)"

{
  printf 'DEX_ISSUER=http://127.0.0.1:5556/dex\n'
  printf 'DEX_LOCAL_EMAIL=record-hub-dev@example.test\n'
  printf 'DEX_LOCAL_PASSWORD=%s\n' "$local_password"
  printf 'DEX_LOCAL_PASSWORD_HASH=%s\n' "$local_password_hash"
  printf 'DEX_APPROVER_WEB_SECRET=%s\n' "$(openssl rand -hex 32)"
  printf 'DEX_FLUXION_WEB_SECRET=%s\n' "$(openssl rand -hex 32)"
  printf 'DEX_BIDS_WEB_SECRET=%s\n' "$(openssl rand -hex 32)"
  printf 'DEX_RECORD_HUB_WEB_SECRET=%s\n' "$(openssl rand -hex 32)"
} >"$env_file"

chmod 600 "$env_file"
echo "created $env_file with mode 0600"
