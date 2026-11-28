---
title: Knowledge API
description: HTTP contract for scoped knowledge ingestion and retrieval.
diataxis: reference
audience: [app-author, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# Knowledge and Assistant API

## Public Knowledge API

All public endpoints use `/api/cap/ai`. Knowledge and administration calls
require a verified bearer token and a current selected `X-Hasura-Role`.
Assistant calls use the verified subject and resolve each app role on the
server. `GET /health` checks deployed database readiness without content.

| Method and path | Behavior |
| --- | --- |
| POST /documents | Register a clean private `file_id` with `standard-text-v1`; repeat requests return the existing registration |
| GET /documents | Paginated owner document metadata and ingestion status |
| DELETE /documents/{id} | Owner removal with `If-Match` revision |
| GET /documents/{id}/original | Reauthorize and return the bounded original or redirect to the protected File Service |
| POST /search | Lexical or hybrid search with PERSONAL, APP, or ALL scope |
| GET /documents/{id}/citations/{chunk} | Live preview and provenance; requires the current `version_id` |
| GET /app-knowledge | Readable sources and separate grant management affordances |
| GET /documents/{id}/grant-management | Grant list, current ceiling and revision |
| POST /documents/{id}/grants | Add a named user grant with `If-Match` |
| DELETE /documents/{id}/grants/{user} | Revoke a named user grant with `If-Match` |
| POST /answers | Evidence-grounded application answer, lexical results if generation is unavailable |
| GET, POST /conversations | List or create owner-only conversations |
| GET, DELETE /conversations/{id} | Paginated history or owner removal |
| POST /conversations/{id}/messages | Non-streaming turn with UUID `Idempotency-Key` |
| GET /conversations/{id}/citations/{chunk} | Owner and live source validation against a persisted message citation |

Unauthenticated, forbidden, concealed, conflict and unavailable operations
use HTTP 401, 403, 404, 409 and 503 as applicable. Assistant responses also
report explicit unavailable and insufficient-evidence outcomes. No API
returns raw tool transcripts. File upload and scan status continue through
the existing Core File Service and Hasura.

## Internal source and publication endpoints

Only the verified dispatcher subject may POST
`/internal/v1/package-sources/reconcile`. The server validates the current
Core app, generation and spec digest before registration. The deployment
routes only public `/api/cap/ai/*` paths through APISIX.

The File Service subject may POST `/internal/v1/source-delegations/validate`.
The grant is bound to the current ingestion lease, exact worker subject,
owner, clean original and content hash. File Service checks it before
reading and before returning bytes.

## Model Gateway

The four slots use one LiteLLM-based gateway. Platform Admin calls under
`/admin` manage providers, immutable model revisions, slot bindings and
policy. Registry reads return credential presence and safe audit and
operation state. Synthetic probes use fixed inputs.

The gateway evaluates current role, source and processing policy before
dispatch, then claims a durable operation and records that it may have
sent a request. The final transport check repeats authority and
destination checks. An uncertain operation is never sent again. Hosted
policy is exact-revision and default-deny. [Processing
policy](authorization.md#model-processing-policy).
