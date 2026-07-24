# SecureBrain v0 backend

SecureBrain is a localhost demonstration of a policy-controlled data-routing network. Brains own files, query paths select data, identity Services form finite owner-controlled routes, and destination owners approve pushed data before it is persisted.

This repository implements the backend specified by `securebrain-v0-prd.md` and `securebrain-v0-technical-doc.md`. Authentication is deliberately mocked. Services return their input unchanged, and chat is explicitly not grounded in uploaded files.

## Prerequisites

- Go 1.25.x
- A Supabase project
- An OpenAI API key, unless `CHAT_DISABLED=true`

## Set up a blank Supabase project

1. Open the Supabase SQL Editor, paste all of [`db/schema.sql`](db/schema.sql), and run it. The script is idempotent and creates the private bucket, schema, RLS posture, three users, three Brains, and two identity Services.
2. In Supabase's **Connect** dialog, copy a direct or session-pooler Postgres connection string into `DATABASE_URL`.
3. From project settings, copy the project URL and service-role key into `SUPABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY`. The service-role value is a backend secret and must never be placed in frontend code.
4. Copy `.env.example` to a private environment file and set the required values. Environment files are intentionally not loaded by the program; export them with your preferred local tool.

For example:

```sh
set -a
. ./.env.local
set +a
go run ./cmd/server
```

The default address is `http://127.0.0.1:8080`. `GET /healthz` reports process liveness and `GET /readyz` checks Postgres.

## Run with the frontend

In a second terminal:

```sh
cd ../silo
npm ci
npm run dev
```

Open `http://localhost:3000`. The Astro development server proxies API and query requests to `http://127.0.0.1:8080`, matching the backend's default `FRONTEND_ORIGIN`. Override the proxy target with `SECUREBRAIN_API_URL` when needed.

## Test and quality checks

Unit tests need no network or external services:

```sh
go test ./...
go vet ./...
go test -race ./...
```

Store integration tests use `DATABASE_TEST_URL` when supplied and otherwise skip
with a clear message. The configured PostgreSQL user must have `CREATEDB`; the
suite creates a uniquely named database, initializes it from `db/schema.sql`, and
drops it after the run. Never point this variable at a production server.

## Minimal API walkthrough

The API returns `{"data":...,"request_id":"req_..."}` on success and stable error envelopes on failure. Preserve the cookie jar between calls.

```sh
# Seeded users
curl -s http://127.0.0.1:8080/api/users

# Sign in as Maya
curl -i -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"user_id":"00000000-0000-4000-8000-000000000001"}' \
  http://127.0.0.1:8080/api/session

# List Maya's Brains
curl -s -b cookies.txt 'http://127.0.0.1:8080/api/brains?scope=owned'

# Upload a file
curl -s -b cookies.txt \
  -F 'file=@research.md' -F 'object_key=research/research.md' \
  http://127.0.0.1:8080/api/brains/brain.maya/assets
```

Create a path with `POST /api/brains/brain.maya/query-paths`, using asset UUIDs returned by upload:

```json
{
  "path": "/research/share",
  "asset_ids": ["ASSET_UUID"],
  "operations": ["raw_read", "text_search"],
  "visibility": "private",
  "allowed_brain_ids": ["brain.atlas"],
  "allowed_service_ids": ["service.notion", "service.obsidian"],
  "route": {
    "service_hops": ["service.notion", "service.obsidian", "service.notion"],
    "terminal": "brain.atlas"
  },
  "state": "enabled"
}
```

Switch to Atlas and pull the exact saved route with `POST /q/brain.maya/research/share` and body:

```json
{
  "initiating_brain_id": "brain.atlas",
  "query": {"operation": "text_search", "query": "foundation model"}
}
```

Sending uses `POST /api/brains/brain.maya/query-paths/{queryPathId}/send` with an `Idempotency-Key`. Atlas can then accept the pending transfer with `POST /api/transfers/{transferId}/accept`, another idempotency key, and `{"object_key":"inbox/maya-research.json"}`.

## Reset

For a deterministic hackathon reset, use a disposable Supabase project or delete application rows in dependency order, then rerun `db/schema.sql`. Do not edit `storage.objects` directly; object cleanup must go through the Storage API. The schema script safely restores seed identities but intentionally does not delete user-created data.

## v0 boundaries

- Mock sessions are a local authorization boundary, not production authentication.
- Service capability tags are presentation metadata; every Service is an in-process identity transform.
- Routes are finite lists with at most 20 hops. There is no recursion, branching, retry loop, MCP runtime, VM, or WebSocket transport.
- Chat uses the OpenAI Responses API but receives no asset content or metadata and returns `grounded:false`.
- Storage and database writes cannot share a transaction. The implementation uses checksum-derived paths and explicit processing states to make failures recoverable.
