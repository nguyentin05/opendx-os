# Data contracts, permissions, and manifests

Shared source of truth for Platform/Operator and Capabilities/UI/Apps. This is the design contract; migrations, JSON Schema/CRD, and the test suite will implement it during development. [Rule 02](../rules/02-data-contracts-and-permissions.md) holds the invariants, and [Rule 03](../rules/03-anti-patterns-and-security.md) holds the acceptance criteria.

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
| `core.employees` | `id`, `workspace_id`, `full_name`, `email`, `department`, `skill_level`, `work_shift` |
| `core.devices` | `id`, `workspace_id`, `device_code`, `name`, `location`, `status`, `last_reading` |
| `core.ot_events` | `id`, `workspace_id`, `device_id`, `event_dedup_key`, `metric_name`, `metric_value`, `created_at`; unique `(workspace_id, device_id, event_dedup_key)` |
| `core.work_items` | `id`, `workspace_id`, `title`, `description`, `priority`, `assigned_employee_id`, `source_device_id`, `created_at` |
| `core.file_objects` | `id`, `workspace_id`, `bucket`, `object_key`, `file_name`, `scan_status`, `sha256_hash`, `uploaded_by`, `scanned_at` |
| `core.audit_logs` | `id`, `workspace_id`, `actor_id`, `action`, `entity_type`, `entity_id`, `entity_version`, `payload`, `prev_hash`, `row_hash`, `created_at` |
| `core.export_batches` | Batch identity, workspace, Parquet location, row count, publication status, and reconciliation information |
| `core.data_lineage` | Links a batch to the source `(workspace_id, entity_type, entity_id, entity_version)` |
| `core.app_registry` | Published app configuration by `(workspace_id, app_id)`: display name, role/permissions, domain tables, UI schema, state machine, dependent capabilities, and deployment status |
| `core.capability_registry` | Shared capability inventory: `cap_id`, `category_hpdi`, `base_route`, `enabled`; permission to use a capability is still evaluated per caller and app |

The registry/audit/export tables are written by the control plane or the assigned service. User roles may read only the parts they are granted. `core.work_items` keeps the shared work identity; business state lives in each domain table.

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

## 5. Version, command, audit, and retry

**Chosen mechanism:** a `BEFORE UPDATE` trigger on the domain table sets `NEW.version = OLD.version + 1` and `updated_at` for every successful UPDATE. The client and the Command API do not increment the version themselves; the Operator generates the trigger together with the table. A retry that has been recognized does not perform another UPDATE.

A command compares `id`, workspace, permissions, state, and `expected_version` in the same transaction using a row lock or an equivalent conditional UPDATE. If the record is valid within the caller's scope but its version has changed, return `409`; do not write state, audit, or a business event for the failed attempt.

Example: the approver reads `version=1`; the requester edits `quoted_amount` through CRUD, making the record `version=2`; an approval command carrying `expected_version=1` must be rejected. When two commands with the same version run concurrently, at most one state transition succeeds.

Each command carries an idempotency key. The lock scope includes workspace, actor, app, entity, and command; the server stores the payload fingerprint and result in the same transaction. Retrying the same key/payload returns the recorded result after checking current permissions, before re-evaluating the old version; the same key with a different payload returns `409`. The result-storage schema is settled during implementation, but it must not be kept only in memory.

Write the change, audit, and durable event in the same transaction. Use Hasura Event Triggers or an equivalently durable outbox to deliver to Kestra; do not rely on an HTTP call to Kestra after commit as the sole delivery mechanism. The workflow deduplicates each effect by event identity and step; a retry after a crash does not create duplicate business effects.

Audit serializes per workspace before reading the chain head and computing the hash; `pg_advisory_xact_lock(hashtext(workspace_id::text))` may be used within the transaction. Checkpoints are signed and stored outside PostgreSQL in OpenBao. Integrity is proven up to the anchored checkpoint; do not claim that the unanchored tail resists history rewriting by a DB superuser.

## 6. Files, OT, and BI

New files start as `QUARANTINED`; only the scanning service moves them to `CLEAN` or `INFECTED`. Download, OCR, and commands that use a file must check permission/workspace and `CLEAN` status. Scanned content must not be overwritten under the same file identity; replacing a file creates a new identity and requires a rescan. A scanner error keeps the file unclean.

OT validates the device/workspace, stores a unique event per the key in section 2, and emits the standard event `ot.event.accepted`. The app's declared workflow evaluates thresholds and creates the appropriate work. The capability does not pre-select the maintenance app; duplicate delivery adds no further business effects.

The full BI source key is `(workspace_id, entity_type, entity_id, entity_version)`. Export only records in the app-declared `terminalStates`, and read only successfully published batches. Re-exporting the same source version adds no contribution; choose the latest version by `(workspace_id, entity_type, entity_id)`. Two entity types with the same UUID remain two distinct sources. Two exports with the same version key but different content must be detected as a reconciliation error rather than arbitrarily choosing one.

Lineage must preserve the full source record set behind each aggregate metric, not just one representative ID. The DDL-level representation of the registry, batch, receipt, and lineage must be settled before writing consumers that depend on them.

## 7. `DXApplication` manifest contract

This is the shared logical shape; before implementing parsers and independent apps, settle a versioned schema, a validator, and at least one valid sample manifest. Do not guess two different serializations on the two sides.

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
