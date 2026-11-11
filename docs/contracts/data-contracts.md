# Data contracts, permissions, and manifests

This document defines the intended data and manifest contract shared by the
Operator, capabilities, UI, and application packages. The implementation is
partial; existing Go types and sample YAML are not evidence that every requirement
below is enforced. See [architecture and decision changes](../architecture/README.md#decisions-and-contract-changes)
and [security acceptance criteria](security-and-acceptance.md).

## 1. Identity and same-workspace links

- The authenticated context includes `user_id`, `workspace_id`, the set of allowed roles, and the selected role. The effective role must belong to the authenticated role set; role or workspace names from the body/header do not create permissions.
- Tables scoped to a workspace contain `workspace_id NOT NULL` referencing `core.workspaces(id)`. For referenced entity tables, `id` is the primary key; add `UNIQUE(workspace_id, id)` to support composite FKs. Link/receipt/batch tables may use a suitable composite business key.
- Every FK between workspace-bearing tables uses `(workspace_id, <foreign_id>) REFERENCES <table>(workspace_id, id)`. This applies to work items, employees, devices, files, and domain relationships. Optional FKs allow `<foreign_id>` to be null but never `workspace_id`.
- Hasura derives `workspace_id`/creator identity fields from the session through presets on insert, locks them on update, and filters select/update/delete by effective permissions. A service using higher DB privileges must enforce the same constraint.
- Nested inserts, upserts, and relationships must not open an extra path around permissions. If permissions for an operation have not been designed, do not grant that operation.

## 2. Shared entities

The field inventory below uses logical names. Entity IDs use UUID; audit IDs use BIGSERIAL. Apply the workspace/FK rules from section 1 to every table that has a workspace.

| Table | Fields and specific constraints |
| --- | --- |
| `core.workspaces` | `id`, unique `code`, `name` |
| `core.employees` | `id`, `workspace_id`, `full_name`, `email`, `department`, `created_at`; unique `(workspace_id, email)` |
| `core.work_items` | `id`, `workspace_id`, `title`, `description`, `priority`, `assigned_employee_id`, `created_at` |
| `core.file_objects` | `id`, `workspace_id`, `bucket`, `object_key`, `file_name`, `scan_status`, `sha256_hash`, `uploaded_by`, `scanned_at` |
| `core.audit_logs` | `id`, `workspace_id`, `actor_id`, `action`, `entity_type`, `entity_id`, `entity_version`, `payload`, `prev_hash`, `row_hash`, `created_at` |
| `core.outbox_events` | Durable event identity, workspace, event type, payload, and dispatch status; producer transaction and retry-safe delivery |
| `core.app_registry` | Published app configuration by `(workspace_id, app_id)`: display name, role/permissions, domain tables, UI schema, state machine, dependent capabilities, and deployment status |
| `core.app_definitions` | Active global application definition by `app_id`: display name, version, validated spec. Domain tables and Hasura metadata are shared by all workspaces, so the Operator plans them from this table; every workspace publishing an app runs this version ([ADR 0004](../adr/0004-kubernetes-crd-operator.md)) |
| `core.capability_registry` | Shared capability inventory: `cap_id`, `category_hpdi`, `base_route`, `enabled`; permission to use a capability is still evaluated per caller and app |

The registry/audit/export tables are written by the control plane or the assigned service. User roles may read only the parts they are granted. `core.work_items` keeps the shared work identity; business state lives in each domain table.

As established by [ADR 0002](../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md),
domain attributes such as `skill_level` and `work_shift` belong in application
tables. Employee relationships expose only the domain tables authorized for the
effective role; a shared employee does not grant cross-app traversal.

### Optional capability storage

[ADR 0003](../adr/0003-modular-capability-schema-separation-for-iot-data.md)
separates the base kernel from capability SQL. The `core.*` namespace does not
imply that every table is part of the base installation.

| Module | Tables and logical fields |
| --- | --- |
| `cap-process` | `core.command_receipts` for durable command results; `core.processed_workflow_steps` for effect deduplication |
| `cap-data` | `core.devices`: `id`, `workspace_id`, `device_code`, `name`, `location`, `status`, `last_reading` |
| `cap-data` | `core.device_work_items`: same-workspace device/work-item association; the base work item has no `source_device_id` |
| `cap-data` | `core.ot_events`: `id`, `workspace_id`, `device_id`, `event_dedup_key`, `metric_name`, `metric_value`, `created_at`; unique `(workspace_id, device_id, event_dedup_key)` |
| `cap-data` | `core.export_batches`: batch identity, workspace, Parquet location, row count, publication status, reconciliation information |
| `cap-data` | `core.data_lineage`: batch links to `(workspace_id, entity_type, entity_id, entity_version)` |

These modules live in `platform/postgres/capabilities/` and are loaded by enabled
capability configuration. Apps without a `cap-data` declaration receive no device
or telemetry permissions or relationships. Removing access or disabling a module
does not by itself authorize deleting its stored data.

## 3. Domain tables and initial state

Every domain table has `id`, `workspace_id`, `work_item_id`, `status`, `version`, and `updated_at`. The initial state comes from `stateMachine.initial`; `version` starts at `1`; the server sets the time.

| Illustrative app | Business table and fields | Initial state |
| --- | --- | --- |
| Maintenance | `domain.maintenance_records`: `assigned_employee_id`, `device_id`, `issue_summary`, `resolution_note` | `OPEN` |
| Procurement | `domain.procurement_requests`: `requester_id`, `vendor_name`, `item_description`, `quoted_amount NUMERIC(14,2) > 0`, `quote_file_id`, `ai_policy_summary`, `rejection_reason` | `DRAFT` |
| Timesheet | `domain.timesheet_entries`: `employee_id`, `work_date`, `logged_hours NUMERIC(4,2)` within `(0, 24]` | `SUBMITTED` |

This is declared data for the illustrative apps, not a fixed table list inside the platform. If a record is created in an already-submitted state, as with timesheet, the create path must enforce the corresponding business conditions inside the transaction; CRUD may be opened only when those conditions are fully enforced.

## 4. Write matrix and field ownership

| Operation | User role | Server responsibility |
| --- | --- | --- |
| Create record | Only business columns in the `insert` allowlist | Assign workspace/actor, initial state, version `1`, timestamps; check FKs and initialization conditions |
| Edit content | `update` allowlist, only within `editableStates` | Filter workspace/permissions, increment the version, record time and audit in the same transaction |
| Transition state | Send a command with `expected_version` | Check role, current state, permissions on the record, related files, and write atomically |
| Upsert/nested mutation | Denied by default; if granted, it must satisfy both the insert and update paths | Must not reset state/version, move workspace, or bypass FKs |
| Delete | Denied by default for ordinary roles | Settle the retention/audit policy before opening it; do not replace a `cancel` command with delete |

Remove `status`, `version`, `workspace_id`, and server-managed actor/timestamp fields from the columns a client may insert/update. Use server-side presets/defaults, not merely a declared SQL `DEFAULT`. For `core.file_objects`, clients do not write `scan_status`, the verification hash, or scan results; the scanner/file service holds that permission.

Read permissions are declared per table, role, and column. For example, `role_maintenance` reads the employee columns needed for maintenance and the relationship to maintenance; `role_timesheet` reads the timesheet area and the relationship to timesheet. Grant a relationship only when the target table is also readable. Manager roles must have explicit grants per app; the name `role_manager` does not imply system-wide permissions.

Hasura enforces permissions per table, operation, role, row, and column;
PostgreSQL `GRANT` does not replace its metadata. Domain tables keep independent
state and version even when they share an employee or work item.

## 5. Version, command, audit, and retry

**Chosen mechanism:** a `BEFORE UPDATE` trigger on the domain table sets `NEW.version = OLD.version + 1` and `updated_at` for every successful UPDATE. The client and the Command API do not increment the version themselves; the Operator generates the trigger together with the table. A retry that has been recognized does not perform another UPDATE.

A command compares `id`, workspace, permissions, state, and `expected_version` in the same transaction using a row lock or an equivalent conditional UPDATE. If the record is valid within the caller's scope but its version has changed, return `409`; do not write state, audit, or a business event for the failed attempt.

Example: the approver reads `version=1`; the requester edits `quoted_amount` through CRUD, making the record `version=2`; an approval command carrying `expected_version=1` must be rejected. When two commands with the same version run concurrently, at most one state transition succeeds.

Each command carries an idempotency key. The lock scope includes workspace, actor, app, entity, and command; the server stores the payload fingerprint and result in the same transaction. Retrying the same key/payload returns the recorded result after checking current permissions, before re-evaluating the old version; the same key with a different payload returns `409`. The receipt schema and its consumers must implement this full scope; storing the result only in memory is insufficient.

Write the change, audit, and durable event in the same transaction. Use Hasura Event Triggers or an equivalently durable outbox to deliver to Kestra; do not rely on an HTTP call to Kestra after commit as the sole delivery mechanism. The workflow deduplicates each effect by event identity and step; a retry after a crash does not create duplicate business effects.

Audit serializes per workspace before reading the chain head and computing the hash; `pg_advisory_xact_lock(hashtext(workspace_id::text))` may be used within the transaction. Checkpoints are signed and stored outside PostgreSQL in OpenBao. Integrity is proven up to the anchored checkpoint; do not claim that the unanchored tail resists history rewriting by a DB superuser.

## 6. Files, OT, and BI

New files start as `QUARANTINED`; only the scanning service moves them to `CLEAN` or `INFECTED`. Download, OCR, and commands that use a file must check permission/workspace and `CLEAN` status. Scanned content must not be overwritten under the same file identity; replacing a file creates a new identity and requires a rescan. A scanner error keeps the file unclean.

OT validates the device/workspace, stores a unique event per the key in section 2, and emits the standard event `ot.event.accepted`. The app's declared workflow evaluates thresholds and creates the appropriate work. The capability does not pre-select the maintenance app; duplicate delivery adds no further business effects.

The full BI source key is `(workspace_id, entity_type, entity_id, entity_version)`. Export only records in the app-declared `terminalStates`, and read only successfully published batches. Re-exporting the same source version adds no contribution; choose the latest version by `(workspace_id, entity_type, entity_id)`. Two entity types with the same UUID remain two distinct sources. Two exports with the same version key but different content must be detected as a reconciliation error rather than arbitrarily choosing one.

Lineage must preserve the full source record set behind each aggregate metric, not just one representative ID. Check the registry, batch, receipt, and lineage DDL against these requirements before connecting consumers; the presence of a table alone does not prove compliance.

## 7. `DXApplication` manifest contract

This is the intended logical shape. Before extending parsers or app packages,
agree on a versioned serialization, validator, and sample that consumers all use.
The existing parser and app examples are partial implementations; differences
from this table must be resolved explicitly rather than guessed independently.

| Field | Meaning and required data |
| --- | --- |
| `kind` | `DXApplication` |
| `metadata` | `name`, `displayName`, `version` of the app package |
| `spec.role` | The app's primary business role; other roles must be declared in the corresponding permissions |
| `spec.sharedAccess` | List of `core.*` tables, columns read, roles, and relationships used; no implicit write access to master data |
| `spec.domainTable` | Table name, columns/data types, nullability, CHECK constraints, composite FK within the workspace; the system columns in section 3 are managed by the Operator |
| `spec.domainAccess` | Allowlist of roles/columns for select, insert, and update; permission conditions on records. Deny undeclared operations by default |
| `spec.stateMachine` | `initial`, `editableStates`, `terminalStates` eligible for export, and commands with `from`, `to`, `allowedRoles`; states not in `editableStates` disallow content-editing CRUD |
| `spec.attachments` | File FK columns to check on download/OCR/command; do not infer them from domain column names |
| `spec.workflows` | Flows with an identity, a YAML file in the package, and subscribed events; `ot.event.accepted` or `command.committed` carries workspace/app/entity/version and an event ID as applicable |
| `spec.capabilities` | Capabilities required for the app/features to operate |
| `spec.uiSchema` | `listColumns`, `formFields`, views/actions referencing declared fields and commands; creates no additional permissions |

A workflow is registered only in a deployed workspace/app and runs under a least-privilege service identity. Event content does not grant permissions beyond the workspace. Table names, columns, relationships, and declared expressions must pass validator checks before DDL/metadata generation.

### Implementation gaps

The current Go types use `stateMachine.initialState` and `transitions`; the logical
contract above also needs editable/terminal states and command validation.
Attachment-column bindings, constraint/FK declarations, per-role access rules, and
workflow field names need one serialization shared by the parser, sample apps,
validators, services, and UI. Resolve compatibility and migrations for existing
data/manifests when changing that serialization. An example without matching
validation remains illustrative.

## 8. Semantic concept ownership

[ADR 0002](../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md#3-three-scenario-governance-for-domain-attributes-tier-3)
defines three cases: independent domain attributes, an explicitly shared concept
identified by a `semanticType` URI, and conflicting declarations rejected during
planning/reconcile. A shared concept has one authoritative Write Owner and
Multi-Reader replicas; owner updates produce transactional outbox events for
workflow fan-out. Matching column names alone never establishes shared meaning.

Incompatible types or multiple Write Owners for the same concept must fail before
configuration is published. The manifest serialization, ownership registry,
replica delivery contract, and enforcing validator still need to be specified and
implemented; the current parser does not define `semanticType` fields. This section
records the accepted requirement without claiming that synchronization works.

## 9. Capability interfaces

| Capability | Intended interface |
| --- | --- |
| `cap-process` | `POST /api/cap/process/files/upload`, `POST /api/cap/process/commands/{app_id}/{command}` |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export`, `GET /api/cap/data/lakehouse/analytics` |
| `cap-ai` | `POST /api/cap/ai/extract`, `POST /api/cap/ai/rag` |

Inspect actual handlers and agree on request/response schemas and service
identity before connecting callers. This logical route inventory includes work
still to implement; it is not an assertion that every endpoint is running.

### Current workflow deployment adapter

The process publisher supports a legacy full `KESTRA_URL` with Bearer
`KESTRA_API_TOKEN`, or deployment-owned single-destination routing via
`KESTRA_ROUTES_FILE`. The private JSON route array binds `workspace_id`, `app_id`,
package `version`, `workflow_id`, and a complete `endpoint`. Deployment derives
namespace/flow identity from the manifest-referenced workflow YAML and supplies
the private webhook key; event callers cannot supply a destination. Before each
send, the publisher requires a validated Ready app snapshot and exactly one
workflow subscribed to that event type. Missing, stale, or ambiguous routing
retains the event for retry. Multiple subscribed destinations require a separately
designed delivery contract; they are not silently reduced to one.

`KESTRA_AUTH_SCHEME` explicitly selects `Bearer` (legacy default) or `Basic`;
`KESTRA_API_TOKEN` is the credential following that scheme. Transport credentials
are separate from the signed effect service token. Routes/auth are loaded on
process startup; deployment changes require process recreation. The disposable
integration helper provides that lifecycle and renews private Kestra secrets.
