#!/bin/sh
set -eu

if [ -z "${1:-}" ]; then
  echo "Usage: $0 BACKUP_DIRECTORY" >&2
  exit 2
fi

BACKUP=$1
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
ENV_FILE=${DREAMWHITEBOARD_ENV_FILE:-"$SCRIPT_DIR/../.env"}
CHECK_DB=dreamwhiteboard_restore_check_$$

if [ ! -f "$ENV_FILE" ]; then
  echo "Compose environment file not found: $ENV_FILE" >&2
  echo "Set DREAMWHITEBOARD_ENV_FILE or create deploy/.env." >&2
  exit 2
fi

compose() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

test -f "$BACKUP/database.dump"
test -f "$BACKUP/uploads.tar.gz"
grep -qx 'format=dreamwhiteboard-backup-v1' "$BACKUP/manifest.txt"
(cd "$BACKUP" && sha256sum -c SHA256SUMS)
tar -tzf "$BACKUP/uploads.tar.gz" >/dev/null

cleanup() {
  compose exec -T postgres sh -c "dropdb --if-exists --username=\"\$POSTGRES_USER\" $CHECK_DB" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cleanup
compose exec -T postgres sh -c "createdb --username=\"\$POSTGRES_USER\" $CHECK_DB"
compose exec -T postgres sh -c "pg_restore --single-transaction --no-owner --no-acl --username=\"\$POSTGRES_USER\" --dbname=$CHECK_DB" < "$BACKUP/database.dump"
compose exec -T postgres sh -c "psql --username=\"\$POSTGRES_USER\" --dbname=$CHECK_DB --tuples-only --command='SELECT count(*) FROM schema_migrations'" >/dev/null

echo "Backup checksum, upload archive, and database restore verified."
