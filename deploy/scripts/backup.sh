#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
OUTPUT=${1:-"$SCRIPT_DIR/../backups/$(date -u +%Y%m%dT%H%M%SZ)"}

mkdir -p "$OUTPUT"
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -c 'pg_dump --format=custom --no-owner --no-acl --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' > "$OUTPUT/database.dump"
docker compose -f "$COMPOSE_FILE" exec -T api tar -C /app/uploads -czf - . > "$OUTPUT/uploads.tar.gz"
(cd "$OUTPUT" && sha256sum database.dump uploads.tar.gz > SHA256SUMS)
cat > "$OUTPUT/manifest.txt" <<EOF
format=dreamwhiteboard-backup-v1
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
database=database.dump
uploads=uploads.tar.gz
EOF

echo "Backup written to $OUTPUT"
