#!/bin/sh
set -eu

if [ -z "${1:-}" ]; then
  echo "Usage: $0 BACKUP_DIRECTORY" >&2
  exit 2
fi

BACKUP=$1
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
CHECK_DB=dreamwhiteboard_restore_check

test -f "$BACKUP/database.dump"
test -f "$BACKUP/uploads.tar.gz"
grep -qx 'format=dreamwhiteboard-backup-v1' "$BACKUP/manifest.txt"
(cd "$BACKUP" && sha256sum -c SHA256SUMS)
tar -tzf "$BACKUP/uploads.tar.gz" >/dev/null

cleanup() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c "dropdb --if-exists --username=\"\$POSTGRES_USER\" $CHECK_DB" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cleanup
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c "createdb --username=\"\$POSTGRES_USER\" $CHECK_DB"
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c "pg_restore --no-owner --no-acl --username=\"\$POSTGRES_USER\" --dbname=$CHECK_DB" < "$BACKUP/database.dump"
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c "psql --username=\"\$POSTGRES_USER\" --dbname=$CHECK_DB --tuples-only --command='SELECT count(*) FROM schema_migrations'" >/dev/null

echo "Backup checksum, upload archive, and database restore verified."
