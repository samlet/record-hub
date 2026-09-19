#!/usr/bin/env bash
set -Eeuo pipefail

# P3-406: backup/restore gate.  A real run requires explicit source and empty
# restore targets; the script never guesses a database or destroys a user's
# existing Mongo/PostgreSQL/NATS data.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${RECORD_HUB_P3_BACKUP_EVIDENCE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/record-hub-p3-406-evidence.XXXXXX")}"
mkdir -p "$evidence_dir"

if [[ "${RECORD_HUB_P3_BACKUP_LIVE:-0}" != "1" ]]; then
  echo "P3-406 backup/restore: SKIPPED (set RECORD_HUB_P3_BACKUP_LIVE=1 with explicit source/restore targets)"
  exit 0
fi

for command in jq mongodump mongorestore pg_dump pg_restore nats tar shasum; do
  command -v "$command" >/dev/null || {
    echo "P3-406 backup/restore: SKIPPED (missing command: $command)"
    exit 0
  }
done

required=(RECORD_HUB_P3_BACKUP_MONGO_URI RECORD_HUB_P3_BACKUP_PG_DATABASES RECORD_HUB_P3_BACKUP_NATS_STORE RECORD_HUB_P3_RESTORE_MONGO_URI RECORD_HUB_P3_RESTORE_PG_DATABASES RECORD_HUB_P3_RESTORE_NATS_STORE)
missing=()
for variable in "${required[@]}"; do
  [[ -n "${!variable:-}" ]] || missing+=("$variable")
done
if (( ${#missing[@]} > 0 )); then
  jq -n --argjson missing "$(printf '%s\n' "${missing[@]}" | jq -R . | jq -s .)" \
    '{gate:"P3-406",status:"SKIPPED",reason:"explicit backup source and isolated restore targets were not provided",missing:$missing,retry:"Provide the six RECORD_HUB_P3_BACKUP_* / RECORD_HUB_P3_RESTORE_* variables pointing at disposable isolated services."}' \
    | tee "$evidence_dir/backup-restore.json" "$evidence_dir/p3-406-backup-restore.json" >/dev/null
  echo "P3-406 backup/restore: SKIPPED (missing explicit source/restore target: ${missing[*]})"
  exit 0
fi

backup_root="$evidence_dir/backup"
restore_root="$evidence_dir/restore"
mkdir -p "$backup_root/mongo" "$backup_root/postgres" "$backup_root/nats-source" "$restore_root"

write_component_manifest() {
  local label="$1" root="$2" output="$3"
  {
    printf '## %s\n' "$label"
    (cd "$root" && find . -type f -print0 | sort -z | xargs -0 shasum -a 256)
  } >>"$output"
}

# Capture each source using native tools.  The caller is responsible for
# making restore PostgreSQL databases and Mongo/NATS targets disposable.
mongodump --uri "$RECORD_HUB_P3_BACKUP_MONGO_URI" --out "$backup_root/mongo" --gzip
IFS=',' read -r -a databases <<<"$RECORD_HUB_P3_BACKUP_PG_DATABASES"
for database in "${databases[@]}"; do
  database="${database//[[:space:]]/}"
  [[ -n "$database" ]] || continue
  pg_dump --format=custom --file "$backup_root/postgres/${database}.dump" "$database"
done
tar -C "$RECORD_HUB_P3_BACKUP_NATS_STORE" -cf - . | tar -C "$backup_root/nats-source" -xf -
tar -C "$backup_root/nats-source" -czf "$backup_root/jetstream.tar.gz" .

: >"$evidence_dir/backup.sha256"
write_component_manifest mongo "$backup_root/mongo" "$evidence_dir/backup.sha256"
write_component_manifest postgres "$backup_root/postgres" "$evidence_dir/backup.sha256"
write_component_manifest nats "$backup_root/nats-source" "$evidence_dir/backup.sha256"

mongorestore --uri "$RECORD_HUB_P3_RESTORE_MONGO_URI" --drop --gzip "$backup_root/mongo"
IFS=',' read -r -a restore_databases <<<"$RECORD_HUB_P3_RESTORE_PG_DATABASES"
for database in "${restore_databases[@]}"; do
  database="${database//[[:space:]]/}"
  [[ -n "$database" ]] || continue
  dump="$backup_root/postgres/${database}.dump"
  [[ -f "$dump" ]] || { echo "missing dump for restore database $database" >&2; exit 1; }
  pg_restore --clean --if-exists --no-owner --dbname "$database" "$dump"
done
rm -rf "$RECORD_HUB_P3_RESTORE_NATS_STORE"
mkdir -p "$RECORD_HUB_P3_RESTORE_NATS_STORE"
tar -C "$RECORD_HUB_P3_RESTORE_NATS_STORE" -xzf "$backup_root/jetstream.tar.gz"

# Re-dump the restored databases and compare normalized component manifests.
# Comparing the backup directory with itself would be a false PASS; the
# verification below reads every restored Mongo/PostgreSQL/NATS target.
mkdir -p "$restore_root/mongo" "$restore_root/postgres"
mongodump --uri "$RECORD_HUB_P3_RESTORE_MONGO_URI" --out "$restore_root/mongo" --gzip
IFS=',' read -r -a restore_verify_databases <<<"$RECORD_HUB_P3_RESTORE_PG_DATABASES"
for database in "${restore_verify_databases[@]}"; do
  database="${database//[[:space:]]/}"
  [[ -n "$database" ]] || continue
  pg_dump --format=custom --file "$restore_root/postgres/${database}.dump" "$database"
done
: >"$evidence_dir/restore.sha256"
write_component_manifest mongo "$restore_root/mongo" "$evidence_dir/restore.sha256"
write_component_manifest postgres "$restore_root/postgres" "$evidence_dir/restore.sha256"
write_component_manifest nats "$RECORD_HUB_P3_RESTORE_NATS_STORE" "$evidence_dir/restore.sha256"
if cmp -s "$evidence_dir/backup.sha256" "$evidence_dir/restore.sha256"; then
  status="PASS"
else
  status="FAIL"
fi
jq -n --arg status "$status" --arg backupSha "$evidence_dir/backup.sha256" --arg restoreSha "$evidence_dir/restore.sha256" \
  '{gate:"P3-406",status:$status,hashes:{backup:$backupSha,restore:$restoreSha},checks:{mongoDumpRestore:true,postgresDumpRestore:true,jetStreamStoreArchive:true,hashManifestEqual:($status == "PASS")}}' \
  | tee "$evidence_dir/backup-restore.json" "$evidence_dir/p3-406-backup-restore.json" >/dev/null
[[ "$status" == "PASS" ]] || exit 1
echo "P3-406 backup/restore passed (evidence: $evidence_dir)"
