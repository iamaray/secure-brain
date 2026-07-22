# SecureBrain v0 Backend Technical Design

**Status:** Ready for agent implementation  
**Version:** 0.1  
**Date:** July 21, 2026  
**Runtime:** Go 1.25.x modular monolith  
**Infrastructure:** Supabase Postgres + private Supabase Storage, OpenAI Responses API  
**Scope:** Backend only; the frontend will be connected later

---

## 1. Purpose and implementation contract

This document converts the SecureBrain v0 PRD into an executable backend design. An implementation agent should be able to build the backend from this document without making product decisions. When this document and an implementation shortcut conflict, this document wins.

The product is a localhost hackathon demonstration of a policy-controlled data-routing network:

```text
Brain query path -> zero or more identity Service hops -> destination Brain
```

The backend must provide real persistence, file handling, route validation, access control, receiver-approved transfers, audit history, and OpenAI-powered chat. Service nodes are identity transforms only. Chat is deliberately not grounded in uploaded files.

The owner must only need to:

1. Create a Supabase project.
2. Paste the SQL in Section 24 into the Supabase SQL Editor once.
3. Provide the environment variables in Section 5.
4. Run the Go server.

No Supabase dashboard table editing, manual seed insertion, Storage policy creation, or frontend-held secret is required.

### 1.1 Required outcomes

- Mock-select one of at least three seeded users.
- Create and manage Brains and identity Services.
- Upload, overwrite, preview, download, and delete arbitrary files.
- Parse and query `.txt`, `.md`, and `.csv`; route unsupported files as bytes.
- Atomically configure a query path, policy, asset scope, and one finite route.
- Pull authorized data through the exact saved route.
- Push data to a fixed Brain and require receiver acceptance before persistence.
- Project saved routes into a graph response.
- Record mutations, denied access, executions, ordered repeated hops, and transfers.
- Maintain short per-Brain chat history and call OpenAI without using Brain assets.

### 1.2 Non-goals

Do not implement production authentication, Supabase Auth, RAG, embeddings, semantic search, arbitrary service code, MCP, containers, real WebSockets, recursive routes, branching routes, file versions, malware scanning, billing, organizations, or production tenancy claims. A polling-friendly REST API is enough; do not add event streaming unless the frontend later requires it.

---

## 2. Architecture

Use one stateless Go HTTP process and two external systems:

```text
Frontend (later)
    |
    | JSON, multipart uploads, HttpOnly mock-session cookie
    v
Go modular monolith
    |-- domain/application services
    |-- pgx transactional stores --------> Supabase Postgres
    |-- narrow Storage REST client ------> private Supabase Storage bucket
    `-- narrow Responses API client -----> OpenAI
