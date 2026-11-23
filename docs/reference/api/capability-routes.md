---
title: Capability routes
description: The intended HTTP surface of cap-hr, cap-process, cap-data, and cap-ai.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Capability routes

Every route is reached through the gateway with a Keycloak bearer token and, for
role-scoped calls, the acting role in `X-Hasura-Role`.
`contracts/runtime-v1.openapi.yaml` describes the cap-process routes.

| Capability | Routes | Status |
| --- | --- | --- |
| `dxos-authz` | `GET /api/auth/me`, `GET /api/auth/apps`; for `role_operator`: `GET /api/auth/users`, `GET /api/auth/roles`, `PUT` and `DELETE /api/auth/users/{user_id}/roles/{role}` ([UI integration](../../guides/integrating-the-ui.md#2-who-am-i-and-what-can-i-open)) | Implemented |
| `cap-hr` | `GET /api/cap/hr/me`, `GET /api/cap/hr/org-units`, `GET /api/cap/hr/staff`; for `role_hr_admin`: `POST /api/cap/hr/org-units`, `PATCH /api/cap/hr/org-units/{id}`, `PUT` and `DELETE /api/cap/hr/staff/{user_id}` ([UI integration](../../guides/integrating-the-ui.md#8-administration)) | Implemented |
| `cap-process` | `POST /api/cap/process/commands/{app_id}/{command}`, `POST /api/cap/process/files/upload`, `GET /api/cap/process/files/{file_id}`, `GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history` (below); `POST /api/cap/process/files/private/upload` for personal library sources ([knowledge contract](../rag/knowledge-contract.md)) | Implemented |
| `cap-process` effects | `POST /api/cap/process/effects/work-item`, for the workflow service only; the gateway does not route it ([events](events.md#workflow-deployment)) | Implemented |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export` | Not in service: the handlers target retired core tables and are not deployed by Compose or routed by the gateway; v1.1.0 (#111) |
| `cap-ai` | [Knowledge and source APIs](../rag/api.md), [optional model answers](../rag/answers.md) and the owner conversations below, under `/api/cap/ai/` | Implemented in the `compose.rag.yaml` overlay. The legacy `/rag` and extraction routes are not available |

## Record history

`GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history` returns the
audit history of one record. `{table}` is the registry table name as the portal
receives it, `schema.table`. Send a bearer token and the acting role in
`X-Hasura-Role`, like a command.

The record is checked with the same viewer and select rules as a command before
its audit rows are read. An unknown table, a child table, a missing record and a
record outside the caller's scope all return 404, so the route does not reveal
existence; a malformed application, table or record id returns 400. The response is
`{"entries":[{"at","action","actor":{"id","display_name"},"acting_role","acting_role_description","version","status"}]}`:
the newest 200 entries, oldest first. `acting_role_description` is the role's
registry description, omitted when empty. `status` is present only for a
state-machine table. Audit payloads, chain/sequence and hash columns are never
returned.

## Owner conversations

Send a bearer access token. The server resolves the active Core user and current
source-app roles. Its base path is `/api/cap/ai/conversations`.

| Method and relative path | Request and response |
| --- | --- |
| `POST` (base path) | Send `{"title":"…"}` to create an owned conversation. Returns 201 with the conversation ID and `created_at` timestamp. |
| `GET` (base path) | List owned conversations. Optional `limit` and `cursor`. Returns `conversations` with their creation timestamps and nullable `next_cursor`. |
| `GET /{id}` | Read ordered owner history. Optional `limit` and `after_seq`. Returns `conversation_id`, `messages` and nullable `next_after_seq`. |
| `DELETE /{id}` | End access to the conversation. Returns 204. |
| `POST /{id}/messages` | Send `{"query":"…"}` and one `Idempotency-Key` per intent. Returns a non-streaming turn. Reuse that key and question on transport retry. |
| `GET /{id}/citations/{chunk}` | Fetch the returned citation URL with its exact `document_id`, `version_id` and assistant `message_seq`. Current READ is checked on every fetch. |

Turn responses contain `status` (`ANSWERED`, `SEARCH_ONLY`, or `NO_EVIDENCE`),
nullable `answer`, and `citations`. They may include `search_status`, `warnings`,
`reason` and `usage`. A terminal response and its replay carry the durable
assistant `message_seq`; the adjacent USER sequence is one less. A pending
replay returns `SEARCH_ONLY` with `TURN_PENDING` and omits the sequence.
When a search fails and no scope completes successfully, the terminal response
is `SEARCH_ONLY` with `SEARCH_UNAVAILABLE`. When some scopes complete and
others fail, the response carries the generic `SEARCH_INCOMPLETE` warning and
may still use authorized evidence. Scope denials are concealed.
Reusing a key with different text or posting a competing intent returns 409.
Unauthorized callers receive 401, foreign conversations and inaccessible
citations return 404, and disabled chat on cap-ai returns 503. Source
revocation preserves the owner's delivered transcript and restricts future
citation access and model context. See the [processing policy](../rag/authorization.md).
