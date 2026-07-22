# SecureBrain v0 Product Requirements Document

**Status:** Draft for implementation  
**Version:** 0.1  
**Date:** July 21, 2026  
**Product phase:** Hackathon / pre-MVP  
**Primary audience:** Product owner, frontend engineer, backend engineer, demo team

---

## 1. Executive summary

SecureBrain v0 is a localhost demonstration of a private network for personal intellectual context. A user creates a persistent personal node called a **Brain**, uploads files to it, exposes selected data through controlled **query paths**, and connects those paths to other Brains through ordered sequences of **Service nodes**.

In v0, Service nodes are deliberately non-functional identity transforms: each Service accepts data and returns exactly the same data. Their purpose is to make the network, routing, access-control, and audit concepts tangible in the product experience before real computation is introduced.

The defining interaction is:

```text
Brain query path → zero or more Service hops → destination Brain
```

A route may use the same Service more than once and may terminate at its source Brain. Its terminal is either the invoking Brain (`caller`) or one fixed `brain.*` ID. This allows public paths to serve any caller, private paths to enforce allowlists, and the UI to represent complex sequences and closed loops while keeping execution finite and predictable.

The product must demonstrate four core ideas:

1. A Brain is a user-owned container for files and queryable data.
2. A query path is an owner-defined contract for accessing a selected data scope.
3. Access is either public or restricted to explicitly authorized Brain and Service IDs.
4. Data moves only through the route attached to the invoked query path, and every attempted movement is auditable.

This is not a production security platform. It is a coherent, believable product simulation with real file handling, route validation, permissions, identity Service execution, transfer approval, and activity history.

---

## 2. Product vision

People who produce valuable intellectual work should be able to preserve it as a private, controllable advantage while still making it useful to collaborators, agents, and computational services.

SecureBrain ultimately aims to provide a personal security and computation boundary around that work. The broader product may later support real service workloads, MCP agents, private model training, richer storage, and federated networks. The v0 proves the smallest end-to-end version of the underlying interaction model.

### Product thesis

SecureBrain is not primarily a file-sharing product. It is a **policy-controlled data-routing network** in which:

- Brains own persistent data.
- Query paths define how data may be read.
- Services define how data is processed in transit.
- Routes define where data goes.
- Access policies define which identities may participate.
- Audit events make the movement legible.

---

## 3. Goals

### 3.1 Product goals

The v0 must allow a demo user to:

1. Enter the application through a seeded mock-auth flow.
2. Create and manage one or more Brains.
3. Upload and browse arbitrary files in a Brain.
4. Preview `.txt`, `.md`, and `.csv` files using format-aware behavior.
5. Create named query paths over selected files.
6. Mark a query path public or restrict it to named Brain and Service IDs.
7. Create named Service nodes that perform an identity operation.
8. Define a finite route from a Brain query path through an ordered sequence of Services to a destination Brain.
9. Invoke a route from another Brain or from the same Brain.
10. Read routed data or send it to another Brain.
11. Require the receiving Brain's owner to accept pushed data before it is persisted.
12. Visualize Brains, Services, and their connections in a network playground.
13. Inspect an audit trail of allowed and denied access and route execution.
14. Ask a chat model a question “about your Brain” through a clearly mocked experience.

### 3.2 Hackathon goals

- Present a polished, understandable story in a short live demo.
- Keep backend behavior deterministic and easy to seed or reset.
- Prioritize functional completeness of the happy path over production-scale infrastructure.
- Preserve interfaces that can later be replaced with real storage, authorization, services, and AI retrieval.

### 3.3 Success criteria

The build is successful when a presenter can complete the canonical demo in Section 19 without database manipulation, manual API calls, or unexplained mocked UI transitions.

---

## 4. Non-goals

The following are explicitly outside v0:

- Real virtual machines, containers, or per-Brain operating systems.
- Arbitrary user code execution.
- Real Service computation beyond returning the input unchanged.
- Unbounded loops, recursive route execution, conditional branches, or parallel route execution.
- MCP server implementation or agent capability negotiation.
- Real WebSocket data transport between isolated machines.
- Fine-tuning, model-weight upload, GPU workloads, or private inference.
- Vector search, embeddings, retrieval-augmented generation, or semantic indexing.
- Live grounding of chat responses in uploaded Brain content.
- Production authentication, account recovery, email verification, or multi-factor authentication.
- Production-grade cryptographic tenant isolation.
- Organizations, teams, collaborative Brain ownership, or granular user roles.
- Payments, billing, quotas, or a Service marketplace.
- Internet hosting, multi-region operation, high availability, or disaster recovery.
- Malware scanning, content moderation, or legal compliance certification.
- File version history.
- Cross-Brain editing of source files.

The application should not claim these capabilities in the v0 UI.

---

## 5. Users and actors

### 5.1 Seeded user

A locally defined demo identity that can own multiple Brains and Services. A user enters through a mock sign-in or user-selection screen. No password security is implied.

### 5.2 Brain

A persistent personal node owned by one seeded user. It stores files, owns query paths, participates in routes, sends data, receives data, and exposes an identity in the network.

Canonical identifier:

```text
brain.<unique_name>
```

Example:

```text
brain.maya
```

### 5.3 Service

A named network node owned by one seeded user. In v0 it performs exactly one operation:

```text
output = input
```

Canonical identifier:

```text
service.<unique_name>
```

Example:

```text
service.notion
```

### 5.4 Principal

An identity evaluated by access control. In v0, a principal is either a Brain ID or Service ID. User IDs establish ownership but are not entered directly into query-path allowlists.

### 5.5 Initiating Brain

The Brain whose identity starts a read or send execution. A user must own the initiating Brain.

### 5.6 Destination Brain

The final Brain declared in a saved route. It receives the routed result. It may be the same as the source Brain or initiating Brain.

---

## 6. Core domain concepts

### 6.1 Asset