```

### 2.1 Decisions

| Concern | Decision | Reason |
|---|---|---|
| Deployment | One Go process | Lowest operational and agent coordination cost |
| HTTP routing | Go `net/http` `ServeMux` | Go 1.22+ method/wildcard patterns are sufficient |
| Database | Supabase Postgres through `pgx/v5` | Transactions and row locking are required |
| ORM | None | SQL is clearer for this small relational model |
| Files | One private bucket named `securebrain-private` | No file is directly public |
| Storage access | Server-only Supabase service-role key | Browser access is intentionally excluded |
| LLM | OpenAI `POST /v1/responses` | Current API for text and multi-turn workflows |
| Chat state | Local DB messages, `store: false` at OpenAI | Predictable clearing and no provider-side state dependency |
| Execution | Synchronous | Files and routes are bounded for the demo |
| Service behavior | In-process identity transform | No network/service runtime exists in v0 |
| Events | Persisted audit rows; frontend polls | Avoid a second transport system |
| IDs | UUID primary keys plus canonical public IDs | Stable database relations and human-readable graph IDs |

Supabase recommends a direct Postgres connection for a long-lived backend, or session pooler mode when IPv4 requires it. All object mutations must go through the Storage API; never write `storage.objects` directly. The server's service key bypasses Storage RLS and must never leave the backend. See [Supabase database connections](https://supabase.com/docs/guides/database/connecting-to-postgres), [Storage schema guidance](https://supabase.com/docs/guides/storage/schema/design), and [Storage access control](https://supabase.com/docs/guides/storage/security/access-control).

### 2.2 Trust boundary

The browser is untrusted. It may supply a seeded user ID only when starting a mock session. After that, all ownership comes from the server-side session lookup. Never accept `owner_user_id`, `actor_user_id`, or an equivalent ownership field in mutation bodies.

The Go server is the only component allowed to:

- use `DATABASE_URL`;
- use `SUPABASE_SERVICE_ROLE_KEY`;
- use `OPENAI_API_KEY`;
- fetch private object bytes;
- decide authorization;
- create audit records.

---

## 3. Repository and package layout

Agents must converge on this layout. Do not create additional architectural layers without a concrete need.

```text
cmd/server/main.go                 composition root; signal handling only
internal/config/config.go          environment parsing and validation
internal/httpapi/router.go         routes and middleware assembly
internal/httpapi/handlers_*.go     decoding, application calls, response mapping
internal/httpapi/middleware.go     request ID, recovery, logging, session, CORS
internal/domain/models.go          domain types and constants
internal/domain/errors.go          typed application errors and stable codes
internal/store/store.go            pgx stores and transaction helper
internal/store/*.go                resource-specific SQL, no business policy
internal/storage/client.go         Supabase Storage REST adapter
internal/assets/service.go         upload, preview, download, delete
internal/query/service.go          text/CSV/raw query engine
internal/routes/service.go         config validation and execution
internal/transfers/service.go      list, preview, accept, reject, expire
internal/network/service.go        graph projection
internal/audit/service.go          event creation and visibility
internal/chat/service.go           history and OpenAI orchestration
internal/openai/client.go          narrow Responses API HTTP adapter
internal/testutil/                 fixtures and fake external adapters
db/schema.sql                      exact copy of Section 24 SQL
.env.example                       names only; no real secrets
README.md                          setup, run, test, and demo commands
```

Keep `main.go` under 100 lines. It loads config, opens the pool, constructs adapters/services/handlers, starts the server, and performs graceful shutdown.

---

## 4. Dependencies

Keep the dependency set intentionally small:

```text
github.com/jackc/pgx/v5
```

Use the standard library for HTTP, JSON, CSV, MIME detection, hashing, cookies, logging, and the Supabase/OpenAI REST clients. A test assertion package is optional; do not add a web framework, ORM, dependency-injection container, migration framework, queue, or OpenAI/Supabase community SDK for v0.

Use structured `log/slog` JSON logs. Never log cookies, authorization headers, API keys, chat text, file bytes, routed payloads, or full OpenAI requests/responses.

---

## 5. Configuration and startup

### 5.1 Required environment variables

| Variable | Example | Rule |
|---|---|---|
| `DATABASE_URL` | `postgresql://postgres:...@db....supabase.co:5432/postgres` | Required; direct or session-pooler connection |
| `SUPABASE_URL` | `https://project-ref.supabase.co` | Required; no trailing slash after normalization |
| `SUPABASE_SERVICE_ROLE_KEY` | secret service-role value | Required; server-only |
| `OPENAI_API_KEY` | `sk-...` | Required for chat; startup may succeed without it only when `CHAT_DISABLED=true` |
| `SESSION_SECRET` | 32+ random bytes | Required; HMAC-signs the mock-session cookie |

`DATABASE_URL` is a connection secret in addition to an API key. The project owner gets it from Supabase's **Connect** dialog.

### 5.2 Optional environment variables and defaults

| Variable | Default |
|---|---|
| `HTTP_ADDR` | `127.0.0.1:8080` |
| `FRONTEND_ORIGIN` | `http://localhost:3000` |
| `OPENAI_MODEL` | `gpt-5.6-luna` |
| `OPENAI_BASE_URL` | `https://api.openai.com` |
| `STORAGE_BUCKET` | `securebrain-private` |
| `MAX_FILE_BYTES` | `10485760` |
| `MAX_ROUTE_HOPS` | `20` and may not exceed the SQL limit of 20 |
| `MAX_ROUTE_PAYLOAD_BYTES` | `26214400` |
| `MAX_PREVIEW_BYTES` | `262144` |
| `MAX_CSV_ROWS` | `500` |
| `TRANSFER_TTL` | `24h` |
| `CHAT_HISTORY_MESSAGES` | `20` |
| `CHAT_MAX_OUTPUT_TOKENS` | `600` |
| `CHAT_DISABLED` | `false` |
| `LOG_LEVEL` | `info` |

The current official model catalog describes `gpt-5.6-luna` as the cost-sensitive GPT-5.6 model and makes the model configurable here so a project without access can substitute another Responses-compatible model. See [OpenAI models](https://developers.openai.com/api/docs/models).

### 5.3 Startup behavior

Startup must fail with a single actionable message when required configuration is missing, the database is unreachable, or the schema version is absent. Do not print secret values. Verify:

1. required config parses;
2. `pgxpool.Ping` succeeds;
3. `app_meta.schema_version = '1'` exists;
4. the Storage bucket name in configuration is `securebrain-private` unless deliberately overridden.

The server exposes:

- `GET /healthz`: process is alive; no dependency call.
- `GET /readyz`: database ping succeeds; return `503` otherwise.

Use server timeouts: 10 seconds read-header, 30 seconds read, 60 seconds write, 60 seconds idle. The chat handler uses a child context capped at 45 seconds.

---

## 6. Domain model and invariants

### 6.1 Identities

- Brain canonical ID: `brain.<slug>`.
- Service canonical ID: `service.<slug>`.
- Slug regex: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`.
- Names are globally unique within their node type and immutable.
- Display names are optional and mutable.
- Principal strings in policies are canonical Brain or Service IDs, never user IDs.

### 6.2 Query path

A query path belongs to one source Brain and contains:

- a normalized path beginning with `/`;
- an ordered, non-empty asset scope;
- one or more allowed operations;
- `public` or `private` visibility;
- zero or more Brain and Service grants;
- zero or one route while draft, exactly one route while enabled;
- state `draft`, `enabled`, or `disabled`;
- a monotonically increasing `config_version`.

Path rules:

- Accept lower-case URL-safe segments only: `^/[a-z0-9][a-z0-9/_-]*$`.
- Reject `//`, a trailing `/`, `.` or `..` segments, percent-encoded separators, and paths under `/api`, `/q`, `/healthz`, or `/readyz`.
- Unique within a Brain.
- Management uses the query-path UUID. Invocation resolves the Brain canonical ID plus path.

### 6.3 Route

- Ordered list of 0 to 20 Service occurrences. Duplicates are valid.
- Terminal is `caller` or one fixed Brain.
- Execute the stored list once; no recursion, branching, retry loops, or caller-supplied hops.
- Push requires a fixed terminal.
- Pull with fixed terminal requires initiating Brain = fixed terminal.
- Pull with `caller` resolves destination = initiating Brain.
- An execution uses an immutable route/policy/scope snapshot created before asset bytes are read.

For private paths, authorize at execution time:

1. the initiating Brain, unless it is the source Brain;
2. the resolved destination Brain, unless it is the source Brain;
3. every distinct Service in the route.

For a private route to be enabled, its fixed destination and all distinct Services must already be granted. A caller terminal is checked against the Brain grants only at execution time. Public caller-terminal paths accept any registered initiating Brain. Public fixed-terminal paths accept only their fixed destination.

### 6.4 Service identity operation

The internal contract is:

```go
type Payload struct {
    Bytes             []byte
    MediaType         string
    SuggestedFilename string
    Metadata          map[string]any
}

type ServiceExecutor interface {
    Execute(ctx context.Context, service domain.Service, in Payload) (Payload, error)
}
```

The v0 implementation returns the same payload without modifying, copying, serializing, or reordering its data. Compute SHA-256 immediately before the first hop and after every hop. A mismatch is `SERVICE_HOP_FAILED`. Record one `execution_hops` row per occurrence, including repeated Services.

### 6.5 Payload representation

- A one-asset `raw_read` routes the original bytes, media type, and original filename.
- Multiple raw assets route one deterministic JSON manifest with base64 file data.
- Text-search and CSV-query results route deterministic JSON.
- JSON serialization happens once before Service traversal.
- Pull responses wrap the payload as metadata plus either UTF-8 `text` or `data_base64`; never coerce arbitrary bytes to text.
- The payload size after query processing must not exceed `MAX_ROUTE_PAYLOAD_BYTES`.
- An accepted push creates exactly one destination asset. A multi-file or structured result becomes a `.json` asset.

---

## 7. Persistence model

Section 24 is authoritative. The main relationships are:

```text
app_users
  |-- brains -- assets
  |      `-- query_paths -- query_path_assets
  |                         |-- query_path_brain_grants
  |                         |-- query_path_service_grants
  |                         `-- routes -- route_hops -- services
  |-- services
  `-- mock_sessions

query_paths/routes -> route_executions -> execution_hops
                                      `-> transfers -> accepted asset
brains -> chat_messages
all operations -> audit_events -> audit_event_viewers
```

### 7.1 Database rules versus application rules

Use database constraints for shape, uniqueness, referential integrity, and state vocabulary. Use application services for ownership, route completeness, policy membership, operation compatibility, contiguous hop ordering, and cross-resource authorization. Never assume a save-time check replaces execution-time authorization.

Every mutation that changes only Postgres state must run in one transaction and insert its audit event before commit. Query-path create/update must replace asset scope, grants, route, and hops in one transaction.

### 7.2 Concurrency

- Require `If-Match: <config_version>` for query-path update, enable, disable, and delete. Return `409 CONFIG_VERSION_CONFLICT` on mismatch.
- Lock transfers with `SELECT ... FOR UPDATE` during accept/reject/expire.
- Require `Idempotency-Key` on send, accept, and reject. Store the request hash and completed HTTP response. Reuse with a different body returns `409 IDEMPOTENCY_KEY_REUSED`.
- Acceptance uses a deterministic new asset UUID and Storage path recorded before upload; a retry overwrites that same uncommitted path and cannot create a second asset.
- Do not hold database transactions open while calling OpenAI.

---

## 8. Supabase Storage design

The SQL creates a private `securebrain-private` bucket with a 25 MiB per-object limit. Direct user uploads remain capped at 10 MiB by the Go application; the larger bucket ceiling permits bounded multi-file and structured transfer payloads. All operations use the service-role key from the Go server. No object URL is returned to the frontend.

Object paths contain generated IDs and checksums, never user-controlled filenames:

```text
brains/<brain-uuid>/assets/<asset-uuid>/<sha256>
brains/<brain-uuid>/accepted-transfers/<transfer-uuid>/<sha256>
transfers/<transfer-uuid>/<sha256>
```

Object keys such as `research/notes.md` are logical database fields, not Storage paths. Normalize them by converting `\` to `/`, removing a single leading `/`, and rejecting empty segments, `.`, `..`, NUL, control characters, or length over 512 bytes.

### 8.1 Narrow Storage adapter

Expose only:

```go
type ObjectStore interface {
    Put(ctx context.Context, path, mediaType string, body io.Reader, size int64, upsert bool) error
    Get(ctx context.Context, path string) (io.ReadCloser, ObjectMetadata, error)
    Delete(ctx context.Context, paths []string) error
}
```

The adapter calls Supabase Storage REST with both `apikey` and `Authorization: Bearer <service-role-key>` headers. Treat non-2xx responses as typed dependency errors. Limit error bodies to 8 KiB and redact headers.

### 8.2 Upload/overwrite choreography

1. Authenticate and verify Brain ownership before reading the multipart body.
2. Apply `http.MaxBytesReader(MAX_FILE_BYTES + 1 MiB multipart overhead)`.
3. Stream the part to an OS temporary file while calculating byte count and SHA-256; always close and remove the temp file.
4. Reject a logical object-key collision unless `overwrite=true`.
5. Inspect extension/media type and validate supported formats from the temp file.
6. For a new asset, insert it as `uploading` with a newly derived checksum Storage path. For overwrite, retain the old ready row until the new object upload succeeds.
7. Upload to the new Storage path. Do not overwrite the old content path; checksum paths prevent stale reads.
8. Mark the new asset `ready`/`parse_failed`, or atomically point the overwritten row to the new object and metadata, then commit the audit event.
9. After a successful overwrite, best-effort delete the old Storage object. Log only its path and result.
10. On upload failure, mark the new asset `upload_failed`; on overwrite failure, retain the old ready asset unchanged.

Database and object storage cannot share one transaction. The deterministic path and state machine make retries safe. A maintenance function may remove objects associated with `upload_failed` assets, but no background worker is required for v0.

---

## 9. File and query behavior

### 9.1 Format classification

Classify by lower-cased filename extension, not client MIME alone:

| Extension | `format` | Behavior |
|---|---|---|
| `.txt` | `text` | UTF-8 preview, raw read, text search |
| `.md` | `markdown` | UTF-8 source preview, raw read, text search |
| `.csv` | `csv` | table preview, raw read, CSV query |
| other | `binary` | metadata, download, raw read only |

Invalid UTF-8 text is stored but classified `binary`. Malformed CSV is stored as `csv` with `processing_state=parse_failed`; it remains downloadable and supports raw read only.

### 9.2 Preview

- Text/Markdown: return at most `MAX_PREVIEW_BYTES`, a `truncated` flag, and UTF-8 source. Never render Markdown server-side.
- CSV: parse at most 100 data rows for preview; return headers and rows as strings. Prefix cells beginning with `=`, `+`, `-`, or `@` with a single quote in the preview response only so spreadsheet-like frontends do not execute formulas. Stored and routed bytes remain unchanged.
- Binary: metadata only unless `download=true`, which streams original bytes with `Content-Disposition: attachment` and a safely quoted filename.

### 9.3 Operations

Allowed operation names are exactly:

```text
raw_read
text_search
csv_query
```

`raw_read` returns every scoped asset in configured order. `text_search` applies only to ready text/Markdown assets. `csv_query` applies only to ready CSV assets. Reject an operation incompatible with every selected asset; compatible assets may execute while incompatible assets are listed as skipped metadata.

Text search is Unicode-aware case-insensitive substring matching, line by line, with deterministic asset order then line number. Cap at 200 matches and return no more than 200 characters of context per match.

CSV request:

```json
{
  "operation": "csv_query",
  "select": ["author", "score"],
  "filters": [{"column": "score", "operator": ">=", "value": "0.8"}],
  "limit": 100,
  "offset": 0
}
```

Operators are `=`, `!=`, `contains`, `>`, `<`, `>=`, `<=`. Unknown columns/operators are errors. Default limit is 100, maximum is `MAX_CSV_ROWS`, and maximum offset is 10,000.

Inference is per column and per file: a column is numeric only when every non-empty value parses as a finite base-10 `float64`; otherwise it is a string column. Numeric comparison is used only for numeric columns and numeric query values. String equality and ordering are case-sensitive; `contains` is case-insensitive. Empty values remain strings and sort lexicographically. No SQL or arbitrary expression is accepted.

---

## 10. Mock authentication and HTTP security

`POST /api/session` accepts `{"user_id":"<seeded uuid>"}`. If it exists, create a random 32-byte session token, store only its SHA-256 hash in `mock_sessions`, and return a signed cookie containing the raw token. Cookie name: `securebrain_session`; attributes: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Max-Age=43200`, and `Secure` when the request is HTTPS.

The cookie value is `<base64url-token>.<base64url-HMAC-SHA256>`. Verify HMAC in constant time, hash the token, load the non-expired session and user, and update `last_seen_at` at most once per minute. `DELETE /api/session` deletes the current session and expires the cookie.

All `/api/*` and `/q/*` routes except `/api/users` and session creation require a session. Resource handlers must still verify ownership/visibility; authentication alone grants nothing.

For localhost CORS, allow only the exact configured `FRONTEND_ORIGIN`, credentials, `Content-Type`, `Idempotency-Key`, and `If-Match`. Allow only declared methods. Reject state-changing requests whose `Origin` header is present and does not exactly match. Do not use `*` with credentialed requests.

---

## 11. HTTP API conventions

All JSON is UTF-8. Request bodies reject unknown fields and trailing JSON. Default maximum JSON body is 1 MiB. Times use RFC 3339 UTC. IDs in URL paths are canonical IDs for nodes and UUIDs for subordinate records.

Success envelope:

```json
{"data": {}, "request_id": "req_..."}
```

Error envelope:

```json
{
  "error": {
    "code": "PRINCIPAL_NOT_AUTHORIZED",
    "message": "The initiating Brain is not authorized for this path."
  },
  "request_id": "req_..."
}
```

Use `201` for creates, `204` for successful deletes/clear, `400` malformed input, `401` no session, `403` known-but-forbidden owner actions, `404` missing resources and unauthorized private paths, `409` conflicts/idempotent terminal conflicts, `413` size limits, `422` valid JSON with invalid domain configuration, `502` Supabase/OpenAI dependency errors, and `503` readiness failures.

Required stable codes:

```text
INVALID_REQUEST, NOT_AUTHENTICATED, NOT_AUTHORIZED, NODE_NOT_FOUND,
PATH_NOT_FOUND, PATH_DISABLED, OPERATION_NOT_ALLOWED, INITIATOR_NOT_OWNED,
DESTINATION_MISMATCH, PRINCIPAL_NOT_AUTHORIZED, ROUTE_INVALID,
ROUTE_TOO_LONG, ASSET_UNAVAILABLE, ASSET_PARSE_FAILED, QUERY_INVALID,
SERVICE_HOP_FAILED, TRANSFER_ALREADY_RESOLVED, TRANSFER_EXPIRED,
NAME_ALREADY_EXISTS, RESOURCE_IN_USE, CONFIG_VERSION_CONFLICT,
IDEMPOTENCY_KEY_REQUIRED, IDEMPOTENCY_KEY_REUSED, PAYLOAD_TOO_LARGE,
STORAGE_PROVIDER_ERROR, CHAT_PROVIDER_ERROR
```

List endpoints use `limit` (default 50, max 200) and opaque UUID/time cursor pagination. Return deterministic ordering.

---

## 12. Endpoint contract

### 12.1 Session and users

| Method | Path | Authorization | Behavior |
|---|---|---|---|
| `GET` | `/api/users` | none | List seeded mock users; no secrets |
| `POST` | `/api/session` | none | Select user and set cookie |
| `GET` | `/api/session` | session | Active user and mock-auth disclosure |
| `DELETE` | `/api/session` | session | Delete session and cookie |

### 12.2 Brains and Services

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/brains?scope=owned|network` | Owned list or all graph-visible Brains |
| `POST` | `/api/brains` | Create from `slug`, optional `display_name` |
| `GET` | `/api/brains/{brainId}` | Owner detail; graph-safe detail for others |
| `DELETE` | `/api/brains/{brainId}?confirm_id={brainId}` | Owner only; confirmation must match; block active-route references |
| `GET` | `/api/services?scope=owned|network` | Owned or graph-visible Services |
| `POST` | `/api/services` | Create identity Service |
| `GET` | `/api/services/{serviceId}` | Detail |
| `DELETE` | `/api/services/{serviceId}?confirm_id={serviceId}` | Owner only; confirmation must match; block active-route references |

Create body for both node types:

```json
{"slug":"maya-lab", "display_name":"Maya Lab"}
```

### 12.3 Assets

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/brains/{brainId}/assets` | Owner asset list |
| `POST` | `/api/brains/{brainId}/assets` | One multipart `file`, `object_key`, optional `overwrite=true` |
| `GET` | `/api/brains/{brainId}/assets/{assetId}` | Owner metadata |
| `GET` | `/api/brains/{brainId}/assets/{assetId}/content?download=false` | Safe preview or streamed download |
| `DELETE` | `/api/brains/{brainId}/assets/{assetId}` | Block when referenced by an enabled path |

The frontend may upload multiple files concurrently; each HTTP request deliberately handles one file so errors and retries are independent.

### 12.4 Query paths

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/brains/{brainId}/query-paths` | Owner list; public discovery uses network endpoint |
| `POST` | `/api/brains/{brainId}/query-paths` | Atomic path/policy/scope/route create |
| `GET` | `/api/brains/{brainId}/query-paths/{queryPathId}` | Owner configuration |
| `PATCH` | `/api/brains/{brainId}/query-paths/{queryPathId}` | Merge fields, validate complete desired config, atomic replace |
| `DELETE` | `/api/brains/{brainId}/query-paths/{queryPathId}` | Owner, version checked |
| `POST` | `/api/brains/{brainId}/query-paths/{queryPathId}/validate` | Validate proposed full config without saving |

Create/configuration shape:

```json
{
  "path": "/research/share",
  "asset_ids": ["uuid-1", "uuid-2"],
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

Return normalized configuration, route UUID, and `config_version`. PATCH requires `If-Match`. An omitted PATCH field retains its old value; explicit empty arrays clear grants/hops. The service must load, merge, validate, and write the entire resulting configuration in one transaction.

### 12.5 Pull, send, execution

| Method | Path | Behavior |
|---|---|---|
| `POST` | `/q/{sourceBrainId}/{queryPath...}` | Pull using saved route |
| `POST` | `/api/brains/{brainId}/query-paths/{queryPathId}/send` | Source-owner push; fixed terminal only |
| `GET` | `/api/executions/{executionId}` | Participant-visible status and metadata |
| `GET` | `/api/executions/{executionId}/trace` | Participant-visible ordered hops; never payload |

Pull URL example: `POST /q/brain.maya/research/share` resolves path `/research/share`.

Invocation body:

```json
{
  "initiating_brain_id": "brain.atlas",
  "query": {
    "operation": "text_search",
    "query": "foundation model"
  }
}
```

`send` requires `Idempotency-Key` and uses the same `query` shape; initiating Brain is the source Brain and cannot be overridden. Every attempt returns an execution UUID when the source path can be safely identified. Unauthorized exact invocation of a private path returns generic `404 PATH_NOT_FOUND` but still creates a denied audit/execution record visible to permitted owners.

### 12.6 Transfers

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/brains/{brainId}/transfers?direction=incoming|outgoing&status=pending` | Source/destination owner only |
| `GET` | `/api/transfers/{transferId}` | Safe metadata; destination owner may request bounded preview |
| `POST` | `/api/transfers/{transferId}/accept` | Destination owner; requires idempotency key |
| `POST` | `/api/transfers/{transferId}/reject` | Destination owner; requires idempotency key |

Accept body:

```json
{"object_key":"inbox/maya-research.json"}
```

If the proposed key collides, return `409 NAME_ALREADY_EXISTS` with safe metadata and require another key. Repeated acceptance/rejection with the same idempotency key returns the recorded response. A different resolution of a terminal transfer returns `409 TRANSFER_ALREADY_RESOLVED` without creating an asset.

### 12.7 Network and audit

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/network` | Graph nodes and route-derived edges visible to active user |
| `GET` | `/api/network/search?q=` | Search canonical IDs/display names |
| `GET` | `/api/audit-events?node_id=&event_type=&status=` | Viewer-filtered metadata, newest first; all filters optional |

Private paths are omitted from callers who are not owners or authorized initiating/destination Brain owners. Public route summaries are discoverable. Node records never expose owner-private asset metadata.

### 12.8 Chat

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/api/brains/{brainId}/chat` | Owner-only short history plus `grounded:false` |
| `POST` | `/api/brains/{brainId}/chat` | Owner-only model call |
| `DELETE` | `/api/brains/{brainId}/chat` | Owner-only clear |

Chat request is `{"message":"..."}` with 1 to 4,000 Unicode characters. The response is non-streaming JSON for v0.

---

## 13. Route configuration validation

Run all checks and return a list of field errors for owner-facing validate/save endpoints. Stop at a generic not-found for unauthorized execution.

Validation order:

1. Source Brain exists and active user owns it.
2. Query path syntax is normalized, unique, and not reserved.
3. Every asset exists, belongs to the source Brain, is distinct, and is ordered.
4. At least one operation is present; each is known and compatible with at least one selected asset.
5. Visibility is public/private.
6. Every grant resolves to a registered canonical node of the correct type.
7. A route exists before state can be `enabled`.
8. Hop count is within configuration and SQL maximum.
9. Every hop resolves; duplicates remain in their supplied positions.
10. Terminal is exactly `caller` or an existing Brain canonical ID.
11. For private paths, every distinct hop and fixed destination is granted unless destination is the source Brain.
12. For enabled paths, all assets are `ready` or `parse_failed` assets used only by `raw_read`; no `uploading`/`upload_failed` asset is allowed.

`validate` performs no mutation except a `route.validated` audit event for an existing draft. Create/update returns `422 ROUTE_INVALID` with `details.fields`, never protected asset content.

---

## 14. Route execution algorithm

Use one application service for pull and push so their authorization/query/hop behavior cannot drift.

1. Authenticate active user.
2. Resolve and verify the initiating Brain is owned by that user. For send, it is always the source Brain.
3. Resolve source Brain and exact enabled path. Apply private non-disclosure behavior.
4. In a short read transaction, load the path, ordered assets, grants, route, and ordered hops; create an immutable value snapshot and insert `route_executions` with `created` then `authorizing`.
5. Validate current node existence, hop count, terminal rules, operation, and all access rules again.
6. On denial, set `failed`, save a non-sensitive failure code, add viewer-scoped audit events, and return no bytes.
7. Set state `reading`; fetch scoped objects only after all authorization passes.
8. Run the selected query and construct one bounded payload.
9. Set state `processing`; invoke each identity Service in order. Insert one completed hop row with hop index, service snapshot, duration, and input/output checksums.
10. If any hop fails or checksum changes, set execution failed; deliver nothing and do not create a transfer.
11. Pull: set `delivered`, store result metadata only, and return payload in the HTTP response.
12. Push: upload payload to `transfers/<uuid>/<checksum>`, create one pending transfer with expiry, set execution delivered, and return transfer metadata.

The route snapshot JSON must contain source canonical ID/path, config version, visibility, scoped asset IDs/checksums, allowed operation, ordered Service canonical IDs including duplicates, terminal mode, and resolved destination. It must not contain payload bytes, filenames not needed for audit, or API keys.

Execution state updates and audit writes are best effort after the immutable snapshot is created, but a successful delivery must never be returned unless its final database transaction committed.

---

## 15. Transfer lifecycle

### 15.1 Creation

Only successful push execution creates a transfer. Store bytes in the private transfer prefix, persist checksum/size/media type/suggested filename, and set `expires_at = now() + TRANSFER_TTL`. Source and destination owners become audit viewers.

### 15.2 Preview

Only the destination owner may preview payload content. Apply normal preview limits and insert `transfer.previewed`. The source owner may see metadata/status but not use this endpoint as a second payload read channel.

### 15.3 Acceptance

1. Validate destination ownership and idempotency key.
2. Begin transaction and lock the transfer row.
3. If already accepted with the same idempotent request, return the same asset. If another terminal state, return conflict.
4. If expired, atomically mark expired and return `410 TRANSFER_EXPIRED`.
5. Validate/normalize the receiver's object key and reject collision.
6. Generate one asset UUID and the retry-stable destination Storage path `brains/<destination-brain-uuid>/accepted-transfers/<transfer-uuid>/<sha256>`.
7. Fetch transfer bytes, verify stored checksum, and upload to destination path while the transfer remains locked. This bounded v0 transaction may include the Storage call to guarantee one acceptance winner.
8. Insert the asset as ready, set `accepted_asset_id`, mark transfer accepted, insert audit events/idempotency response, and commit.
9. Best-effort delete the transfer Storage object after commit.

If the transaction rolls back after upload, retry uses the same idempotency record/asset identity when present or safely overwrites the checksum-derived orphan path. Never create more than one accepted asset.

### 15.4 Rejection and expiry

Rejection locks and changes only `pending -> rejected`, records audit/idempotency response, commits, then best-effort deletes temporary bytes. Expiry is lazy: before any transfer list/get/resolve, atomically update matching pending rows with `expires_at <= now()` to expired. A periodic process is optional and not required.

---

## 16. Network projection

`GET /api/network` derives the graph; there is no editable `edges` table.

Node key is canonical ID. A route expands to ordered edge keys:

```text
<route-uuid>:0
<route-uuid>:1
...
```

For route `brain.maya:/research/share -> notion -> obsidian -> notion -> brain.atlas`, emit four edges. Repeated node pairs remain separate because edge key includes hop index.

Each node contains `id`, `type` (`brain|service`), `display_name`, `owned`, `status`, and conceptual `ports`. Each edge contains `id`, `route_id`, `query_path_id`, `hop_index`, `from`, `to`, `kind:"service_route"`, and a safe route label. A caller terminal uses synthetic node ID `caller:<route-uuid>` with type `terminal`; it is not a persisted Brain.

The frontend owns coordinates. The backend does not store draft drag lines. A route-building UI must submit a complete query-path configuration before an edge becomes active.

---

## 17. Audit design

Audit payloads are metadata, not tamper-proof logs. Insert events in the same transaction as the associated Postgres mutation whenever possible.

Minimum event types:

```text
session.started
brain.created, brain.deleted
service.created, service.deleted
asset.uploaded, asset.overwritten, asset.parse_failed, asset.deleted
query_path.created, query_path.updated, query_path.enabled
query_path.disabled, query_path.deleted
route.validated
route.execution_started, route.authorization_denied
route.hop_completed, route.execution_completed, route.execution_failed
transfer.created, transfer.previewed, transfer.accepted
transfer.rejected, transfer.expired
chat.requested, chat.failed
```

`audit_event_viewers` materializes which users may see each event. Add the actor, relevant Brain owners, relevant Service owners for hop events, and source/destination owners for transfers. Do not derive visibility solely at read time because nodes may later be deleted.

Metadata may include canonical IDs, resource UUIDs, operation, hop index, status, error category, checksums, byte counts, duration, and config version. It must exclude file bytes, query result content, chat messages, OpenAI output, cookies, headers, and secrets.

---

## 18. OpenAI chat integration

Use a narrow raw HTTP client for `POST {OPENAI_BASE_URL}/v1/responses`. Send `Authorization: Bearer`, `Content-Type: application/json`, and no tools. Set `store:false`. OpenAI recommends the Responses API for current reasoning and multi-turn workflows; this app manages its own short history. See [OpenAI model guidance](https://developers.openai.com/api/docs/guides/latest-model).

Fixed instructions, stored as a Go constant and covered by an exact unit test:

```text
You are the SecureBrain demo assistant for the Brain named {{DISPLAY_NAME}}
({{CANONICAL_ID}}). Answer helpfully and concisely. This is a simulated
Brain-aware experience. You have not been given, cannot inspect, and must not claim
knowledge of files uploaded to this Brain. If asked what is in those files, state
that this v0 chat is not grounded in uploaded content. Do not imply that you can
modify Brains, files, routes, permissions, Services, or transfers.
```

Request shape owned by `internal/openai`:

```json
{
  "model": "gpt-5.6-luna",
  "store": false,
  "instructions": "fixed prompt with Brain identity",
  "input": [
    {"role":"user", "content":"earlier user message"},
    {"role":"assistant", "content":"earlier response"},
    {"role":"user", "content":"current message"}
  ],
  "max_output_tokens": 600,
  "text": {"verbosity":"low"}
}
```

Load at most `CHAT_HISTORY_MESSAGES` preceding messages in ascending order. Do not include asset metadata or contents. Extract text only from assistant message output items whose content type is `output_text`; concatenate in provider order. Treat missing output text, non-2xx, timeout, or malformed JSON as `CHAT_PROVIDER_ERROR`.

Call OpenAI before the database transaction that inserts the user/assistant pair. On success, insert both messages atomically. On failure, insert no conversation messages, emit `chat.failed` metadata, and return a recoverable error. Emit `chat.requested` without message content. Clearing chat deletes only `chat_messages`, not audit metadata.

---

## 19. Coding guidelines for implementation agents

These are acceptance requirements, not style suggestions.

### 19.1 Keep the solution small

- Implement only behavior named in this document.
- Prefer explicit functions and SQL over generic repositories, reflection, code generation, or abstractions with one implementation.
- Introduce interfaces only at external boundaries (`ObjectStore`, `ChatClient`, clock/ID generator in tests).
- Do not create microservices, queues, workers, WebSockets, GraphQL, or frontend code.
- Do not duplicate route authorization in handlers; one route service owns it.
- Do not implement a generic policy language or generic workflow engine.

### 19.2 Go rules

- All I/O functions accept `context.Context` first and honor cancellation.
- Use `errors.Is/As` and one typed `domain.Error{Code, Message, Cause, Details}`.
- Wrap errors with operation context but never secrets or content.
- Validate at boundaries; domain services may assume decoded types but not authorization.
- Use constructor injection. No mutable package globals or `init()` side effects.
- Use UTC at persistence boundaries and inject a clock where expiry/idempotency tests need it.
- Close bodies/rows/files immediately with checked errors where they matter.
- Bound every read with `io.LimitReader` or HTTP body limits.
- Use `crypto/rand` for tokens/UUID source as appropriate, `crypto/subtle` for MAC comparison, and SHA-256 for content checksums.
- Run `gofmt`, `go vet ./...`, and `go test ./...` before handoff.

### 19.3 SQL rules

- Use parameterized SQL only. Never concatenate user values, column names, operators, paths, sort keys, or filters.
- Keep SQL in store methods as readable constants; do not build a generic query builder.
- Always check `rows.Err()` and affected-row counts for versioned mutations.
- Business mutations and audit rows share a transaction.
- Use `SELECT ... FOR UPDATE` only for documented state transitions.
- No handler may access `pgxpool.Pool` directly.
- Do not mutate Supabase's `storage` schema or `storage.objects`; bucket creation in Section 24 is the only direct Storage metadata insert.

### 19.4 HTTP rules

- Handlers only authenticate, decode, call one application operation, and encode.
- Use one shared strict JSON decoder and one shared error writer.
- Set `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` on protected responses, and a request ID on every response.
- Sanitize `Content-Disposition`; never reflect raw filenames into headers.
- Return generic 404 for unauthorized private resources.
- Never return Storage URLs, database errors, stack traces, or provider response bodies.

### 19.5 Tests and fakes

- Unit-test validation, ACL matrices, terminal resolution, repeated hops, payload identity, query semantics, path/key normalization, cookie signing, and provider response parsing.
- Store integration tests run against a disposable Postgres database using `DATABASE_TEST_URL`; skip with a clear message when absent.
- External Storage/OpenAI tests use `httptest.Server`, not real services.
- HTTP tests use `httptest`, a fake application service where appropriate, and assert stable status/error codes.
- Every reported PRD behavior needs at least one happy-path test; every authorization rule needs an allowed and denied test.
- Tests must not depend on execution order, real clocks, seeded production state, or external network access.

### 19.6 Agent handoff discipline

- An agent completes one checklist item at a time and leaves the tree compiling.
- An agent must read this entire document before changing schema/domain contracts.
- If an unavoidable ambiguity is found, record the narrow assumption in `README.md`; do not silently invent a feature.
- Schema changes require updating both `db/schema.sql` and this document's Section 24 in the same change.
- Do not mark a checklist item complete without its stated verification.

---

## 20. Observability and failure handling

Log one structured completion record per request: request ID, method, route pattern, status, duration, active user UUID when authenticated, and stable error code. Do not log raw URL query values for search/chat endpoints.

Dependency clients classify timeouts and non-2xx responses. Retry only idempotent Storage GET/DELETE and OpenAI transport failures, at most once with short jitter, and only while context time remains. Do not automatically retry Storage upload, send, accept, reject, or any database transaction after an ambiguous commit; use idempotency instead.

Graceful shutdown stops accepting new requests, gives active requests 10 seconds, closes idle connections, then closes the database pool.

---

## 21. Performance and bounds

- Metadata target: under 500 ms on the demo machine.
- File preview/route target: under 2 seconds excluding OpenAI.
- Graph target: at least 100 nodes and 250 edges.
- Max upload: 10 MiB by default.
- Max combined routed payload: 25 MiB.
- Max Services: 20 occurrences.
- Max CSV returned rows: 500.
- Max text matches: 200.
- Max list page: 200.
- Database pool: max 10, min 1, max connection lifetime 30 minutes.

Fetch multiple scoped Storage assets sequentially for v0 to keep memory and failure order deterministic. Stream downloads to the caller. Query execution may load bounded files into memory because each object and total payload are capped; document this as a v0 tradeoff.

---

## 22. Definition of done

The backend is complete when a clean Supabase project initialized with Section 24 plus environment secrets can run the canonical demo without direct database manipulation. `README.md` must give exact setup commands, and `go test ./...` must pass without network access.

The canonical demo is:

1. Sign in as Maya.
2. Upload Markdown, text, and CSV to `brain.maya`.
3. Preview Markdown and query CSV.
4. Create private `/research/share`, granting `brain.atlas`, `service.notion`, and `service.obsidian`.
5. Save `notion -> obsidian -> notion -> brain.atlas` and observe repeated ordered graph edges.
6. Sign in as Atlas and pull successfully.
7. Attempt from Anish and receive a non-disclosing denial recorded in audit.
8. Sign in as Maya and send to Atlas.
9. Sign in as Atlas and accept into a chosen object key exactly once.
10. Inspect the complete ordered execution/audit trace.
11. Ask chat a question and see a real OpenAI response clearly marked `grounded:false`.

---

## 23. Sequential implementation checklist

Agents must execute these items in order. Later items may rely on all prior contracts.

- [ ] **1. Normalize the repository.** Move the executable to `cmd/server`, create the package layout in Section 3, update the Go module to a repository-appropriate module path if one is known, and add `.env.example`. Verification: `go test ./...` compiles an empty server skeleton.
- [ ] **2. Install the database schema.** Copy Section 24 byte-for-byte to `db/schema.sql`, apply it to a disposable Supabase/Postgres project, and verify schema version and seed rows. Verification: schema runs once without error and a second run is harmless.
- [ ] **3. Implement configuration and process lifecycle.** Strict environment parsing, redacted errors, database pool, health/readiness, structured logging, timeouts, and graceful shutdown. Verification: config unit tests and `/healthz`/`/readyz` tests.
- [ ] **4. Implement shared domain/error/HTTP primitives.** Constants, IDs, typed errors, strict JSON, envelopes, request IDs, recovery, CORS, security headers, and status mapping. Verification: table-driven decoder and error-mapping tests.
- [ ] **5. Implement mock sessions.** Seed-user listing, random signed cookies, hashed server-side sessions, expiry, session middleware, switching, and logout. Verification: tampered/expired cookies fail and ownership never comes from request bodies.
- [ ] **6. Implement Brain and Service management.** Create/list/detail/delete, slug rules, immutable canonical IDs, ownership, reference checks, and audit viewers. Verification: duplicate names and active-route deletion return stable conflicts.
- [ ] **7. Implement the Supabase Storage adapter.** Bounded Put/Get/Delete with required service headers, response redaction, and fake-server tests. Verification: no Storage URL/key appears in API responses or logs.
- [ ] **8. Implement assets.** Object-key normalization, streaming temp-file upload/checksum, unique checksum paths, overwrite choreography, metadata/list/detail/download/delete, classification, preview, and audit. Verification: supported, malformed, binary, collision, overwrite, size-limit, traversal, and in-use tests.
- [ ] **9. Implement the query engine.** Raw single/multi payloads, deterministic JSON, text search, CSV inference/filter/select/limit/offset, compatibility checks, and bounds. Verification: golden payload tests and all operator/edge-case tests.
- [ ] **10. Implement query-path configuration.** Atomic create/patch/delete/validate of scopes, grants, route, duplicate ordered hops, terminal, state, and version checks. Verification: rollback leaves no partial enabled path; all Section 13 cases are tested.
- [ ] **11. Implement authorization as one reusable policy function.** Cover public/private, caller/fixed, source-owner implicit access, initiating ownership, destination, distinct Services, and private non-disclosure. Verification: a table-driven permissions matrix contains every combination used by pull/send.
- [ ] **12. Implement synchronous route execution.** Immutable snapshot, pre-read authorization, query-before-hop behavior, identity checksums, repeated hop rows, execution states, failure atomicity, pull delivery, and audit visibility. Verification: a 3-hop route with the same Service twice records three ordered events and returns byte-equivalent content.
- [ ] **13. Implement push creation.** Fixed-terminal/source-owner enforcement, idempotency, transfer payload upload, pending row, expiry, and execution completion. Verification: caller terminal is rejected and duplicate idempotency key creates one execution/transfer.
- [ ] **14. Implement transfer list/preview/accept/reject/expire.** Viewer rules, row locks, checksum verification, collision choice, exactly-one accepted asset, terminal idempotency, lazy expiry, cleanup, and audit. Verification: concurrent accept test produces one asset.
- [ ] **15. Implement the network projection.** Visible nodes, public/authorized routes, synthetic caller terminals, repeated edge keys, safe search, and no private leakage. Verification: canonical demo route returns four ordered unique edges.
- [ ] **16. Implement audit and execution read APIs.** Viewer-filtered pagination/filters and safe trace metadata. Verification: Maya, Atlas, Anish, and Service owners each see only their expected subsets; no payload/chat text is stored.
- [ ] **17. Implement OpenAI chat.** Fixed prompt, owner-only history, bounded Responses request with `store:false`, output-text extraction, atomic message pair, clear, failure audit, and fake-provider tests. Verification: request contains no asset data and API key remains server-side.
- [ ] **18. Add end-to-end HTTP tests.** Exercise the entire canonical demo with fake Storage/OpenAI and disposable Postgres, including denial and idempotency paths. Verification: tests run repeatedly and deterministically.
- [ ] **19. Finish operational documentation.** README setup from blank Supabase project, environment acquisition, one-command run/test, API examples, limitations, reset guidance, and canonical demo script. Verification: a fresh agent can follow it without undocumented steps.
- [ ] **20. Final quality gate.** Run `gofmt`, `go vet ./...`, `go test -race ./...`, normal `go test ./...`, and manually scan logs/responses/schema for secrets or payload leakage. Verification: all pass and every checklist item has linked tests or documentation.

---

## 24. Exact Supabase SQL schema

Paste this entire block into the Supabase SQL Editor and run it. It creates the application schema, private bucket, constraints, indexes, safety triggers, schema version, and canonical demo seed. The application must still enforce the cross-row authorization/configuration rules described above.

```sql
begin;

create extension if not exists pgcrypto;

create table if not exists public.app_meta (
    key text primary key,
    value text not null,
    updated_at timestamptz not null default now()
);

insert into public.app_meta (key, value)
values ('schema_version', '1')
on conflict (key) do update
set value = excluded.value,
    updated_at = now();

create table if not exists public.app_users (
    id uuid primary key default gen_random_uuid(),
    handle text not null unique,
    display_name text not null,
    created_at timestamptz not null default now(),
    constraint app_users_handle_format check (handle ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$')
);

create table if not exists public.brains (
    id uuid primary key default gen_random_uuid(),
    owner_user_id uuid not null references public.app_users(id) on delete restrict,
    slug text not null unique,
    canonical_id text generated always as ('brain.' || slug) stored,
    display_name text not null,
    status text not null default 'ready',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint brains_slug_format check (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    constraint brains_canonical_id_unique unique (canonical_id),
    constraint brains_status check (status in ('ready', 'disabled'))
);

create table if not exists public.services (
    id uuid primary key default gen_random_uuid(),
    owner_user_id uuid not null references public.app_users(id) on delete restrict,
    slug text not null unique,
    canonical_id text generated always as ('service.' || slug) stored,
    display_name text not null,
    status text not null default 'ready',
    capability_tags text[] not null default '{}',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint services_slug_format check (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    constraint services_canonical_id_unique unique (canonical_id),
    constraint services_status check (status in ('ready', 'disabled'))
);

create table if not exists public.mock_sessions (
    id uuid primary key default gen_random_uuid(),
    token_hash bytea not null unique,
    user_id uuid not null references public.app_users(id) on delete cascade,
    created_at timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    expires_at timestamptz not null,
    constraint mock_sessions_token_hash_length check (octet_length(token_hash) = 32),
    constraint mock_sessions_expiry check (expires_at > created_at)
);

create table if not exists public.assets (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    object_key text not null,
    storage_path text not null unique,
    original_filename text not null,
    media_type text not null default 'application/octet-stream',
    byte_size bigint not null default 0,
    sha256 text,
    format text not null default 'binary',
    processing_state text not null default 'uploading',
    parse_error text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint assets_object_key_unique unique (brain_id, object_key),
    constraint assets_object_key_nonempty check (length(object_key) between 1 and 512),
    constraint assets_byte_size check (byte_size >= 0 and byte_size <= 26214400),
    constraint assets_sha256 check (sha256 is null or sha256 ~ '^[0-9a-f]{64}$'),
    constraint assets_format check (format in ('text', 'markdown', 'csv', 'binary')),
    constraint assets_processing_state check (processing_state in ('uploading', 'ready', 'parse_failed', 'upload_failed'))
);

create table if not exists public.query_paths (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    path text not null,
    visibility text not null,
    state text not null default 'draft',
    operations text[] not null default '{}',
    config_version bigint not null default 1,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint query_paths_brain_path_unique unique (brain_id, path),
    constraint query_paths_path_format check (
        path ~ '^/[a-z0-9][a-z0-9/_-]*$'
        and path !~ '//'
        and right(path, 1) <> '/'
        and path !~ '(^|/)\.\.?(/|$)'
    ),
    constraint query_paths_visibility check (visibility in ('public', 'private')),
    constraint query_paths_state check (state in ('draft', 'enabled', 'disabled')),
    constraint query_paths_operations check (
        operations <@ array['raw_read', 'text_search', 'csv_query']::text[]
    ),
    constraint query_paths_config_version check (config_version > 0)
);

create table if not exists public.query_path_assets (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    asset_id uuid not null references public.assets(id) on delete cascade,
    position integer not null,
    primary key (query_path_id, asset_id),
    constraint query_path_assets_position_unique unique (query_path_id, position),
    constraint query_path_assets_position check (position >= 0)
);

create table if not exists public.query_path_brain_grants (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    brain_id uuid not null references public.brains(id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (query_path_id, brain_id)
);

create table if not exists public.query_path_service_grants (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    service_id uuid not null references public.services(id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (query_path_id, service_id)
);

create table if not exists public.routes (
    id uuid primary key default gen_random_uuid(),
    query_path_id uuid not null unique references public.query_paths(id) on delete cascade,
    terminal_mode text not null,
    destination_brain_id uuid references public.brains(id) on delete restrict,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint routes_terminal_mode check (terminal_mode in ('caller', 'fixed')),
    constraint routes_terminal_shape check (
        (terminal_mode = 'caller' and destination_brain_id is null)
        or
        (terminal_mode = 'fixed' and destination_brain_id is not null)
    )
);

create table if not exists public.route_hops (
    route_id uuid not null references public.routes(id) on delete cascade,
    hop_index integer not null,
    service_id uuid not null references public.services(id) on delete restrict,
    primary key (route_id, hop_index),
    constraint route_hops_index check (hop_index between 0 and 19)
);

create table if not exists public.route_executions (
    id uuid primary key default gen_random_uuid(),
    mode text not null,
    query_path_id uuid references public.query_paths(id) on delete set null,
    actor_user_id uuid references public.app_users(id) on delete set null,
    initiating_brain_id uuid references public.brains(id) on delete set null,
    source_brain_id uuid references public.brains(id) on delete set null,
    destination_brain_id uuid references public.brains(id) on delete set null,
    source_canonical_id text not null,
    source_path text not null,
    destination_canonical_id text,
    operation text not null,
    state text not null default 'created',
    route_snapshot jsonb not null default '{}'::jsonb,
    result_metadata jsonb not null default '{}'::jsonb,
    error_code text,
    error_message text,
    created_at timestamptz not null default now(),
    started_at timestamptz,
    completed_at timestamptz,
    constraint route_executions_mode check (mode in ('pull', 'push')),
    constraint route_executions_operation check (operation in ('raw_read', 'text_search', 'csv_query')),
    constraint route_executions_state check (state in ('created', 'authorizing', 'reading', 'processing', 'delivered', 'failed')),
    constraint route_executions_snapshot_object check (jsonb_typeof(route_snapshot) = 'object'),
    constraint route_executions_result_object check (jsonb_typeof(result_metadata) = 'object')
);

create table if not exists public.execution_hops (
    id uuid primary key default gen_random_uuid(),
    execution_id uuid not null references public.route_executions(id) on delete cascade,
    hop_index integer not null,
    service_id uuid references public.services(id) on delete set null,
    service_canonical_id text not null,
    status text not null,
    input_sha256 text not null,
    output_sha256 text not null,
    duration_ms integer not null,
    error_code text,
    created_at timestamptz not null default now(),
    constraint execution_hops_order_unique unique (execution_id, hop_index),
    constraint execution_hops_index check (hop_index between 0 and 19),
    constraint execution_hops_status check (status in ('completed', 'failed')),
    constraint execution_hops_input_sha check (input_sha256 ~ '^[0-9a-f]{64}$'),
    constraint execution_hops_output_sha check (output_sha256 ~ '^[0-9a-f]{64}$'),
    constraint execution_hops_duration check (duration_ms >= 0)
);

create table if not exists public.transfers (
    id uuid primary key default gen_random_uuid(),
    execution_id uuid not null unique references public.route_executions(id) on delete restrict,
    source_brain_id uuid references public.brains(id) on delete set null,
    destination_brain_id uuid references public.brains(id) on delete set null,
    source_canonical_id text not null,
    destination_canonical_id text not null,
    status text not null default 'pending',
    storage_path text not null unique,
    suggested_object_key text not null,
    suggested_filename text not null,
    media_type text not null,
    byte_size bigint not null,
    sha256 text not null,
    accepted_asset_id uuid unique references public.assets(id) on delete set null,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    resolved_at timestamptz,
    constraint transfers_status check (status in ('pending', 'accepted', 'rejected', 'expired')),
    constraint transfers_size check (byte_size >= 0 and byte_size <= 26214400),
    constraint transfers_sha check (sha256 ~ '^[0-9a-f]{64}$'),
    constraint transfers_expiry check (expires_at > created_at),
    constraint transfers_resolution_shape check (
        (status = 'pending' and resolved_at is null and accepted_asset_id is null)
        or (status = 'accepted' and resolved_at is not null and accepted_asset_id is not null)
        or (status in ('rejected', 'expired') and resolved_at is not null and accepted_asset_id is null)
    )
);

create table if not exists public.chat_messages (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    user_id uuid not null references public.app_users(id) on delete cascade,
    role text not null,
    content text not null,
    model text,
    created_at timestamptz not null default now(),
    constraint chat_messages_role check (role in ('user', 'assistant')),
    constraint chat_messages_content_length check (length(content) between 1 and 20000),
    constraint chat_messages_model_shape check (
        (role = 'user' and model is null) or (role = 'assistant' and model is not null)
    )
);

create table if not exists public.audit_events (
    id uuid primary key default gen_random_uuid(),
    event_type text not null,
    actor_user_id uuid references public.app_users(id) on delete set null,
    resource_type text not null,
    resource_id uuid,
    brain_id uuid references public.brains(id) on delete set null,
    service_id uuid references public.services(id) on delete set null,
    execution_id uuid references public.route_executions(id) on delete set null,
    status text not null,
    metadata jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    constraint audit_events_status check (status in ('allowed', 'denied', 'succeeded', 'failed', 'pending')),
    constraint audit_events_metadata_object check (jsonb_typeof(metadata) = 'object')
);

create table if not exists public.audit_event_viewers (
    audit_event_id uuid not null references public.audit_events(id) on delete cascade,
    user_id uuid not null references public.app_users(id) on delete cascade,
    primary key (audit_event_id, user_id)
);

create table if not exists public.idempotency_records (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references public.app_users(id) on delete cascade,
    scope text not null,
    idempotency_key text not null,
    request_hash text not null,
    response_status integer,
    response_body jsonb,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    constraint idempotency_records_unique unique (user_id, scope, idempotency_key),
    constraint idempotency_records_key_length check (length(idempotency_key) between 8 and 200),
    constraint idempotency_records_hash check (request_hash ~ '^[0-9a-f]{64}$'),
    constraint idempotency_records_response_status check (response_status is null or response_status between 200 and 599),
    constraint idempotency_records_response_shape check (response_body is null or jsonb_typeof(response_body) = 'object'),
    constraint idempotency_records_expiry check (expires_at > created_at)
);

create index if not exists brains_owner_idx on public.brains (owner_user_id, created_at desc);
create index if not exists services_owner_idx on public.services (owner_user_id, created_at desc);
create index if not exists mock_sessions_user_idx on public.mock_sessions (user_id, expires_at desc);
create index if not exists mock_sessions_expiry_idx on public.mock_sessions (expires_at);
create index if not exists assets_brain_idx on public.assets (brain_id, created_at desc);
create index if not exists assets_state_idx on public.assets (processing_state);
create index if not exists query_paths_brain_state_idx on public.query_paths (brain_id, state, updated_at desc);
create index if not exists query_path_assets_asset_idx on public.query_path_assets (asset_id);
create index if not exists query_path_brain_grants_brain_idx on public.query_path_brain_grants (brain_id);
create index if not exists query_path_service_grants_service_idx on public.query_path_service_grants (service_id);
create index if not exists route_hops_service_idx on public.route_hops (service_id);
create index if not exists route_executions_actor_idx on public.route_executions (actor_user_id, created_at desc);
create index if not exists route_executions_source_idx on public.route_executions (source_brain_id, created_at desc);
create index if not exists route_executions_destination_idx on public.route_executions (destination_brain_id, created_at desc);
create index if not exists route_executions_state_idx on public.route_executions (state, created_at desc);
create index if not exists transfers_source_idx on public.transfers (source_brain_id, created_at desc);
create index if not exists transfers_destination_idx on public.transfers (destination_brain_id, status, created_at desc);
create index if not exists transfers_expiry_idx on public.transfers (expires_at) where status = 'pending';
create index if not exists chat_messages_brain_idx on public.chat_messages (brain_id, created_at desc);
create index if not exists audit_events_created_idx on public.audit_events (created_at desc);
create index if not exists audit_events_resource_idx on public.audit_events (resource_type, resource_id, created_at desc);
create index if not exists audit_events_execution_idx on public.audit_events (execution_id, created_at);
create index if not exists audit_event_viewers_user_idx on public.audit_event_viewers (user_id, audit_event_id);
create index if not exists idempotency_records_expiry_idx on public.idempotency_records (expires_at);

create or replace function public.set_updated_at()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    new.updated_at = now();
    return new;
end;
$$;

drop trigger if exists brains_set_updated_at on public.brains;
create trigger brains_set_updated_at
before update on public.brains
for each row execute function public.set_updated_at();

drop trigger if exists services_set_updated_at on public.services;
create trigger services_set_updated_at
before update on public.services
for each row execute function public.set_updated_at();

drop trigger if exists assets_set_updated_at on public.assets;
create trigger assets_set_updated_at
before update on public.assets
for each row execute function public.set_updated_at();

drop trigger if exists query_paths_set_updated_at on public.query_paths;
create trigger query_paths_set_updated_at
before update on public.query_paths
for each row execute function public.set_updated_at();

drop trigger if exists routes_set_updated_at on public.routes;
create trigger routes_set_updated_at
before update on public.routes
for each row execute function public.set_updated_at();

create or replace function public.block_asset_delete_when_enabled()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.query_path_assets qpa
        join public.query_paths qp on qp.id = qpa.query_path_id
        where qpa.asset_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: asset is referenced by an enabled query path';
    end if;
    return old;
end;
$$;

drop trigger if exists assets_block_enabled_reference on public.assets;
create trigger assets_block_enabled_reference
before delete on public.assets
for each row execute function public.block_asset_delete_when_enabled();

create or replace function public.block_brain_delete_when_active_route()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.query_paths qp
        where qp.brain_id = old.id
          and qp.state = 'enabled'
    ) or exists (
        select 1
        from public.routes r
        join public.query_paths qp on qp.id = r.query_path_id
        where r.destination_brain_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: brain is referenced by an active route';
    end if;
    return old;
end;
$$;

drop trigger if exists brains_block_active_route on public.brains;
create trigger brains_block_active_route
before delete on public.brains
for each row execute function public.block_brain_delete_when_active_route();

create or replace function public.block_service_delete_when_active_route()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.route_hops rh
        join public.routes r on r.id = rh.route_id
        join public.query_paths qp on qp.id = r.query_path_id
        where rh.service_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: service is referenced by an active route';
    end if;
    return old;
end;
$$;

drop trigger if exists services_block_active_route on public.services;
create trigger services_block_active_route
before delete on public.services
for each row execute function public.block_service_delete_when_active_route();

alter table public.app_meta enable row level security;
alter table public.app_users enable row level security;
alter table public.brains enable row level security;
alter table public.services enable row level security;
alter table public.mock_sessions enable row level security;
alter table public.assets enable row level security;
alter table public.query_paths enable row level security;
alter table public.query_path_assets enable row level security;
alter table public.query_path_brain_grants enable row level security;
alter table public.query_path_service_grants enable row level security;
alter table public.routes enable row level security;
alter table public.route_hops enable row level security;
alter table public.route_executions enable row level security;
alter table public.execution_hops enable row level security;
alter table public.transfers enable row level security;
alter table public.chat_messages enable row level security;
alter table public.audit_events enable row level security;
alter table public.audit_event_viewers enable row level security;
alter table public.idempotency_records enable row level security;

revoke all on all tables in schema public from anon, authenticated;
revoke all on all sequences in schema public from anon, authenticated;

insert into storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
values ('securebrain-private', 'securebrain-private', false, 26214400, null)
on conflict (id) do update
set name = excluded.name,
    public = excluded.public,
    file_size_limit = excluded.file_size_limit,
    allowed_mime_types = excluded.allowed_mime_types;

insert into public.app_users (id, handle, display_name)
values
    ('00000000-0000-4000-8000-000000000001', 'maya', 'Maya'),
    ('00000000-0000-4000-8000-000000000002', 'anish', 'Anish'),
    ('00000000-0000-4000-8000-000000000003', 'atlas', 'Atlas')
on conflict (id) do update
set handle = excluded.handle,
    display_name = excluded.display_name;

insert into public.brains (id, owner_user_id, slug, display_name)
values
    ('10000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001', 'maya', 'Maya Brain'),
    ('10000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002', 'anish', 'Anish Brain'),
    ('10000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000003', 'atlas', 'Atlas Brain')
on conflict (id) do update
set owner_user_id = excluded.owner_user_id,
    slug = excluded.slug,
    display_name = excluded.display_name;

insert into public.services (id, owner_user_id, slug, display_name, capability_tags)
values
    ('20000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001', 'notion', 'Notion', array['HTTP', 'Files']),
    ('20000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002', 'obsidian', 'Obsidian', array['MCP', 'Files'])
on conflict (id) do update
set owner_user_id = excluded.owner_user_id,
    slug = excluded.slug,
    display_name = excluded.display_name,
    capability_tags = excluded.capability_tags;

commit;
```
