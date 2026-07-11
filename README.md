# DreamWhiteboard

DreamWhiteboard is a single-host, self-hosted collaborative whiteboard. The v2 architecture uses a Go/Postgres control plane, Yjs CRDT documents over authenticated WebSockets, and a React DOM canvas.

## What is included

- HttpOnly cookie sessions persisted as SHA-256 token hashes in Postgres; passwords use Argon2id.
- Project roles (`owner`, `admin`, `editor`, `viewer`) with last-owner protection.
- Versioned SQL migrations, health/readiness endpoints, structured request logs, request IDs, body limits, login throttling, security headers, and a CORS allowlist.
- Yjs `Y.Map("blocks")` documents with `text` and `image` block schemas, durable ordered updates, stable update IDs, acknowledgements, automatic reconnect/replay, checkpoints, and Awareness presence.
- Sequence-bound image reference manifests prevent deletion of assets that are still used by a board; stale or conflicting indexes fail closed.
- A routed React UI using TanStack Query and Zustand, including multi-select, box select, undo/redo, copy/cut/paste, duplicate, z-ordering, align/distribute, keyboard movement, image upload progress/cancel/retry, and saved per-board viewports.
- Same-origin Nginx proxying, private API/Postgres networks, container health checks, graceful shutdown, backup/restore verification scripts, CI, dependency updates, vulnerability scans, and tagged image publishing.
- A transactional Postgres cleanup outbox retries asset-file and project-directory removal across filesystem errors and process restarts.

The collaboration service is intentionally single-instance. There is no Redis fan-out and no refresh-persistent offline editing; unacknowledged edits survive reconnects only while the page remains open.

## Quick start

Requirements: Docker Engine with Compose v2.

```bash
cp deploy/.env.example deploy/.env
```

Edit `deploy/.env`. At minimum, replace both password placeholders with unique values. Then start the stack:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
```

Open <http://127.0.0.1:8080>. The bootstrap administrator is required to change the configured one-time password on first login.

After that password change succeeds, remove `FIRST_ADMIN_EMAIL` and `FIRST_ADMIN_PASSWORD` from `deploy/.env` (or leave them empty) and restart. Bootstrap runs only for an empty database, so the one-time secret does not need to remain on the host.

Check service status:

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
docker compose -f deploy/docker-compose.yml --env-file deploy/.env ps
```

Only Nginx binds a host port. Postgres and the API are reachable only on the internal Compose network.

## Local development

Start Postgres from the Compose stack after creating `deploy/.env`:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d postgres
```

Run the API (the production executable deliberately has no in-memory fallback):

```bash
cd backend
DATABASE_URL='postgres://dreamwhiteboard:YOUR_URL_SAFE_PASSWORD@localhost:5432/dreamwhiteboard?sslmode=disable' \
FIRST_ADMIN_EMAIL='admin@example.com' \
FIRST_ADMIN_PASSWORD='a-unique-one-time-password' \
SESSION_COOKIE_SECURE=false \
go run ./cmd/server
```

For host-based backend development, expose Postgres with a local-only Compose override or run a separate development Postgres container. Do not expose the production database publicly.

Run the frontend; Vite proxies `/api` and WebSocket upgrades to port 8080:

```bash
cd frontend
npm ci
npm run dev
```

Run the Postgres-backed integration tests by pointing them at a disposable database:

```bash
cd backend
TEST_DATABASE_URL='postgres://dreamwhiteboard:test-password@localhost:5432/dreamwhiteboard_test?sslmode=disable' \
go test ./internal/store ./cmd/server -count=1
```

For the browser suite, start the full Compose stack and then run:

```bash
cd frontend
E2E_BASE_URL=http://127.0.0.1:8080 \
E2E_ADMIN_EMAIL=admin@example.com \
E2E_ADMIN_PASSWORD='your-bootstrap-password' \
npm run e2e
```

## Data model and migrations

The API applies numbered files from `backend/migrations` before accepting traffic, and refuses readiness when the schema is behind. Each migration records its version in `schema_migrations`.

Version 2 is a breaking development upgrade. It removes the prototype `board_snapshots`, `board_operations`, and board version column, replacing them with:

- `board_documents`: current Yjs checkpoint and covered server sequence.
- `board_updates`: ordered opaque Yjs updates plus durable update-ID receipts for idempotent replay.
- `sessions`: hashed browser sessions and expiration/last-access timestamps.

Later migrations add:

- `storage_cleanup_jobs`: leased, retryable filesystem deletion jobs written in the same transaction as metadata deletion.
- `board_asset_reference_state` and `board_asset_references`: the last exact, server-sequence-bound asset manifest for each board.

Prototype whiteboard content is not migrated. For a clean development upgrade:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env down -v
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
```