An uploaded file stored inside a Brain. An asset has an owner Brain, object key, original filename, media type, byte size, checksum, upload timestamp, format classification, and processing status.
A query path is configuration, not a filesystem location. It may reference one or more assets regardless of their object keys.

### 6.3 Route

A finite, ordered route attached to a query path:

```text
source query path → service hop 1 → ... → service hop n → terminal
```

Properties:

- A route contains zero or more Service hops.
- The same Service ID may appear multiple times.
- A route has exactly one terminal: either `caller` or a fixed destination Brain ID.
- `caller` resolves to the initiating Brain at execution time.
- A resolved or fixed destination may be the source Brain, producing a closed Brain-to-self loop.
- Execution traverses the saved list exactly once.
- Routes do not branch and do not dynamically call another route.
- There is no `while`, retry loop, recursion, or graph traversal at runtime.

### 6.4 Transfer

A routed payload addressed to a destination Brain. A pull/read returns transient data to the initiating Brain. A push/send creates a pending transfer that the destination owner must accept before the payload becomes a stored asset.

### 6.5 Route execution

One attempt to invoke a query path and traverse its attached route. An execution records its initiator, policy result, selected operation, Services traversed, destination, outcome, timing, and relevant asset identifiers.

---

## 7. Product decisions and resolved ambiguities

### 7.1 Routes are saved source-owned configurations

The owner of a source Brain creates the query path and its route. A caller cannot supply an arbitrary Service chain at request time. This prevents a caller from redirecting protected data through undeclared nodes.

### 7.2 Route integrity is mandatory

Invoking a query path always uses its active route. Callers cannot skip, reorder, add, or remove Service hops.

### 7.3 “Loops” are finite

Two forms of loop are supported:

1. A Service can occur repeatedly in the finite hop list.
2. A route can end at the same Brain that owns the source query path.

An endlessly executing cycle is not supported.

### 7.4 Pull and push are distinct

**Pull/read:** A Brain initiates a read. If the route terminal is `caller`, that Brain becomes the destination. If the terminal is a fixed Brain ID, only that Brain may pull it. If authorized, the result is returned to the user session and is not automatically persisted.

**Push/send:** The owner of the source Brain initiates delivery along a route. The destination receives a pending transfer notification. The payload is persisted only after the destination owner accepts it.

This preserves both requested behaviors: Brains can read authorized paths, and Brains can send data subject to receiver approval.

### 7.5 Terminal binding

The route owner selects one of two deliberately small terminal modes:

- `caller`: Any eligible Brain may invoke the path and becomes the destination. This is the normal mode for a public read path and for a reusable private path shared with several allowlisted Brains.
- `brain.<unique_name>`: The route is bound to one destination. This is useful for a persistent visual connection and is required for push/send.

The terminal cannot be supplied or changed by the caller. A caller therefore controls query parameters, but never the Service sequence or destination rule.

This constraint keeps authorization, visualization, and auditing simple. A later version may support multiple named routes per query path.

### 7.6 Every private-data participant is authorized

For a private query path:

- The initiating Brain must be authorized.
- The resolved or fixed destination Brain must be authorized.
- Every distinct Service ID present in the route must be authorized.

Repeated use of an already authorized Service does not require a second grant.

For a public query path, any registered Brain may invoke a `caller`-terminal route. The owner-defined Service sequence still cannot be changed. A fixed-terminal public route remains callable only by its fixed destination.

### 7.7 Identity Services still produce evidence

Although Services do not alter data, the system records a hop event for each occurrence. If the same Service appears three times, three ordered hop events appear in the execution trace.

---

## 8. Primary user journeys

### 8.1 Create a Brain

1. User signs in through mock auth.
2. User chooses “Create Brain.”
3. User enters a unique name and optional display name.
4. System validates and reserves `brain.<unique_name>`.
5. Brain appears in the dashboard and network playground.

### 8.2 Upload and inspect files

1. User opens a Brain.
2. User uploads one or more files.
3. System stores every accepted file as an asset.
4. Supported formats receive a parsed preview.
5. Unsupported formats show metadata and remain downloadable.

### 8.3 Create a public query path

1. User selects one or more assets.
2. User enters an API-like path.
3. User chooses supported operations.
4. User marks the path public.
5. User chooses an ordered list of Services and selects `caller` as the terminal so any Brain can invoke it.
6. System validates and saves the path and route atomically.
7. The connection becomes visible in the network playground.

### 8.4 Create a private query path

The flow matches the public path flow, with the addition of explicit Brain and Service allowlists. The owner may select `caller` or a fixed destination. The UI shows validation errors if a fixed destination or any route Service is absent from the allowlist.

### 8.5 Pull data from another Brain

1. User selects a Brain they own as the initiating Brain.
2. User invokes a source query path whose route uses `caller` or names that Brain as its fixed terminal.
3. System validates visibility and all participants.
4. System selects data and applies supported query options.
5. System passes the payload through each Service in order.
6. Each Service returns the input unchanged.
7. System returns the final payload without automatically storing it.
8. System records the execution.

### 8.6 Push data to another Brain

1. Source Brain owner invokes “Send” on a query path.
2. System validates the complete route.
3. System produces and routes the payload.
4. System creates a pending transfer for the destination Brain.
5. Destination owner previews metadata and, when safe for the demo, the payload.
6. Destination owner accepts or rejects it.
7. Acceptance persists a new asset in the destination Brain.
8. Rejection does not persist the payload.
9. All state changes are audited.

### 8.7 Ask about a Brain

1. User opens a Brain and enters a chat question.
2. Backend sends the conversation to the OpenAI API using a fixed system prompt.
3. The system prompt simulates a Brain-aware assistant but does not include uploaded files.
4. Response streams or returns to the UI.
5. UI labels the experience as a demo or simulated Brain context.

---

## 9. Functional requirements

Requirement keywords use **MUST**, **SHOULD**, and **MAY** in their conventional product sense.

