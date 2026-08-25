#!/usr/bin/env bash
# Wipes bitebox.db and recreates it (schema + seed data) for testing.
# Run manually: ./scripts/reset-db.sh (or -y to skip the confirmation prompt).
set -euo pipefail

cd "$(dirname "$0")/.."

DB_FILE="bitebox.db"

if [[ "${1:-}" != "-y" ]]; then
  read -r -p "This will delete all data in $DB_FILE and reseed it. Continue? [y/N] " reply
  if [[ ! "$reply" =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
  fi
fi

rm -f "$DB_FILE" "$DB_FILE-wal" "$DB_FILE-shm" "$DB_FILE-journal"

go run ./cmd/resetdb

echo "✅ $DB_FILE reset and reseeded (admin/admin123, staff/staff123)."
