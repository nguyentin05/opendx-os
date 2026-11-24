---
title: Security invariants
description: Required security properties and the protocol response contract.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

## Security invariants

- The backend authenticates identity and effective permissions; the UI or a role in the request does not grant permissions by itself. Use least privilege for users and service identities.
- Keycloak tokens carry identity only. The roles a caller may act as are read from `core.user_roles` on each request ([ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)); a role named in a header or token claim is a request, never a grant.
- Runtime database connections use least-privilege roles, not `superuser` or `BYPASSRLS`. The operator is the one control-plane exception: it creates schemas, tables, triggers and grants, so in Compose it connects as `POSTGRES_USER`. No request-serving service or Hasura uses that account (#116).
- Request-serving database roles have a 10-second statement timeout. Hasura, cap-process, authz and cap-hr also have a 30-second idle-in-transaction timeout; the cap-ai API keeps only the statement limit. Background knowledge ingestion, the knowledge dispatcher, the model gateway, Kestra and migration/test roles keep no limit because they run long operations. Existing installations apply `0006_runtime_statement_limits.sql` with `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope core --through 0006`, then restart services so new connections pick up the settings.
- Enforce application, record-owner and department scopes in rows and links through both CRUD and service paths. Current DB membership is authoritative; Keycloak verifies identity and JWT role claims cannot override a DB revoke.
- State transitions, version checks, and audit must be atomic. Every record edit participates in the version mechanism; an insert must not bypass the initial state.
- Only clean files with the correct permissions may be used. Ordinary roles may not confirm `CLEAN` themselves; a scan error does not open access.
- Event delivery may repeat; every business effect must tolerate retries. Reconcile and export must also be safe to re-run.
- RAG is limited by app/source/collection, owner and caller permissions; results carry cited sources. OCR must not exceed permissions through a client-supplied `file_id`.
- Audit appends serialize within their explicitly defined chain and have checkpoints outside the DB (checkpoint anchoring is not implemented yet); preserve legacy chain evidence through an approved migration. When changing audit, check concurrent writes and detect edits before the checkpoint. Audit payloads never leave the server raw: record history is a fixed projection of time, actor, action, acting role, version and, for process tables, status.

## Protocol response contract

Command REST: a malformed request or payload returns `400`; missing/invalid authentication returns `401`; a role or `recordFilter` that refuses the command returns `403`; an unknown record, or one the acting role cannot read, returns `404`; a stale version, a state outside the command's `from`, or an idempotency key reused with a different payload returns `409`; a required attachment that is missing or not `CLEAN` returns `422`; an unavailable dependency returns `503`. The read check comes first, so a record outside the caller's scope reveals neither its state, its version nor its files.

Hasura GraphQL: check `data`, `errors`, the number of affected rows, and DB state. A row filter may return an empty result; a disallowed field may cause a GraphQL error even when HTTP is `200`. The requirement is met when no data/relationship outside permissions is leaked and no unauthorized write occurs; not every rejection must be HTTP `403`.
