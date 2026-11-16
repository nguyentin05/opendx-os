---
title: Security invariants
description: Required security properties and the protocol response contract.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## Security invariants

- The backend authenticates identity and effective permissions; the UI or a role in the request does not grant permissions by itself. Use least privilege for users and service identities.
- Keycloak tokens carry identity only. The roles a caller may act as are read from `core.user_roles` on each request ([ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)); a role named in a header or token claim is a request, never a grant.
- Runtime database connections use least-privilege roles, not `superuser` or `BYPASSRLS`.
- Enforce application, record-owner and department scopes in rows and links through both CRUD and service paths. Current DB membership is authoritative; Keycloak verifies identity and JWT role claims cannot override a DB revoke.
- State transitions, version checks, and audit must be atomic. Every record edit participates in the version mechanism; an insert must not bypass the initial state.
- Only clean files with the correct permissions may be used. Ordinary roles may not confirm `CLEAN` themselves; a scan error does not open access.
- Event delivery may repeat; every business effect must tolerate retries. Reconcile and export must also be safe to re-run.
- RAG is limited by app/source/collection, owner and caller permissions; results carry cited sources. OCR must not exceed permissions through a client-supplied `file_id`.
- Audit appends serialize within their explicitly defined chain and have checkpoints outside the DB; preserve legacy chain evidence through an approved migration. When changing audit, check concurrent writes and detect edits before the checkpoint.

## Protocol response contract

Command REST: missing/invalid authentication returns `401`; authenticated but insufficient permission returns `403`; a stale version or conflicting idempotency key returns `409`; using an unclean file returns `422`. Perform the permission check before revealing the state/version/file of a resource outside the caller's scope.

Hasura GraphQL: check `data`, `errors`, the number of affected rows, and DB state. A row filter may return an empty result; a disallowed field may cause a GraphQL error even when HTTP is `200`. The requirement is met when no data/relationship outside permissions is leaked and no unauthorized write occurs; not every rejection must be HTTP `403`.
