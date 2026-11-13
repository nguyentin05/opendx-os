# 08 — Proposed Knowledge API, File Integration and Durable Events

**All endpoint paths and payloads in this chapter are PROPOSED unless specifically marked VERIFIED.** Do not code clients against these without implementing and versioning the contract.

## 1. Existing baseline vs proposed surface

**VERIFIED:** `capabilities/cmd/cap-ai/main.go` exposes `POST /api/cap/ai/extract` and `POST /api/cap/ai/rag`. The `rag` handler currently parses `app_name` + `query` and does **not** call `auth.ExtractIdentity`. The `extract` handler obtains identity but `ai.go` queries legacy `core.file_objects` with `workspace_id` and returns mocked extraction fields. The current RAG logic scans local app Markdown and keyword-matches text.

**PROPOSED additions:**

| Endpoint | Purpose | Auth and action |
|---|---|---|
| `POST /api/cap/ai/collections` | Create a personal/app collection | Trusted identity + app/collection create |
| `GET /api/cap/ai/collections` | List only accessible collections | Scoped list |
| `POST /api/cap/ai/documents` | Register existing CLEAN `file_id` as a new document | Source file permission + create |
| `GET /api/cap/ai/documents/{id}` | Metadata/status only if readable | Document READ |
| `POST /api/cap/ai/documents/{id}/versions` | Version update with new CLEAN source | Document MANAGE |
| `PUT /api/cap/ai/documents/{id}/policy` | Set visibility/roles under policy ceiling | Document MANAGE + app admin policy |
| `POST /api/cap/ai/documents/{id}/grants` | Grant named user READ | Document MANAGE + allowed grant ceiling |
| `DELETE /api/cap/ai/documents/{id}/grants/{userId}` | Revoke grant | Document MANAGE |
| `POST /api/cap/ai/search` | Authorized lexical/vector search | Authenticated user |
| `POST /api/cap/ai/answers` | Search + grounded generation | Authenticated user + model quota |
| `GET /api/cap/ai/documents/{id}/citations/{chunkId}` | Protected citation/preview | Document READ + version/source |
| `GET /api/cap/ai/ingestions/{jobId}` | Upload processing progress | Source owner/authorized manager |
| `POST /internal/cap/ai/package-sources/reconcile` | Reconcile trusted Operator source snapshot | Signed service identity, not public |
| `GET /api/cap/ai/admin/model-usage` | Aggregated metering | Explicit platform operations permission |

Maintain compatibility or introduce a deliberate version/transition for existing endpoints; avoid quietly changing semantics in production. Do not use unscoped `GET /documents` that lists titles to unauthorized users.

## 2. Proposed register-document request

```http
POST /api/cap/ai/documents
Authorization: Bearer <user-token>
Idempotency-Key: <stable-client-uuid>
Content-Type: application/json

{
  "file_id": "file-uuid",
  "collection_id": "collection-uuid",
  "title": "My onboarding notes",
  "visibility": "PRIVATE"
}
```

Server derives `owner_user_id` and allowed app context from the authenticated session and collection; does not accept client-controlled `owner_user_id`, role set, `workspace_id` or `scan_status`. Return `document_id`, `version_id`, `state = WAITING_SCAN/QUEUED` with HTTP `202` only after durable registration.

Because scan may still be in progress, either require `CLEAN` at registration or explicitly create `WAITING_SCAN` state that never indexes until trusted scanner marks CLEAN. The flow chosen must be one documented contract, not an ambiguous mix.

## 3. Proposed answer request and response

```http
POST /api/cap/ai/answers
Authorization: Bearer <user-token>
Content-Type: application/json

{
  "app_id": "procurement",
  "collection_ids": ["collection-uuid"],
  "query": "Who approves a quote above 10 million VND?",
  "max_citations": 5
}
```

`app_id` and `collection_ids` are **scope requests**, not authority; server intersects them with available grants. It is invalid to accept `role_hr` or arbitrary `user_id` as an authorization decision.

```json
{
  "answer": "The approval policy requires a director for quotes of 10 million VND or more.",
  "grounded": true,
  "citations": [
    {
      "document_id": "doc-uuid",
      "version_id": "version-uuid",
      "chunk_id": "chunk-uuid",
      "page": 4,
      "section": "Approval thresholds",
      "source_url": "/api/cap/ai/documents/doc-uuid/citations/chunk-uuid"
    }
  ],
  "model_profile": "answer-default",
  "usage": {
    "prompt_tokens": 840,
    "completion_tokens": 78
  }
}
```

Never return provider credentials, raw object keys or unrestricted source URLs.

## 4. Proposed event / job envelope

```json
{
  "event_id": "event-uuid",
  "event_type": "ai.document.version.registered",
  "schema_version": 1,
  "app_id": "procurement",
  "document_id": "doc-uuid",
  "version_id": "version-uuid",
  "source_kind": "UPLOAD",
  "source_ref": "file-uuid",
  "occurred_at": "2026-11-13T00:00:00Z"
}
```

Do not publish document bytes, private text, credentials or client-supplied roles in events. Worker reads authorized source through a trusted service identity and rechecks CLEAN before processing.

```mermaid
sequenceDiagram
    participant API as Knowledge API
    participant PG as PostgreSQL
    participant BUS as Durable job/outbox relay
    participant W as Ingestion worker
    API->>PG: Transaction: document version + job record
    PG-->>API: Committed
    BUS->>PG: Lease due job / event
    BUS->>W: Retryable processing request
    W->>PG: Persist structured elements + chunks by version
    W->>PG: Mark candidate READY
    W->>PG: Atomic publish if source still valid
    Note over BUS,W: Duplicates allowed; effects idempotent
```

A dedicated `cap_ai.ingestion_jobs` poller can serve as the durable queue for MVP, avoiding a new message broker. Use existing transactional outbox patterns when cross-service event delivery is needed. Never rely on a single best-effort HTTP callback with no durable record.

## 5. Error/status semantics (proposal)

| Condition | Suggested status | Data disclosure rule |
|---|---|---|
| Unauthenticated | 401 | No resource information |
| Authenticated but forbidden | 403 or concealed 404 | Never expose private title/content |
| No such visible document | 404 | No unauthorized metadata |
| Duplicate idempotency key, different payload | 409 | No duplicate document/job |
| Unsupported file / unclean source | 415 / 422 | No OCR/embedding; scanner error safe |
| Excessive request size | 413 | Bound parse and model cost |
| User/app/model quota exceeded | 429 | No model call |
| Restricted external model egress | 403 or explicit policy status | No content sent to provider |
| Async job accepted | 202 | Durable version/job IDs only |

## 6. Concurrency and authorization

- Idempotency keys scope to actor + app + action + source + payload fingerprint; a retry after commit returns the original result after rechecking permissions.
- Policy changes use optimistic version/ETag or transaction lock; concurrent grant/revoke must not lose updates.
- Query uses an authorization snapshot and a policy revision; recheck immediately before external rerank/generation/response.
- Events may repeat; enforce unique job and chunk keys.
- Model usage reservation and settlement must be concurrency safe to enforce spend ceilings.
- Never let legacy `rag` remain a bypass around the new secured endpoint; either secure it or deprecate it with a carefully tested compatibility plan.

## 7. Trusted service identity

Operator-to-Knowledge and worker-to-File use separate service identities with minimum scoped privileges. A service token must not be treated as a user with unlimited document READ; worker source access is constrained to its assigned job and `CLEAN` source. Evaluate APISIX/Keycloak service account configuration actually present in the deployment before fixing an auth method.
