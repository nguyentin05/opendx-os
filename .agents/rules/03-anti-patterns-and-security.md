# Rule 03 — Security and the acceptance matrix

Applies when designing, implementing, testing, or reviewing authorization, commands, files, workflows, OT, BI, and audit. Mechanism details live in the [data contracts](../memory/data-contracts.md).

## Security invariants

- The backend authenticates identity and effective permissions; the UI or a role in the request does not grant permissions by itself. Use least privilege for users and service identities.
- Separate data by workspace in both data rows and links; check both the CRUD path and the service path that uses higher DB privileges.
- State transitions, version checks, and audit must be atomic. Every record edit participates in the version mechanism; an insert must not bypass the initial state.
- Only clean files with the correct permissions may be used. Ordinary roles may not confirm `CLEAN` themselves; a scan error does not open access.
- Event delivery may repeat; every business effect must tolerate retries. Reconcile and export must also be safe to re-run.
- RAG is limited by workspace, app namespace, and the caller's permissions; results carry cited sources. OCR must not exceed permissions through a client-supplied `file_id`.
- Audit chains are locked per workspace and have checkpoints outside the DB. When changing audit, check concurrent writes and detect edits before the checkpoint.

## Protocol response contract

Command REST: missing/invalid authentication returns `401`; authenticated but insufficient permission returns `403`; a stale version or conflicting idempotency key returns `409`; using an unclean file returns `422`. Perform the permission check before revealing the state/version/file of a resource outside the caller's scope.

Hasura GraphQL: check `data`, `errors`, the number of affected rows, and DB state. A row filter may return an empty result; a disallowed field may cause a GraphQL error even when HTTP is `200`. The requirement is met when no data/relationship outside permissions is leaked and no unauthorized write occurs; not every rejection must be HTTP `403`.

## Eight mandatory acceptance groups

| ID | Scenario | Evidence required when implemented |
| --- | --- | --- |
| S01 | Workspace & role isolation | Select, relationship, insert/update, nested/upsert, and command do not exceed permissions; try FK employee/work item/file to another workspace; ordinary roles cannot insert an approved state or set the version themselves |
| S02 | OT deduplication | Sending the same `(workspace_id, device_id, event_dedup_key)` multiple times creates only one event and one work effect per registered workflow |
| S03 | Workflow/command idempotency | Retrying the same key/payload after commit returns the previous result; a duplicate key with a different payload is rejected; a crash/retry of a step does not repeat business effects |
| S04 | Stale version & concurrency | Editing content through CRUD increments the version exactly once; approving with an old version returns `409`; two concurrent commands with the same version: only one succeeds; the failed attempt leaves no new audit/event |
| S05 | Independent state between apps | A state transition on one record does not overwrite another app's record state, even when they share a work item/employee |
| S06 | File quarantine | `QUARANTINED`/`INFECTED` files are blocked from download, OCR, and submit; no self-writing `CLEAN`; changing content requires a new file and a rescan |
| S07 | Export reconciliation | Re-running a batch does not increase totals; a failed batch that has not been published does not participate in BI; the same UUID with a different entity type/workspace is distinguished |
| S08 | Lineage | Every metric traces to the full, exact source `(workspace_id, entity_type, entity_id, entity_version)`; test both aggregation of multiple records and versions |

In **design**, each group has a contract and a verification scenario; not having a test does not mean it passed or that an execution defect exists. In **implementation**, run the affected groups and the necessary integration checks. In **release**, all eight groups must have passing evidence on the revision/artifact being released. How to record results is described in [pre-pr-gate](../hooks/pre-pr-gate.md).

Reference sources for Hasura behavior: [row permissions](https://hasura.io/docs/2.0/auth/authorization/permissions/row-level-permissions/), [GraphQL HTTP errors](https://hasura.io/docs/2.0/deployment/graphql-engine-flags/reference/#preserve-401-errors). When the deployed version changes, check the documentation for that version.
