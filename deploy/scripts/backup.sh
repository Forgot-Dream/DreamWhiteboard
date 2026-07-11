#!/bin/sh
set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
ENV_FILE=${DREAMWHITEBOARD_ENV_FILE:-"$SCRIPT_DIR/../.env"}
OUTPUT=${1:-"$SCRIPT_DIR/../backups/$(date -u +%Y%m%dT%H%M%SZ)"}

if [ ! -f "$ENV_FILE" ]; then
  echo "Compose environment file not found: $ENV_FILE" >&2
  echo "Set DREAMWHITEBOARD_ENV_FILE or create deploy/.env." >&2
  exit 2
fi

compose() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

restart_services() {
  compose up -d api frontend >/dev/null
}

restart_on_exit() {
  restart_services || true
}

mkdir -p "$OUTPUT"
trap restart_on_exit EXIT
compose stop frontend api >/dev/null
compose exec -T postgres sh -c 'pg_dump --format=custom --no-owner --no-acl --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' > "$OUTPUT/database.dump"
compose run --rm --no-deps -T api tar -C /app/uploads -czf - . > "$OUTPUT/uploads.tar.gz"
(cd "$OUTPUT" && sha256sum database.dump uploads.tar.gz > SHA256SUMS)
cat > "$OUTPUT/manifest.txt" <<EOF
format=dreamwhiteboard-backup-v1
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
database=database.dump
uploads=uploads.tar.gz
EOF
restart_services
trap - EXIT

echo "Backup written to $OUTPUT"