### 9.1 Mock authentication

- **AUTH-01:** The system MUST provide at least two seeded users so ownership and cross-user sharing can be demonstrated.
- **AUTH-02:** A user MUST be able to enter a local session by selecting or mock-signing-in as a seeded identity.
- **AUTH-03:** The active user MUST be visible in the UI.
- **AUTH-04:** The backend MUST derive ownership actions from the active mock session rather than accepting an arbitrary owner ID in mutation payloads.
- **AUTH-05:** A user MUST be able to switch identities for demo purposes.
- **AUTH-06:** The UI MUST indicate that authentication is mocked and local-only.

### 9.2 Brain management

- **BRN-01:** An authenticated user MUST be able to create multiple Brains.
- **BRN-02:** A Brain MUST have a canonical globally unique ID of `brain.<unique_name>`.
- **BRN-03:** The unique name MUST use lowercase ASCII letters, digits, and hyphens; it MUST begin and end with a letter or digit.
- **BRN-04:** The full canonical ID MUST be unique across all Brains.
- **BRN-05:** The Brain name MUST NOT be editable in v0.
- **BRN-06:** A user MUST be able to list and inspect Brains they own.
- **BRN-07:** The network playground MUST also be able to list all nodes required for the seeded network demonstration.
- **BRN-08:** A user MUST be able to delete a Brain only after an explicit confirmation.
- **BRN-09:** Deleting a Brain MUST be blocked while it is referenced by an active route, unless those references are first removed.
- **BRN-10:** After successful deletion, its unique name MAY be reused immediately.

### 9.3 Service management

- **SVC-01:** An authenticated user MUST be able to create multiple Services.
- **SVC-02:** A Service MUST have a canonical globally unique ID of `service.<unique_name>`.
- **SVC-03:** Service unique names MUST follow the same character rules as Brain names.
- **SVC-04:** A Service name MUST NOT be editable in v0.
- **SVC-05:** Every Service invocation MUST return a byte-equivalent or structurally equivalent payload, depending on the internal payload representation.
- **SVC-06:** Every hop MUST be recorded even when a Service repeats.
- **SVC-07:** Deleting a Service MUST be blocked while it is referenced by an active route.
- **SVC-08:** Service cards MAY display descriptive capability tags for visual polish, but tags MUST NOT imply implemented behavior.

### 9.4 Asset upload and storage

- **AST-01:** A Brain owner MUST be able to upload arbitrary file types.
- **AST-02:** Each asset MUST preserve the original filename and media type where available.
- **AST-03:** Object keys MAY contain folder-like `/` separators.
- **AST-04:** An upload whose object key already exists MUST be rejected unless the user explicitly selects overwrite.
- **AST-05:** Overwrite MUST replace the current asset content; file version history is not required.
- **AST-06:** The system MUST calculate and store file byte size and a checksum.
- **AST-07:** The user MUST be able to list, inspect metadata for, download, and delete their assets.
- **AST-08:** Asset deletion MUST be blocked if the asset is referenced by an active query path, unless it is first removed from that path.
- **AST-09:** The UI SHOULD communicate upload and parsing status.
- **AST-10:** A configurable local file-size limit MUST exist; the v0 default SHOULD be 10 MB per file unless frontend constraints require otherwise.

### 9.5 Supported format behavior

- **FMT-01:** `.txt` files MUST support decoded text preview and raw-text reads.
- **FMT-02:** `.md` files MUST support decoded source preview and raw-text reads; rendered Markdown preview MAY also be provided.
- **FMT-03:** `.csv` files MUST support parsed tabular preview, header discovery, row filtering, and column selection.
- **FMT-04:** CSV filtering MUST use a deliberately small operator set: equality, inequality, contains, greater-than, less-than, greater-than-or-equal, and less-than-or-equal.
- **FMT-05:** CSV reads SHOULD support row limit and offset.
- **FMT-06:** CSV comparison behavior MUST be documented as string-based or type-inferred and remain consistent. For v0, conservative per-column type inference is preferred.
- **FMT-07:** Malformed CSV files MUST remain stored and downloadable but MUST be marked `parse_failed` and treated as generic objects.
- **FMT-08:** Unsupported formats MUST support object storage, metadata display, download, deletion, routing as raw bytes, and no structured query operations.
- **FMT-09:** Text search over `.txt` and `.md` MUST be simple case-insensitive substring matching.
- **FMT-10:** Semantic search is explicitly excluded.

### 9.6 Query-path management

- **QRY-01:** A Brain owner MUST be able to create, inspect, edit, enable, disable, and delete query paths.
- **QRY-02:** A query path MUST begin with `/` and be unique within its Brain.
- **QRY-03:** Reserved application paths MUST be rejected.
- **QRY-04:** A query path MUST reference at least one asset.
- **QRY-05:** A query path MUST declare its supported operation set.
- **QRY-06:** Supported operations in v0 MUST include raw read and MAY include text search, CSV filter, CSV column selection, row limit, and offset where compatible with the selected assets.
- **QRY-07:** A query path MUST be either `public` or `private`.
- **QRY-08:** A private path MUST maintain allowlists of canonical Brain and Service IDs.
- **QRY-09:** Visibility MUST be configured at the query-path level, not independently at file level.
- **QRY-10:** One asset MAY be referenced by multiple query paths with different visibility and routes.
- **QRY-11:** Editing a route or visibility policy MUST affect new executions only.
- **QRY-12:** Disabled paths MUST reject execution but remain editable and visible to their owner.
- **QRY-13:** A query path and its route MUST be saved atomically; the system MUST NOT expose an enabled path with an invalid or partial route.

### 9.7 Route creation and validation

