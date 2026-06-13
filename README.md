# DreamWhiteboard

DreamWhiteboard is a self-hosted online collaborative whiteboard MVP. It includes account login, system administration, project roles, multi-board projects, an infinite canvas, realtime WebSocket collaboration, customizable text/image blocks, local uploads, and a Docker Compose deployment stack.

## Features

- Account login with bearer token and cookie session support.
- System roles: `system_admin` and `user`.
- Project roles: `owner`, `admin`, `editor`, and `viewer`.
- Project, member, board, user, asset, and snapshot REST APIs.
- Infinite whiteboard canvas with pan, zoom, selection, drag, resize, edit, delete, image upload, and saved-time feedback.
- Realtime collaboration over WebSocket:
  - Server-assigned monotonic board versions for mutating operations.
  - Collaborator cursor presence.
  - Remote selected-object indicators.
  - Viewer mutation rejection.
- Minimal whiteboard controls for text and image blocks, with custom fill, text color, border color, border width, and image aspect-ratio locking.
- Local upload storage for images and attachments.
- Internationalized frontend with English and Simplified Chinese (`zh_cn`) support.
- Docker Compose stack for frontend, Go API, Postgres, and local upload volume.

## Tech Stack

- Frontend: React, Vite, TypeScript, TipTap, lucide-react.
- Backend: Go standard library HTTP server plus a lightweight WebSocket implementation.
- Storage:
  - In-memory repository for local development and tests.
  - Postgres repository and migrations for deployment.
- Deployment: Docker Compose with frontend, API, Postgres, and upload volume.

## Repository Layout

```text
backend/              Go API, domain models, realtime hub, stores, migrations
frontend/             React/Vite application
deploy/               Docker Compose stack and local upload volume
README.md             Project documentation
LICENSE               GNU GPLv3 license text
```

## Quick Start

Start the full stack:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Default local endpoints:

- Frontend: <http://localhost:5173>
- Backend API: <http://localhost:8080>
- Postgres: `localhost:5432`

Default initial system administrator:

- Email: `admin@example.com`
- Password: `admin123`

Change these credentials before using a persistent or shared deployment.

## Local Development

Run the backend with the in-memory store:

```bash
cd backend
go run ./cmd/server
```

Run the backend tests:

```bash
cd backend
go test ./...
```

Run the frontend:

```bash
cd frontend
npm install
npm run dev
```

Build the frontend:

```bash
cd frontend
npm run build
```

When the frontend and backend run on different origins, set `VITE_API_BASE` for the frontend:

```bash
VITE_API_BASE=http://localhost:8080 npm run dev
```

## Configuration

Backend environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | API listen address. |
| `DATABASE_URL` | unset | Postgres connection string. If unset, the server uses the in-memory repository. |
| `UPLOAD_DIR` | `./uploads` | Local upload directory for asset files. |
| `FIRST_ADMIN_EMAIL` | `admin@example.com` | Email for the initial system administrator. |
| `FIRST_ADMIN_PASSWORD` | `admin123` | Password for the initial system administrator. |

Frontend environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `VITE_API_BASE` | current origin | Base URL used for REST, asset, and WebSocket API calls. |

## API Summary

Authentication:

- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/me`

Projects and members:

- `GET /api/projects`
- `POST /api/projects`
- `GET /api/projects/:id`
- `PATCH /api/projects/:id`
- `DELETE /api/projects/:id`
- `GET /api/projects/:id/members`
- `POST /api/projects/:id/members`
- `DELETE /api/projects/:id/members?user_id=:userID`

Boards:

- `GET /api/projects/:id/boards`
- `POST /api/projects/:id/boards`
- `GET /api/boards/:id`
- `PATCH /api/boards/:id`
- `DELETE /api/boards/:id`
- `GET /api/boards/:id/ws`

Assets:

- `POST /api/projects/:id/assets`
- `GET /api/assets/:id`

System administration:

- `GET /api/admin/users`
- `POST /api/admin/users`
- `PATCH /api/admin/users/:id`

## WebSocket Protocol

Connect to:

```text
GET /api/boards/:id/ws
```

The client passes `client_id` in the query string. If token auth is used outside cookies, pass `token` as a query parameter.

Client message types:

- `join`
- `operation`
- `cursor`
- `presence`

Server message types:

- `snapshot`
- `operation_ack`
- `operation_broadcast`
- `cursor`
- `presence`
- `error`

Supported whiteboard operations:

- `create_block`
- `update_block`
- `delete_block`
- `move_block`
- `resize_block`
- `reorder_block`

Cursor and presence messages are broadcast-only and do not increment board versions. Mutating operations are validated on the server and receive a persisted board version.

## Permissions

- `system_admin`: can administer all users and access all projects.
- `owner`: owns a project and can manage project members and boards.
- `admin`: can manage project members and boards.
- `editor`: can create and edit boards and whiteboard blocks.
- `viewer`: can view boards and realtime presence, but cannot mutate board state.

## Deployment Notes

The included Compose stack uses:

- Postgres 16 Alpine.
- Backend container built from `backend/Dockerfile`.
- Frontend container built from `frontend/Dockerfile`.
- `deploy/uploads` mounted into the API container for local asset storage.

For production-like deployments:

- Replace the default initial administrator credentials.
- Use a strong database password.
- Put the API behind HTTPS.
- Persist Postgres and upload volumes.
- Consider replacing local upload storage with S3-compatible storage.

## License

DreamWhiteboard is licensed under the GNU General Public License version 3. See [LICENSE](LICENSE).
