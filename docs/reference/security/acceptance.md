---
title: Acceptance scenarios
description: The eight mandatory acceptance groups and additional review cases.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

## Eight mandatory acceptance groups

| ID | Scenario | Evidence required when implemented |
| --- | --- | --- |
| S01 | App, department, owner & current-role isolation | Direct CRUD/relationships/nested/upsert/service paths obey own/department/organization role scope and resource ACL. Try cross-app, other-department and other-owner links; revoke DB membership while JWT remains valid; test a newly DB-granted role without token-role refresh, DB-disabled actor and unavailable authority. Ordinary roles cannot self-assign privileged scope/state/version. Original-file and Hasura paths must independently qualify |
| S02 | OT deduplication | Sending the same `(app_id, device_id, event_dedup_key)` multiple times creates only one event and one work effect per registered workflow |
| S03 | Workflow/command idempotency | Retrying the same key/payload after commit returns the previous result; a duplicate key with a different payload is rejected; a crash/retry of a step does not repeat business effects |
| S04 | Stale version & concurrency | Editing content through CRUD increments the version exactly once; approving with an old version returns `409`; two concurrent commands with the same version: only one succeeds; the failed attempt leaves no new audit/event |
| S05 | Independent state between apps | A state transition on one record does not overwrite another app's record state, even when they share a work item/employee |
| S06 | File quarantine | `QUARANTINED`/`INFECTED` files are blocked from download, OCR, and submit; no self-writing `CLEAN`; changing content requires a new file and a rescan |
| S07 | Export reconciliation | Re-running a batch does not increase totals; a failed batch that has not been published does not participate in BI; the same UUID with a different entity type/application is distinguished |
| S08 | Lineage | Every metric traces to the full, exact source `(app_id, entity_type, entity_id, entity_version)`; test both aggregation of multiple records and versions |

Choose checks by scope and record results using [verification](../../contribute/verification.md).
A release records evidence for every group on the revision and artifact being
released. S02, S07 and S08 need the cap-data service, which is scheduled for
v1.1.0 (#111); until then a release states them as not implemented rather than
passed, and S06 has no OCR path to check.

## Additional review cases

Check that role selection stays within authenticated grants, context changes
discard stale UI requests/cache, and disabled capabilities remain unavailable to
old clients. OCR checks the caller's attachment access; RAG checks source access
and citations. For audit, test concurrent appends and detection of edits to
checkpointed history. Describe each finding with its location, reproducible
scenario, impact, and fix direction.

Reference sources for Hasura behavior: [row permissions](https://hasura.io/docs/2.0/auth/authorization/permissions/row-level-permissions/), [GraphQL HTTP errors](https://hasura.io/docs/2.0/deployment/graphql-engine-flags/reference/#preserve-401-errors). When the deployed version changes, check the documentation for that version.

### Native private source qualification

Source-foundation unit/SQL checks do not establish private-library release
readiness. Run `scripts/test-rag.sh source` against disposable PostgreSQL 17,
Hasura, File Service, private S3 storage and scanner. Required environment:
`TEST_DATABASE_URL`, `RAG_TEST_HASURA_URL`, `RAG_TEST_HASURA_ADMIN_SECRET`,
`RAG_TEST_FILE_API_URL`, `RAG_TEST_S3_ENDPOINT`, `RAG_TEST_S3_BUCKET`, and
`RAG_TEST_OIDC_TOKEN_A`, `RAG_TEST_OIDC_TOKEN_B`, `RAG_TEST_OIDC_TOKEN_ADMIN`.
The installation must contain a Ready knowledge-v1 personal library, with its
private-upload flag enabled only for qualification. Use disposable identities;
the runner seeds memberships and revokes/restores A's membership. Tokens and DSNs
stay in the environment and are never printed. Python websocket-client is needed
for the stale-session subscription probe. Missing prerequisites are NOT_RUN and
nonzero, never PASS. No knowledge routes or hosted models are enabled by this gate.

R01/R02/R04/R13 require same-byte uploads by A/B with distinct file IDs; other
owner/admin original and metadata denial; immutable private policy; CLEAN/hash
checks; infected-source denial; anonymous bucket-object denial; and revocation
with the same JWT and an already-open metadata subscription. A revoke before the
final file check blocks bytes, while bytes already emitted cannot be recalled.
Keep private upload disabled outside qualification until these live negatives
pass. The personal mode also requires parser/worker and browser qualification; see the
[personal library guide](../../guides/knowledge/personal-library.md). Package/model modes
remain reserved and fail NOT_RUN until implemented.

### Metadata and internal service qualification

Metadata routes default closed. `RAG_METADATA_ENABLED=true` and
`RAG_SOURCE_QUALIFIED=true` are deployment assertions for a disposable qualified
installation; do not set them before the source live gate passes. Public cap-ai
uses `DATABASE_URL` for dxos_ai_api and strict OIDC identity verification.
Optional DB passwords are `AI_DATABASE_PASSWORD`, `AI_WORKER_DATABASE_PASSWORD`
and `AI_DISPATCHER_DATABASE_PASSWORD`; unset passwords do not grant login.

Internal services require an explicit separate `RAG_INTERNAL_LISTEN` address,
distinct allowlisted UUID subjects `RAG_WORKER_SERVICE_SUBJECT` and
`RAG_FILE_SERVICE_SUBJECT`, existing issuer/audience/JWKS configuration, and
`RAG_SOURCE_QUALIFIED=true`. cap-ai also requires `AI_WORKER_DATABASE_URL` for
dxos_ai_worker; the internal File Service requires deployment-owned
`RAG_INTERNAL_AI_URL` and `RAG_INTERNAL_BEARER_TOKEN` authenticating the File
Service subject. User role claims cannot substitute for these service subjects.
A worker's verified subject determines its identity; a body cannot select another
worker. Keep internal listeners off public gateway routes. Validator HTTP clients
reject redirects and arbitrary per-request destinations. Service-token expiry,
authority outage or lease revocation denies source access.

`scripts/test-rag.sh metadata` requires the source-mode environment plus
`RAG_TEST_AI_API_URL`, runs the real source gate first, then concurrent REST/DB
registration, receipt conflict, A/B/admin metadata isolation and durable deletion.
Unit/SQL suites separately exercise rollback injection, leases, worker restart,
source delegation and revocation during fetch. Missing live prerequisites are
NOT_RUN/nonzero. DB-only tests or dormant package tables do not certify live
service identity routing, package grants, parser quality or model privacy.