- **RTE-01:** Every enabled query path MUST have exactly one route template.
- **RTE-02:** A route MUST identify its source query path, ordered Service hop list, and terminal.
- **RTE-03:** The Service hop list MAY be empty.
- **RTE-04:** The Service hop list MAY contain duplicate IDs.
- **RTE-05:** The terminal MUST be either the literal `caller` or one canonical Brain ID.
- **RTE-05A:** A fixed destination Brain MAY equal the source Brain; `caller` MAY also resolve to the source Brain.
- **RTE-06:** All referenced nodes MUST exist when the route is saved and invoked.
- **RTE-07:** Route execution MUST traverse the Service list in stored order exactly once.
- **RTE-08:** Runtime recursion, branching, and dynamic hop injection MUST be rejected.
- **RTE-09:** The route MUST have a defensive maximum number of Service hops. The recommended v0 maximum is 20.
- **RTE-10:** For a private path, a fixed destination and every distinct Service in the route MUST appear in their corresponding allowlists before the path can be enabled. For a `caller` terminal, the resolved initiating Brain is checked against the Brain allowlist at execution time.
- **RTE-11:** Route validation failures MUST identify the invalid configuration to the owner without exposing protected content.
- **RTE-12:** An execution MUST use an immutable snapshot of the route as it existed when execution began.

### 9.8 Public and private access

- **ACL-01:** A public query path with terminal `caller` MUST be discoverable and readable by any registered Brain. A public path with a fixed terminal MUST be discoverable but readable only by that Brain.
- **ACL-02:** A private query path MUST NOT be discoverable to unauthorized users or nodes, except by exact-path invocation if the product needs to return a generic not-found response.
- **ACL-03:** A private path MUST authorize the initiating Brain.
- **ACL-04:** A private path MUST authorize the fixed or resolved destination Brain.
- **ACL-05:** A private path MUST authorize every distinct Service in the active route.
- **ACL-06:** Removing a grant MUST block subsequent executions immediately.
- **ACL-07:** Existing completed transfers and accepted copies are not retroactively deleted when a grant is revoked.
- **ACL-08:** Authorization MUST be evaluated again at execution time, not only when a route is created.
- **ACL-09:** The source Brain owner is implicitly authorized to manage and invoke its own paths, but route Services and a different destination still require valid policy entries for private paths.
- **ACL-10:** Authorization failures MUST fail closed and MUST NOT return partial data.

### 9.9 Route invocation

- **EXE-01:** A user MUST choose a Brain they own as the initiating Brain.
- **EXE-02:** For pull/read, `caller` MUST resolve to the initiating Brain. If the route has a fixed terminal, the initiating Brain MUST equal it.
- **EXE-03:** For push/send, the active user MUST own the source Brain.
- **EXE-04:** The system MUST validate the complete route before reading and releasing asset contents.
- **EXE-05:** Query options MUST be restricted to operations enabled by the query path.
- **EXE-06:** Query operations MUST execute before Service traversal.
- **EXE-07:** Each Service MUST receive the output of the preceding stage.
- **EXE-08:** If any hop fails, the entire execution MUST fail and no partial result may be delivered.
- **EXE-09:** The caller MUST receive a stable execution ID.
- **EXE-10:** The final response MUST identify the route, source path, destination, outcome, and result metadata.
- **EXE-11:** Backend errors MAY identify the failed hop but MUST NOT reveal protected payload contents.

### 9.10 Push transfers and approval

- **TRN-01:** Push/send MUST require a fixed Brain terminal; `caller` is not a valid push destination. Successful push execution MUST create a transfer with status `pending`.
- **TRN-02:** A pending transfer MUST be visible only to the destination Brain owner and source Brain owner.
- **TRN-03:** The destination owner MUST be able to accept or reject a pending transfer.
- **TRN-04:** Acceptance MUST be idempotent and persist exactly one new asset.
- **TRN-05:** Rejection MUST be idempotent and MUST NOT create an asset.
- **TRN-06:** Accepted and rejected transfers MUST become terminal.
- **TRN-07:** The receiver MUST choose or confirm the destination object key before acceptance when a filename collision exists.
- **TRN-08:** Pending transfer payloads are temporary. The system SHOULD support a simple expiration period, recommended at 24 hours, after which status becomes `expired`.
- **TRN-09:** A transfer MAY expose a safe preview before acceptance for the demo; access to that preview is itself an audited read.
- **TRN-10:** Revoking source access after a transfer is created does not invalidate a pending transfer in v0; the transfer is treated as a completed send awaiting receiver disposition.

### 9.11 Network playground

Based on the supplied frontend screenshot, the playground MUST support the following product states:

- **NET-01:** Display Personal/Brain nodes and Service nodes using visually distinct shapes.
- **NET-02:** Display canonical IDs, display names, node type, owner-relevant status, and available conceptual ports.
- **NET-03:** Render query-route-derived edges rather than a separate contradictory connection model.
- **NET-04:** Distinguish connection categories visually, including data/WebSocket-style connections and Service-route connections if the frontend preserves both labels.
- **NET-05:** Search by canonical node ID and display name.
- **NET-06:** Select and focus a node.
- **NET-07:** Fit the entire graph into view.
- **NET-08:** Allow the owner to begin route creation by connecting a source Brain/query path to Services and either a fixed destination Brain or a visible `caller` terminal.
- **NET-09:** Convert completed visual connections into a valid saved query-path route, or clearly treat playground-only edges as drafts until required configuration is supplied.
- **NET-10:** Allow an owner to remove a route edge only when the resulting route remains valid, or guide the owner through deleting/disabling the route.
- **NET-11:** Prevent unauthorized users from mutating nodes or routes they do not own.
- **NET-12:** Reflect mutations without a full reload. REST refresh is acceptable; a lightweight event WebSocket is preferred if already compatible with the frontend.

The screenshot's `WS` and `SVC` port labels are treated as product visualization. They do not imply that each Brain is a real VM with separately exposed operating-system ports in v0.

### 9.12 Audit trail