This deletes all database and upload volumes; use it only when that is intended.

## Collaboration protocol

Connect with the session cookie to `GET /api/boards/:id/ws`. No token is accepted in localStorage, headers, asset URLs, or the WebSocket query string. The optional `client_id` query value is a non-secret page-lifetime identifier.

Messages are WebSocket text frames containing JSON. Binary Yjs data uses standard base64 in `data`.

Initial sync is ordered:

1. `sync_start` with the authoritative client/user IDs and `can_edit` permission.
2. An optional `checkpoint`.
3. Zero or more `update` messages ordered by `server_sequence`.
4. `sync_complete`.

An editor sends `{type:"update", update_id, data, reference_base_sequence, asset_ids}`. The manifest is derived from the complete local Yjs document and is covered by the update's idempotency hash. The server persists the opaque update before replying with `update_ack`; a stale manifest never rejects an otherwise independent offline update. Editor declarations cannot authorize deletion by themselves.

After reaching a contiguous server sequence with no pending updates, an owner/admin client reconciles the complete manifest through `PUT /api/boards/:id/asset-references`. A stale sequence is rejected; two different manifests for the same exact sequence mark the index conflicted. Asset deletion fails closed while any project board is stale/conflicted, and returns `asset_in_use` when referenced. Opaque checkpoints are likewise accepted only from project managers; malformed incremental updates are quarantined by clients instead of preventing the board from opening.

Awareness messages carry participant, cursor, viewport, and selection state but are never persisted and do not mark a board as saved. On disconnect, the server immediately broadcasts a removal message. Viewers receive document and awareness traffic, but document updates and checkpoints are rejected.

## REST API

REST owns authentication and metadata; it never modifies whiteboard content.

- Authentication: `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/me`, `POST /api/me/password`.
- Projects: `GET/POST /api/projects`, `GET/PATCH/DELETE /api/projects/:id`.
- Members: `GET/POST /api/projects/:id/members`, `PATCH/DELETE /api/projects/:id/members/:userID`.
- Boards: `GET/POST /api/projects/:id/boards`, `GET/PATCH/DELETE /api/boards/:id`.
- Board asset index: `PUT /api/boards/:id/asset-references`.
- Assets: `POST /api/projects/:id/assets`, `GET/DELETE /api/assets/:id`.
- Administrators: `GET/POST /api/admin/users`, `PATCH /api/admin/users/:id`, `POST /api/admin/users/:id/password`.

