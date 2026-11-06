# Proposed backend SQL requirements for B

Status: **PROPOSED; B acceptance and implementation pending**. B owns all platform
SQL and Operator migrations; this document changes no DDL. Inventory reviewed:
`platform/postgres/init/01_core_schema.sql`, `03_app_registry.sql`, and
`platform/postgres/capabilities/cap_process.sql`. Consumers are A1 registry,
A2 command transactions, A3 delivery/effect transactions and A4 files.

| Surface | Existing inventory | Proposed requirement / ownership |
| --- | --- | --- |
| Registry | workspace_id/app_id PK; display_name/version/role_name/domain_table/manifest_spec/ui_schema/state_machine/dependent_capabilities/status/last_error/updated_at | B publishes one coherent Ready active snapshot atomically. A1 reads only Ready. Deployment-attempt state must be separate, never overwrite an old active row with Pending/Applying/Failed. |
| Receipts | Full workspace/actor/app/entity/command/key PK, payload_hash, response_status/body, created_at | Retain these columns; serialize scope before lookup/update. A2 rechecks current permission before returning response. Hash full canonical request (README), not payload alone. No receipt for rollback/failure. |
| Outbox | id, workspace/app/type/entity/version/payload/created_at; unique workspace/id and source version/type | B adds durable delivery fields below; A3 implements claim/retry/ack. |
| Processed steps | event_id FK, step_name, outcome, processed_at; PK event/step | Add workspace_id, work_item_id, result JSONB; composite FKs below. A3 owns atomic effect/result writes. |
| Audit/version | Core bump function; audit hash/immutability triggers | B generates domain BEFORE UPDATE version/time trigger plus domain audit insertion trigger. A2 sets validated transaction-local context before UPDATE; never manually insert duplicate audit. |
| Files | immutable identity metadata, hash, uploader, scan state/time, unique bucket/key | B/A agree app-scoped authorization storage below; A4 owns immutable upload/scan transitions. |

## Durable delivery columns

Propose adding to `core.outbox_events`: `delivery_status TEXT NOT NULL DEFAULT
'pending' CHECK IN ('pending','in_flight','delivered')`, `attempt_count INTEGER
NOT NULL DEFAULT 0 CHECK >= 0`, `next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
`lease_token UUID NULL`, `lease_until TIMESTAMPTZ NULL`, `delivered_at TIMESTAMPTZ
NULL`, `last_error TEXT NULL`. Enforce paired lease token/time for in_flight;
delivered requires delivered_at and no active lease. Index pending due time and
expired leases. Preserve identity, payload and all original event source columns.

A3 claims due/expired rows with locking (e.g. FOR UPDATE SKIP LOCKED) in a short
transaction, increments attempts and persists a new token/expiry before network
I/O. Acknowledge/reschedule only with matching lease token; stale workers cannot
ack a newer claim. Failed/lost acknowledgement schedules bounded backoff without
changing event ID; a crash reclaims after lease expiry. No network call holds a
command transaction open. Event retention must preserve processed-step FKs.
The one-destination core contract is insufficient for independent subscribers;
B/A must agree a per-destination delivery table before adding fan-out.

## Work-item effect result and source association

`core.processed_workflow_steps` gains NOT NULL workspace_id, work_item_id UUID and
result JSONB for successful create_work_item results (stored
`{"work_item_id":"UUID"}`). Retain event_id/step_name uniqueness, processed_at and
outcome; only a successful atomic effect is stored. Require
`(workspace_id,event_id) REFERENCES core.outbox_events(workspace_id,id)` and
`(workspace_id,work_item_id) REFERENCES core.work_items(workspace_id,id)`; both
columns NOT NULL. Source app/entity/version is recovered through the persisted
event, avoiding a polymorphic unvalidated client association. Lock/serialize the
event/step scope; create work item and processed-step result in one transaction.
A rollback leaves neither; duplicate invocation returns the stored result after
service/workspace authorization. No partial `processing` result counts as success.

## Audit and file access gaps

Audit insertion must derive actor/action from authenticated Hasura `hasura.user`
or validated transaction-local `dxos.actor_id` / `dxos.action`. Reject missing or
malformed context for domain writes; ordinary DB callers cannot set arbitrary
trusted context. B must define restricted connection/function privileges. Version
trigger increments once; audit uses resulting version. Recognized retries and
failed commands perform no UPDATE and produce no new audit/event.
The existing audit hash trigger hashes inserted rows but does not create a domain
update audit record. External anchoring remains deferred; no new security claim.

Existing file_objects has no app_id. Propose NOT NULL app_id for new uploads and
a same-workspace/app reference to active app identity (retention-safe registry key
policy needs B review). Authorization must cover both newly uploaded owner files
and readable declared attachments, before scan-state disclosure. Preserve uploader
same-workspace FK and immutable bucket/key/hash/content; only trusted scanner
writes scan status. B must decide migration/backfill for existing unscoped files;
A must not infer cross-app read permission from ownership alone. MIME/size may be
stored for consistency but are validated against bytes by A4, not client metadata.

## Review and migration boundary

Do not mark these names frozen until B accepts column/check/index names, migration
strategy, deployment-attempt storage (proposed separate core.app_deployment_attempts
with attempt UUID, workspace/app, requested version, status, last_error and times),
file app scope and runtime DB privileges. Active publication occurs after all
DDL/Hasura/workflow validation succeeds; failed attempt preserves the prior active
snapshot. Real PostgreSQL concurrency, Hasura permission and crash/restart tests
remain NOT_RUN until B's service/schema handoff. Fixture agreement is static only.