- **AUD-01:** The system MUST record Brain, Service, asset, query-path, route, and transfer mutations.
- **AUD-02:** Every invocation attempt MUST record whether it was allowed or denied.
- **AUD-03:** An execution record MUST include timestamp, initiating Brain, source Brain and path, operation, route snapshot, destination Brain, ordered hop events, result status, and failure category where applicable.
- **AUD-04:** Audit views MUST not store or display full protected payloads.
- **AUD-05:** Brain owners MUST be able to view events concerning Brains and Services they own.
- **AUD-06:** Events SHOULD be ordered newest-first and filterable by node, event type, and status.
- **AUD-07:** Audit events are application records, not a claim of tamper-proof production logging.

### 9.13 Ask-about-your-Brain chat

- **CHAT-01:** A user MUST be able to open a chat associated with a Brain they own.
- **CHAT-02:** The backend MUST call the OpenAI API; the API key MUST remain server-side.
- **CHAT-03:** The request MUST use a hard-coded system prompt that establishes the simulated Brain persona and demo limitations.
- **CHAT-04:** Uploaded Brain assets MUST NOT be automatically inserted into the prompt in v0.
- **CHAT-05:** The UI MUST disclose that answers are simulated and not grounded in uploaded files.
- **CHAT-06:** The backend SHOULD preserve short per-Brain conversation history for the local session.
- **CHAT-07:** The user MUST be able to clear a conversation.
- **CHAT-08:** API failure MUST produce a recoverable UI error without affecting Brain data.
- **CHAT-09:** Chat events MAY be audited as metadata, but prompts and model responses SHOULD be excluded from the general network audit log.

## 10. Data and query behavior

### 10.1 Data-scope semantics

A query path references an ordered list of assets. When multiple assets are selected:

- Raw reads return a manifest plus one result entry per asset.
- Text search applies independently to supported text assets.
- CSV filters apply only to CSV assets.
- An operation incompatible with every selected asset is rejected.
- Unsupported assets may participate only in raw reads.

### 10.2 Text-search semantics

Input:

```json
{
  "operation": "text_search",
  "query": "foundation model"
}
```

Behavior:

- Case-insensitive substring match.
- Results identify asset ID and matching line numbers.
- A bounded amount of surrounding text MAY be returned.
- No ranking beyond deterministic asset and line order is required.

### 10.3 CSV-query semantics

Illustrative input:

```json
{
  "operation": "csv_query",
  "select": ["author", "score"],
  "filters": [{ "column": "score", "operator": ">=", "value": 0.8 }],
  "limit": 100,
  "offset": 0
}
```

Requirements:

- Unknown columns produce a validation error.
- Invalid operators produce a validation error.
- Limit is capped server-side.
- The result includes selected columns, returned row count, and whether more rows are available.
- No arbitrary expressions or SQL are accepted.

### 10.4 Raw payload preservation

Identity Services must not introduce content changes. The system may wrap payloads in routing envelopes, but the data portion before the first Service and after the last Service must compare equal according to its defined representation.

---

## 11. Permissions matrix

| Action                       |                Source Brain owner |                 Authorized caller/destination Brain |                                                  Unauthorized Brain |  Authorized route Service |      Public participant |
| ---------------------------- | --------------------------------: | --------------------------------------------------: | ------------------------------------------------------------------: | ------------------------: | ----------------------: |
| Manage source files          |                               Yes |                                                  No |                                                                  No |                        No |                      No |
| Manage query path            |                               Yes |                                                  No |                                                                  No |                        No |                      No |
| Change route                 |                               Yes |                                                  No |                                                                  No |                        No |                      No |
| Pull private path            |       Yes, if terminal rules pass | Yes, when `caller` or fixed terminal resolves to it |                                                                  No |  Only as a configured hop |                     N/A |
| Pull public path             |       Yes, if terminal rules pass |                                                 Yes | Yes when terminal is `caller`; otherwise only the fixed destination |  Only as a configured hop | Yes, within route rules |
| Push private path            |                               Yes |                                                  No |                                                                  No |  Only as a configured hop |                     N/A |
| Accept pushed data           | No, unless also destination owner |                                                 Yes |                                                                  No |                        No |                      No |
| View relevant audit metadata |                               Yes |                           Transfer/execution subset |                                                                  No | Owner sees Service subset |                      No |

## “Authorized” never permits route mutation or source-file modification.

## 12. Lifecycle and state models

### 12.1 Asset processing state

```text
uploading → ready
          → parse_failed
          → upload_failed
```

`parse_failed` assets remain valid generic objects.

### 12.2 Query-path state

```text
draft → enabled ↔ disabled
  └──────────────→ deleted
```

Only an enabled query path can be invoked. A draft may contain incomplete route or policy configuration.

### 12.3 Route execution state

```text
created → authorizing → reading → processing → delivered
                    └───────────────→ failed
```

The implementation may execute synchronously, but the externally visible state vocabulary should remain stable.

### 12.4 Transfer state

```text
pending → accepted
        → rejected
        → expired
```

All terminal transitions are idempotent.

---

## 13. Preliminary backend product contract

This section defines the capabilities the frontend needs. Exact URL naming may change when the frontend and backend are fitted together.

### 13.1 Session and seeded users

| Method   | Candidate path | Purpose                                            |
| -------- | -------------- | -------------------------------------------------- |
| `GET`    | `/api/users`   | List seeded demo users                             |
| `POST`   | `/api/session` | Select a mock user and create/update local session |
| `GET`    | `/api/session` | Return active user                                 |
| `DELETE` | `/api/session` | Clear mock session                                 |

### 13.2 Brains

| Method   | Candidate path          | Purpose                                 |
| -------- | ----------------------- | --------------------------------------- |
| `GET`    | `/api/brains`           | List visible/owned Brains, with filters |
| `POST`   | `/api/brains`           | Create a Brain                          |
| `GET`    | `/api/brains/{brainId}` | Get Brain detail                        |
| `DELETE` | `/api/brains/{brainId}` | Delete an unreferenced Brain            |