Errors have a stable shape:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "request validation failed",
    "request_id": "...",
    "fields": { "name": "name is required" }
  }
}
```

`GET /api/boards/:id` returns board metadata, permission, and the collaboration endpoint, not a block snapshot.

## Editor shortcuts

| Shortcut | Action |
| --- | --- |
| `Delete` / `Backspace` | Delete selection |
| `Escape` | Clear selection and return to select tool |
| `Ctrl/Cmd+C`, `X`, `V` | Copy, cut, paste (image blocks reuse asset references) |
| `Ctrl/Cmd+D` | Duplicate |
| `Ctrl/Cmd+Z`, `Shift+Ctrl/Cmd+Z` | Undo, redo current-user commands |
| Arrow keys | Move selection by 1 world pixel |
| `Shift` + arrow keys | Move selection by 10 world pixels |
| `Ctrl/Cmd+A` | Select all blocks |

Use Shift-click for additive selection. Drag an empty canvas area with the select tool for box selection; use the hand tool or middle mouse button to pan.

## Backup and restore

Create a consistent database dump and upload archive:

```bash
deploy/scripts/backup.sh
```

The backup command briefly stops the frontend and API so the database dump and upload archive describe the same application state, then restarts both services. Scripts use `deploy/.env` by default; set `DREAMWHITEBOARD_ENV_FILE` to select another Compose environment file.

Verify checksums, archive readability, and an isolated Postgres restore without replacing the live database:

```bash
deploy/scripts/verify-backup.sh deploy/backups/20260101T000000Z
```

Restore is destructive and requires an explicit flag:

```bash
deploy/scripts/restore.sh --confirm deploy/backups/20260101T000000Z
```

Keep backups outside the application host and test restoration regularly.

CI performs the same backup, an isolated database verification, a destructive restore into the disposable CI stack, and a browser check that the restored account, Yjs document, and image are usable.

## Versioned images and upgrades

Tags matching `v*` publish `dreamwhiteboard-api` and `dreamwhiteboard-frontend` images to GHCR only after backend, frontend, Postgres, browser, backup/restore, vulnerability, and container gates pass. Pin both services to the same release in `deploy/.env`:

```dotenv
API_IMAGE=ghcr.io/OWNER/dreamwhiteboard-api:v2.0.0
FRONTEND_IMAGE=ghcr.io/OWNER/dreamwhiteboard-frontend:v2.0.0
```

Then deploy without rebuilding source:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env pull
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --no-build
```

Before an upgrade, create and verify a backup. Database migrations are forward-only and run before the API accepts traffic. Rollback therefore means restoring the pre-upgrade backup and pinning the previous pair of image tags; changing only the image tag after a migration is not a safe database rollback.

## Configuration

Important API settings:

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres connection URL. |
| `FIRST_ADMIN_EMAIL` / `FIRST_ADMIN_PASSWORD` | required for an empty DB | One-time bootstrap credentials; password must be at least 12 characters. |
| `SESSION_TTL` | `168h` | Persistent session lifetime. |
| `SESSION_COOKIE_SECURE` | `true` in the API | Must be `true` behind public HTTPS; the local HTTP Compose example sets `false`. |
| `ALLOWED_ORIGINS` | local Vite origins | Comma-separated cross-origin development allowlist; same-origin is accepted automatically. |
| `MAX_REQUEST_BYTES` | `1 MiB` | JSON request limit. |
| `MAX_UPLOAD_BYTES` | `25 MiB` | Multipart upload limit. |
| `MAX_IMAGE_PIXELS` | `40,000,000` | Decoded image dimension limit. |
| `LOGIN_RATE_LIMIT` / `LOGIN_RATE_WINDOW` | `5` / `5m` | Failed login throttle. |
| `WS_AUTH_CHECK_INTERVAL` | `10s` | Maximum interval before an open collaboration socket revalidates its session and project access. |
| `UPLOAD_DIR` | `./uploads` | Local asset root. |
| `LOG_LEVEL` | `info` | JSON log level (`debug`, `info`, `warn`, `error`). |

For a shared deployment, terminate TLS in front of Nginx, set `SESSION_COOKIE_SECURE=true`, bind only to the intended interface, use a URL-safe random database password, restrict backup permissions, and do not reuse the bootstrap password.

## Quality gates

Run locally:

```bash
cd backend
gofmt -w .
go vet ./...
go test ./...
go test -race ./...

cd ../frontend
npm run typecheck
npm test
npm run build
npm audit --audit-level=high

cd ..
docker compose -f deploy/docker-compose.yml --env-file deploy/.env.example config
```

GitHub Actions runs the same checks with Postgres, builds and scans both images, and publishes versioned GHCR images for `v*` tags. Dependabot tracks Go, npm, Docker, and Actions updates.

## License

DreamWhiteboard is licensed under the GNU General Public License version 3. See [LICENSE](LICENSE).
