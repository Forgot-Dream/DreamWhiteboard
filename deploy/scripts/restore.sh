#!/bin/sh
set -eu

if [ "${1:-}" != "--confirm" ] || [ -z "${2:-}" ]; then
  echo "Usage: $0 --confirm BACKUP_DIRECTORY" >&2
  exit 2
fi

BACKUP=$2
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
ENV_FILE=${DREAMWHITEBOARD_ENV_FILE:-"$SCRIPT_DIR/../.env"}

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

compose stop frontend api
compose exec -T postgres sh -c 'pg_restore --single-transaction --clean --if-exists --no-owner --no-acl --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' < "$BACKUP/database.dump"
compose run --rm --no-deps -T api sh -c '
  set -eu
  staging=/app/uploads/.restore-staging
  rm -rf "$staging"
  mkdir -p "$staging"
  tar -C "$staging" -xzf -
  find /app/uploads -mindepth 1 -maxdepth 1 ! -name .restore-staging -exec rm -rf -- {} +
  cp -a "$staging"/. /app/uploads/
  rm -rf "$staging"
' < "$BACKUP/uploads.tar.gz"
compose up -d api frontend

attempt=0
until compose exec -T frontend wget -qO- http://127.0.0.1/readyz >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "Restore completed, but readiness did not recover in time." >&2
    exit 1
  fi
  sleep 2
done

echo "Restore complete and readiness verified."
