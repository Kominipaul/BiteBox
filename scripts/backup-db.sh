#!/usr/bin/env bash
# Takes a safe, consistent snapshot of the live bitebox.db (WAL mode) into
# backups/, timestamped, and prunes old ones beyond a retention count.
# Run manually, or on a cron: */30 * * * * cd /path/to/bitebox && ./scripts/backup-db.sh
set -euo pipefail

cd "$(dirname "$0")/.."

DB_FILE="bitebox.db"
BACKUP_DIR="backups"
RETENTION=30 # keep the most recent N backups, delete older ones

if [[ ! -f "$DB_FILE" ]]; then
  echo "No $DB_FILE found — nothing to back up." >&2
  exit 1
fi

command -v sqlite3 >/dev/null 2>&1 || {
  echo "sqlite3 CLI not found. Install it (e.g. 'sudo apt install sqlite3'), or use the" >&2
  echo "admin dashboard's own 'Download backup' button instead, which needs no CLI." >&2
  exit 1
}

mkdir -p "$BACKUP_DIR"

TIMESTAMP="$(date +%Y-%m-%d-%H%M%S)"
DEST="$BACKUP_DIR/bitebox-$TIMESTAMP.db"

# sqlite3's own `.backup` command is the correct way to copy a live
# WAL-mode database — unlike `cp`, it doesn't need the -wal/-shm sidecars
# copied alongside it, and can't produce a torn read mid-write the way
# copying the plain file during an in-flight transaction could.
sqlite3 "$DB_FILE" ".backup '$DEST'"

echo "✅ Backed up to $DEST"

# Prune: keep only the $RETENTION most recent backups.
# shellcheck disable=SC2012
ls -1t "$BACKUP_DIR"/bitebox-*.db 2>/dev/null | tail -n +$((RETENTION + 1)) | while read -r old; do
  rm -f "$old"
  echo "🗑️  Pruned old backup: $old"
done
