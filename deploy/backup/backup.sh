#!/bin/sh
# backup.sh [schedule|now]
#
# Dumps the database, encrypts the dump to BACKUP_AGE_RECIPIENT (an age public
# key whose private half is NOT on this server), and copies it to BACKUP_REMOTE
# with rclone. "schedule" runs once a day at BACKUP_AT (UTC); "now" runs once.
set -eu -o pipefail

: "${PGHOST:=postgres}" "${PGUSER:=spendmux}" "${PGDATABASE:=spendmux}"
: "${BACKUP_REMOTE:?set BACKUP_REMOTE, e.g. b2:my-bucket/spendmux}"
: "${BACKUP_AGE_RECIPIENT:?set BACKUP_AGE_RECIPIENT to an age public key (age1...)}"
: "${BACKUP_AT:=03:00}" "${BACKUP_KEEP_DAYS:=30}" "${BACKUP_KEEP_LOCAL:=3}" "${BACKUP_PING_URL:=}"
export PGHOST PGUSER PGDATABASE

run() {
	name="spendmux-$(date -u +%Y%m%dT%H%M%SZ).dump.age"
	pg_dump --format=custom --no-owner | age -r "$BACKUP_AGE_RECIPIENT" > "/backups/.$name.part"
	mv "/backups/.$name.part" "/backups/$name"
	rclone copyto "/backups/$name" "$BACKUP_REMOTE/$name"
	rclone delete --min-age "${BACKUP_KEEP_DAYS}d" --include 'spendmux-*.dump.age' "$BACKUP_REMOTE"
	ls -1t /backups/spendmux-*.dump.age | tail -n +$((BACKUP_KEEP_LOCAL + 1)) | xargs -r rm -f
	echo "backup ok: $name $(du -h "/backups/$name" | cut -f1) -> $BACKUP_REMOTE"
	# Optional dead-man's switch (healthchecks.io etc.): silence means backups stopped.
	[ -z "$BACKUP_PING_URL" ] || wget -qO /dev/null "$BACKUP_PING_URL" || true
}

case "${1:-schedule}" in
now) run ;;
schedule)
	while :; do
		now=$(date -u +%s)
		next=$(date -u -d "$(date -u +%Y-%m-%d) $BACKUP_AT" +%s)
		[ "$next" -gt "$now" ] || next=$((next + 86400))
		echo "next backup $(date -u -d "@$next" '+%Y-%m-%d %H:%M') UTC"
		sleep $((next - now))
		run || echo "backup FAILED" >&2
	done
	;;
*) echo "usage: backup.sh [schedule|now]" >&2; exit 2 ;;
esac