### 13.3 Services

| Method   | Candidate path              | Purpose                        |
| -------- | --------------------------- | ------------------------------ |
| `GET`    | `/api/services`             | List visible Services          |
| `POST`   | `/api/services`             | Create an identity Service     |
| `GET`    | `/api/services/{serviceId}` | Get Service detail             |
| `DELETE` | `/api/services/{serviceId}` | Delete an unreferenced Service |

### 13.4 Assets

| Method   | Candidate path                                   | Purpose                           |
| -------- | ------------------------------------------------ | --------------------------------- |
| `GET`    | `/api/brains/{brainId}/assets`                   | List assets                       |
| `POST`   | `/api/brains/{brainId}/assets`                   | Upload one or more files          |
| `GET`    | `/api/brains/{brainId}/assets/{assetId}`         | Get metadata and processing state |
| `GET`    | `/api/brains/{brainId}/assets/{assetId}/content` | Preview or download content       |
| `DELETE` | `/api/brains/{brainId}/assets/{assetId}`         | Delete an unreferenced asset      |

### 13.5 Query paths and route configuration

| Method   | Candidate path                                             | Purpose                                  |
| -------- | ---------------------------------------------------------- | ---------------------------------------- |
| `GET`    | `/api/brains/{brainId}/query-paths`                        | List owner-visible paths                 |
| `POST`   | `/api/brains/{brainId}/query-paths`                        | Create path and route config             |
| `GET`    | `/api/brains/{brainId}/query-paths/{queryPathId}`          | Inspect config                           |
| `PATCH`  | `/api/brains/{brainId}/query-paths/{queryPathId}`          | Edit scope, policy, operations, or route |
| `DELETE` | `/api/brains/{brainId}/query-paths/{queryPathId}`          | Delete path and its route                |
| `POST`   | `/api/brains/{brainId}/query-paths/{queryPathId}/validate` | Validate a draft without enabling it     |

Illustrative create payload:

```json
{
  "path": "/research/publications",
  "asset_ids": ["asset_123", "asset_456"],
  "operations": ["raw_read", "text_search"],
  "visibility": "private",
  "allowed_brain_ids": ["brain.atlas"],
  "allowed_service_ids": ["service.notion", "service.obsidian"],
  "route": {
    "service_hops": ["service.notion", "service.obsidian", "service.notion"],
    "terminal": "brain.atlas"
  },
  "enabled": true
}
```

### 13.6 Query invocation and sending

Use an internal query-path ID for management and an API-like path for invocation.

| Method | Candidate path                                         | Purpose                                          |
| ------ | ------------------------------------------------------ | ------------------------------------------------ |
| `POST` | `/q/{sourceBrainId}/{*queryPath}`                      | Pull/read through the path's route               |
| `POST` | `/api/brains/{brainId}/query-paths/{queryPathId}/send` | Source-owner push through a fixed-terminal route |
| `GET`  | `/api/executions/{executionId}`                        | Inspect status and ordered trace                 |

The active session plus `initiating_brain_id` identifies the caller. For a reusable read path, the saved route would instead contain `"terminal": "caller"`:

```json
{
  "initiating_brain_id": "brain.atlas",
  "query": {
    "operation": "text_search",
    "query": "foundation model"
  }
}
```

The backend must reject an initiating Brain not owned by the active user.

### 13.7 Transfers

| Method | Candidate path                       | Purpose                           |
| ------ | ------------------------------------ | --------------------------------- |
| `GET`  | `/api/brains/{brainId}/transfers`    | List incoming/outgoing transfers  |
| `GET`  | `/api/transfers/{transferId}`        | Inspect transfer metadata/preview |
| `POST` | `/api/transfers/{transferId}/accept` | Accept and persist payload        |
| `POST` | `/api/transfers/{transferId}/reject` | Reject payload                    |

### 13.8 Playground graph

| Method | Candidate path                    | Purpose                                               |
| ------ | --------------------------------- | ----------------------------------------------------- |
| `GET`  | `/api/network`                    | Return graph nodes, route-derived edges, and statuses |
| `GET`  | `/api/network/search?q=...`       | Search node IDs and names                             |
| `GET`  | `/api/events` or `WS /api/events` | Optional live topology/activity updates               |

The network response should include enough information for the existing frontend to render node type, labels, ownership, conceptual ports, selection state, and edges. Layout coordinates may remain frontend-owned.
### 13.9 Audit

| Method | Candidate path | Purpose |
|---|---|---|
| `GET` | `/api/audit-events` | List events visible to active user |
| `GET` | `/api/executions/{executionId}/trace` | Return ordered hop trace |

### 13.10 Chat

| Method | Candidate path | Purpose |
|---|---|---|
| `GET` | `/api/brains/{brainId}/chat` | Return local conversation history |
| `POST` | `/api/brains/{brainId}/chat` | Send message to mocked Brain-aware model |
| `DELETE` | `/api/brains/{brainId}/chat` | Clear history |

Streaming may use server-sent events or a WebSocket if the frontend already expects token streaming. A non-streaming JSON response is acceptable for v0.

---

## 14. Network graph representation

The graph shown in the frontend must be a projection of saved product state.

### Nodes

- One graph node per Brain.
- One graph node per Service.
- Canonical ID as the stable key.
- Display name as the primary human label.
- Node type controls shape and conceptual ports.

### Edges

One saved route is expanded into ordered visual edges:

```text
brain.source:/path
  → service.one
  → service.two
  → service.one
  → brain.destination
```

Because graph libraries often collapse duplicate node-to-node edges, every visual edge must include route ID and hop index as part of its stable key. This is necessary to represent repeated Services accurately.

Direct Brain-to-Brain visual connections may represent a zero-Service route. If the frontend separately displays “WebSocket” links, those should either map to direct routes or be clearly marked as decorative/demo connectivity; they must not silently create a second access-control system.

