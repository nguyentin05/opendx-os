# Security and acceptance criteria

These are the required security properties and acceptance scenarios for
authorization, commands, files, workflows, OT, BI, and audit. Mechanism details
live in the [data contracts](data-contracts.md). They describe the target system;
passing static checks alone does not establish runtime enforcement.

## ADR 0005 transition status

[ADR 0005](../adr/0005-department-and-application-data-model.md) defines one organization per installation. App/department/resource isolation replaces workspace tenancy. The scope changes below are target acceptance requirements, not passing runtime evidence. Existing workspace-based fixtures and consumers must be migrated and tested before they qualify. Unrelated OT/export/audit key serialization remains explicitly gated in the [data contract](data-contracts.md#legacy-consumer-transition-for-sections-59).

## Security invariants

- The backend authenticates identity and effective permissions; the UI or a role in the request does not grant permissions by itself. Use least privilege for users and service identities.
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

## Eight mandatory acceptance groups

| ID | Scenario | Evidence required when implemented |
| --- | --- | --- |
| S01 | App, department, owner & current-role isolation | Direct CRUD/relationships/nested/upsert/service paths obey own/department/organization role scope and resource ACL. Try cross-app, other-department and other-owner links; revoke DB membership while JWT remains valid; test a newly DB-granted role without token-role refresh, DB-disabled actor and unavailable authority. Ordinary roles cannot self-assign privileged scope/state/version. Original-file and Hasura paths must independently qualify |
| S02 | OT deduplication | Sending the same `(workspace_id, device_id, event_dedup_key)` multiple times creates only one event and one work effect per registered workflow |
| S03 | Workflow/command idempotency | Retrying the same key/payload after commit returns the previous result; a duplicate key with a different payload is rejected; a crash/retry of a step does not repeat business effects |
| S04 | Stale version & concurrency | Editing content through CRUD increments the version exactly once; approving with an old version returns `409`; two concurrent commands with the same version: only one succeeds; the failed attempt leaves no new audit/event |
| S05 | Independent state between apps | A state transition on one record does not overwrite another app's record state, even when they share a work item/employee |
| S06 | File quarantine | `QUARANTINED`/`INFECTED` files are blocked from download, OCR, and submit; no self-writing `CLEAN`; changing content requires a new file and a rescan |
| S07 | Export reconciliation | Re-running a batch does not increase totals; a failed batch that has not been published does not participate in BI; the same UUID with a different entity type/workspace is distinguished |
| S08 | Lineage | Every metric traces to the full, exact source `(workspace_id, entity_type, entity_id, entity_version)`; test both aggregation of multiple records and versions |

Choose checks by scope and record results using [verification](../development/verification.md).
Release requires evidence for all eight groups on the revision/artifact being released.

## Additional review cases

Check that role selection stays within authenticated grants, context changes
discard stale UI requests/cache, and disabled capabilities remain unavailable to
old clients. OCR checks the caller's attachment access; RAG checks source access
and citations. For audit, test concurrent appends and detection of edits to
checkpointed history. Describe each finding with its location, reproducible
scenario, impact, and fix direction.

Reference sources for Hasura behavior: [row permissions](https://hasura.io/docs/2.0/auth/authorization/permissions/row-level-permissions/), [GraphQL HTTP errors](https://hasura.io/docs/2.0/deployment/graphql-engine-flags/reference/#preserve-401-errors). When the deployed version changes, check the documentation for that version.
