# DreamWhiteboard

DreamWhiteboard is an online collaborative whiteboard MVP with a Go API, React/Vite frontend, WebSocket collaboration, project roles, local uploads, and a Postgres deployment target.

## Stack

- Frontend: React, Vite, TypeScript
- Backend: Go standard library HTTP/WebSocket implementation
- Storage: in-memory repository for local MVP runtime and tests, with Postgres schema/migrations included for the deployment boundary
- Deploy: Docker Compose for frontend, backend, Postgres, and upload volume

## Quick Start

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Local API defaults:

- Admin email: `admin@example.com`
- Admin password: `admin123`
- Frontend: <http://localhost:5173>
- Backend: <http://localhost:8080>

For backend-only development:

```bash
cd backend
go test ./...
go run ./cmd/server
```

For frontend-only development:

```bash
cd frontend
npm install
npm run dev
```

## MVP Scope

- System roles: `system_admin`, `user`
- Project roles: `owner`, `admin`, `editor`, `viewer`
- Project/board/member/user REST APIs
- Local file uploads with asset metadata
- Whiteboard blocks: `rich_text`, `note`, `image`, `shape`
- Infinite canvas interactions: pan, zoom, select, drag, edit, delete
- WebSocket room collaboration with server-assigned monotonically increasing board versions