---

## 15. Validation and failure behavior

### 15.1 General principles

- Fail closed on authorization or route-integrity errors.
- Return no partial payload after a failed hop.
- Use stable machine-readable error codes and concise human messages.
- Record denied and failed executions.
- Do not expose whether a private path exists to an unauthorized caller.

### 15.2 Required failure categories

- `NODE_NOT_FOUND`
- `PATH_NOT_FOUND`
- `PATH_DISABLED`
- `OPERATION_NOT_ALLOWED`
- `INITIATOR_NOT_OWNED`
- `DESTINATION_MISMATCH`
- `PRINCIPAL_NOT_AUTHORIZED`
- `ROUTE_INVALID`
- `ROUTE_TOO_LONG`
- `ASSET_UNAVAILABLE`
- `ASSET_PARSE_FAILED`
- `QUERY_INVALID`
- `SERVICE_HOP_FAILED`
- `TRANSFER_ALREADY_RESOLVED`
- `NAME_ALREADY_EXISTS`
- `RESOURCE_IN_USE`
- `CHAT_PROVIDER_ERROR`

### 15.3 Failure examples

- If hop 3 of 5 references a deleted Service, execution fails before data is released.
- If a private path authorizes the destination but not one route Service, the path cannot be enabled and execution is denied if stale data bypasses save-time validation.
- If a receiver rejects a transfer, retrying acceptance returns the existing terminal state rather than persisting a copy.
- If an unsupported file receives a CSV query, the request is rejected rather than implicitly returning its raw bytes.

---

## 16. UX requirements inferred from the supplied frontend

The existing visual direction emphasizes a live network rather than a conventional admin dashboard. Backend behavior should support that narrative.

### Network playground

- The central canvas should be populated immediately from seeded data.
- Brain nodes should show display name, canonical ID, node type, and readiness/status.
- Service nodes should show display name, canonical ID, and non-binding descriptive tags.
- Selecting a node should expose details and valid actions without navigating away if possible.
- Dragging from a labeled port should begin a route-building interaction, not create an active connection until required query path, visibility, and destination fields are valid.
- “Remove line” should delete or edit the owning route only after confirmation.
- Search should locate both Brains and Services by canonical ID or display name.
- Connection styles must have a legend and deterministic meaning.
### Honesty of the demo

- “Live” may mean responsive local state; it must not imply a production network.
- Service labels such as HTTP, MCP, Files, Notion, or Obsidian are presentation metadata only unless implemented.
- Chat must state that its Brain knowledge is simulated.
- Identity Service execution should still animate each hop, making the future computation model understandable without pretending transformation occurred.

---

## 17. Non-functional requirements

### 17.1 Local operation

- The complete application MUST run on localhost.
- Startup SHOULD require one documented command or a minimal sequence.
- Seed/reset behavior SHOULD be deterministic.
- The application SHOULD remain functional without external network access except for the OpenAI chat call.

### 17.2 Performance

- Routine metadata views SHOULD respond within 500 ms on the demo machine.
- Small file previews and route executions SHOULD complete within 2 seconds, excluding chat-provider latency.
- Network graph loading SHOULD support at least 100 nodes and 250 rendered edges, though the canonical seed should remain smaller and legible.
- CSV result size and file preview size MUST be capped.

### 17.3 Reliability

- Mutations affecting query path, policy, and route MUST be atomic from the frontend's perspective.
- Duplicate accept/send requests MUST not create duplicate stored assets or transfers.
- A failed Service hop MUST leave source assets unchanged.
- Seed data MUST be recoverable through a documented reset mechanism.

### 17.4 Security posture for v0

- Treat mock sessions as authorization boundaries within the application even though they are not production authentication.
- Prevent direct object access across mock users by validating ownership server-side.
- Keep the OpenAI API key on the backend and out of logs and frontend bundles.
- Validate canonical IDs, paths, file keys, and query operators.
- Prevent filesystem path traversal in object keys and downloads.
- Avoid interpreting uploaded Markdown as trusted HTML.
- Avoid formula execution when previewing CSV content.
- Cap request, file, query-result, and route sizes.
- Do not log full routed payloads by default.
### 17.5 Accessibility and clarity

- Do not rely on edge color alone to indicate connection type or status.
- Provide textual route order and access-policy summaries alongside the graph.
- Confirmation dialogs must identify the node, route, asset, or transfer being changed.

---

## 18. Audit event catalog

Minimum event types:

```text
session.started
brain.created
brain.deleted
service.created
service.deleted
asset.uploaded
asset.overwritten
asset.parse_failed
asset.deleted
query_path.created
query_path.updated
query_path.enabled
query_path.disabled
query_path.deleted
route.validated
route.execution_started
route.authorization_denied
route.hop_completed
route.execution_completed
route.execution_failed
transfer.created
transfer.previewed
transfer.accepted
transfer.rejected
transfer.expired
chat.requested
chat.failed
```

`route.hop_completed` should record the hop index so repeated Service occurrences remain distinguishable.

---

## 19. Canonical hackathon demo

### Seed state

- User Maya owns `brain.maya`.
- User Anish owns `brain.anish`.
- User Atlas owns `brain.atlas`.
- At least two identity Services exist, such as `service.notion` and `service.obsidian`.
- The network playground initially resembles the supplied frontend composition.
### Demo sequence

1. Sign in as Maya.
2. Create or open `brain.maya`.
3. Upload `research.md`, `notes.txt`, and `results.csv`.
4. Preview the Markdown and filter the CSV.
5. Create private path `/research/share` over selected assets.
6. Authorize `brain.atlas`, `service.notion`, and `service.obsidian`.
7. Configure the finite route:

   ```text
   brain.maya:/research/share
     → service.notion
     → service.obsidian
     → service.notion
     → brain.atlas
   ```

