#!/bin/sh
set -eu

if [ "${1:-}" != "--confirm" ] || [ -z "${2:-}" ]; then
  echo "Usage: $0 --confirm BACKUP_DIRECTORY" >&2
  exit 2
fi

BACKUP=$2
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"

test -f "$BACKUP/database.dump"
test -f "$BACKUP/uploads.tar.gz"
grep -qx 'format=dreamwhiteboard-backup-v1' "$BACKUP/manifest.txt"
(cd "$BACKUP" && sha256sum -c SHA256SUMS)

docker compose -f "$COMPOSE_FILE" stop frontend api
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c 'pg_restore --clean --if-exists --no-owner --no-acl --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' < "$BACKUP/database.dump"
docker compose -f "$COMPOSE_FILE" run --rm --no-deps -T api sh -c 'find /app/uploads -mindepth 1 -delete && tar -C /app/uploads -xzf -' < "$BACKUP/uploads.tar.gz"
docker compose -f "$COMPOSE_FILE" up -d api frontend

echo "Restore complete. Check /healthz and /readyz before allowing user traffic."
