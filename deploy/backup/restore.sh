#!/bin/sh
# restore.sh [backup-name|latest]
#
# Downloads a backup from BACKUP_REMOTE, decrypts it with the age private key
# at AGE_IDENTITY_FILE, and restores it into RESTORE_DB (default
# spendmux_restore_check, a scratch database), then prints row counts.
# Restoring over the live database needs RESTORE_DB=spendmux and I_KNOW=yes.
set -eu -o pipefail

: "${PGHOST:=postgres}" "${PGUSER:=spendmux}" "${PGDATABASE:=spendmux}"
: "${BACKUP_REMOTE:?set BACKUP_REMOTE}" "${AGE_IDENTITY_FILE:=/run/secrets/backup-age-key}"
export PGHOST PGUSER
target=${RESTORE_DB:-spendmux_restore_check}
name=${1:-latest}

if [ "$name" = latest ]; then
	name=$(rclone lsf --include 'spendmux-*.dump.age' "$BACKUP_REMOTE" | sort | tail -n 1)
fi
[ -n "$name" ] || { echo "no backups found in $BACKUP_REMOTE" >&2; exit 1; }

if [ "$target" = "$PGDATABASE" ]; then
	[ "${I_KNOW:-}" = yes ] || { echo "refusing to overwrite the live database $target; set I_KNOW=yes" >&2; exit 1; }
else
	psql -d postgres -qc "DROP DATABASE IF EXISTS \"$target\"" -c "CREATE DATABASE \"$target\""
fi

echo "restoring $name into $target"
rclone cat "$BACKUP_REMOTE/$name" | age -d -i "$AGE_IDENTITY_FILE" | pg_restore --no-owner --clean --if-exists -d "$target"

for t in $(psql -d "$target" -Atc "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1"); do
	printf '%-32s %s\n' "$t" "$(psql -d "$target" -Atc "SELECT count(*) FROM \"$t\"")"
done