8. Show the repeated Service and ordered edges in the playground.
9. Switch mock identity to Atlas and pull the path as `brain.atlas`.
10. Animate or inspect each identity hop and show the unchanged result.
11. Switch to an unauthorized Brain and show a safe denial.
12. Switch to Maya and send the route output to Atlas.
13. Switch to Atlas; accept the pending transfer and show the new stored asset.
14. Inspect the audit trace, including repeated Service hops.
15. Ask the Brain chat a question and show the explicit simulated-context label.
16. Return to Maya, revoke Atlas, and demonstrate that a new pull is denied.

This sequence demonstrates storage, structured access, routing, repetition, private authorization, receiving approval, auditability, the network UI, and the AI-facing product vision.

---

## 20. Acceptance criteria

The v0 is complete only when all of the following pass:

### Brain and node identity

- A seeded user can create multiple Brains and Services.
- Canonical IDs use the required prefixes and reject duplicate names.
- Deletion is blocked for nodes used by active routes.

### Files

- Arbitrary files upload and download successfully within configured limits.
- `.txt` and `.md` preview as text.
- Valid `.csv` previews as a table and supports the required filters and column selection.
- Malformed CSV remains downloadable and is clearly marked.
- Duplicate object keys require explicit overwrite.

### Query paths and routes

- A user can create a query path over selected assets.
- Visibility is configurable as public or private at the path level.
- Private allowlists accept canonical Brain and Service IDs.
- A route supports zero Services, repeated Services, and a destination equal to its source Brain.
- A route longer than the configured maximum is rejected.
- The caller cannot modify the Service chain during invocation.
## 21. Prioritization

### P0 — demo cannot ship without it

- Mock-user selection and server-side ownership checks
- Brain creation and listing
- Service creation and listing
- File upload, listing, preview, download, and deletion
- Format-aware `.txt`, `.md`, and `.csv` behavior
- Query-path creation with path-level public/private policy
- Finite route creation with repeated identity Services
- Pull execution and authorization
- Push transfer with receiver acceptance/rejection
- Network graph read model
- Minimal execution/audit history
- Mock Brain chat using the OpenAI API

### P1 — strongly improves the demo

- Visual route builder connected to real saved configuration
- Animated route execution
- Live graph refresh
- Query-path draft validation
- CSV pagination
- Transfer expiration
- Audit filtering
- Deterministic seed/reset command

### P2 — cut first if time is constrained

- Rendered Markdown preview
- Service capability tags
- Persistent chat history
- WebSocket rather than polling for network updates
- Surrounding context in text-search results
- Advanced graph deletion/edit interactions

---

## 22. Risks and mitigations

### The graph becomes a second data model

**Risk:** Visual edges and saved routes disagree.  
**Mitigation:** Derive active graph edges from route configuration. Treat unfinished drag interactions as drafts only.

### “Loop” implies unbounded execution

**Risk:** A visual cycle creates hangs, resource exhaustion, or unclear output.  
**Mitigation:** Store an ordered finite hop list, allow repeated IDs and Brain-to-self destinations, cap hops, and forbid runtime recursion.

### Access control becomes difficult to explain

**Risk:** Source owner, caller, destination, and Services each appear to need different permissions.  
**Mitigation:** Use one rule: every non-owner node that touches private data must be listed. A `caller` terminal resolves to the initiating Brain; a fixed terminal must match it for pull.

### Public data appears globally exfiltratable

**Risk:** “Public” could be interpreted as allowing arbitrary Service chains or push destinations.  
**Mitigation:** Public removes the caller allowlist for a `caller`-terminal read, but never lets a caller change the owner-defined Service sequence or turn a read into a push.

### Service cards over-promise integrations

**Risk:** Labels such as Notion, Obsidian, MCP, or HTTP suggest real integrations.  
**Mitigation:** Mark Services as demo identity transforms in detail views and presentation copy.

### Chat appears grounded when it is not

**Risk:** Users infer that answers use uploaded assets.  
**Mitigation:** Persistent disclosure in the chat UI and a hard-coded system prompt that avoids claims of actual retrieval.

### Receiver approval still exposes a preview

**Risk:** Showing full content before acceptance undermines the meaning of approval.  
**Mitigation:** Define approval as consent to persistence, not consent to network delivery. Prefer metadata/limited preview and explain this distinction in the UI.

### Hackathon scope expands into infrastructure

**Risk:** Time is spent simulating VMs, real sockets, or service runtimes.  
**Mitigation:** Model Brains and Services as application entities; keep ports conceptual; implement one deterministic route executor.
---

## 23. Deferred product questions

These questions do not block v0 but must be revisited before an MVP:

- Can a query path support multiple named routes or dynamic destinations?
- Can receivers approve data before any payload bytes reach their security boundary?
- Are Services authorized to initiate calls, or only to act as route hops?
- How are Service implementations verified, sandboxed, and prevented from retaining data?
- Does “public” mean discoverable, callable, readable, or some configurable combination?
- How does a Brain expose capabilities through MCP without exposing raw storage?
- How are policies attached to derived data and copied assets?
- What is the production identity and key-management system?
- Should query paths be versioned, and should executions pin those versions?
- How are real cyclic workflows represented with termination conditions?
- How are model training jobs brought to data and how are weights governed?

---

## 24. Implementation handoff boundary

This PRD intentionally defines product behavior without choosing the backend framework, database, object-store implementation, queue, or deployment model. The subsequent technical design should translate these requirements into:

- Persistence schema and relationships.
- Route execution algorithm.
- Authorization evaluation order.
- Asset storage and safe parsing strategy.
- API request and response schemas fitted to the frontend.
- Graph projection logic.
- OpenAI chat integration boundary.
- Seed/reset tooling.
- Test plan mapped to the acceptance criteria above.

The technical design must preserve the v0's core invariant:

> An enabled query path has one owner-controlled, finite route; every invocation uses that route exactly, every private-data participant is authorized, and every attempted movement is recorded.
