# Data contracts, permissions, and manifests

This document defines the intended data and manifest contract shared by the
Operator, capabilities, UI, and application packages. The implementation is
partial; existing Go types and sample YAML are not evidence that every requirement
below is enforced. See [architecture and decision changes](../architecture/README.md#decisions-and-contract-changes)
and [security acceptance criteria](security-and-acceptance.md).

## 1. Identity, roles and scope

- One installation serves one organization. Keycloak authenticates; tokens
  carry identity only (`sub`, email, name). The role a caller acts as is named
  in `X-Hasura-Role` and accepted only when `core.user_roles` grants it now
  ([ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md)). Role
  names in a body, header or token never create permissions by themselves.
- Every role is owned by one application, one capability or the platform
  (`core.roles`). An application declares, per role and operation, a **scope**:
  `own` (records the caller created), `department` (records of the caller's
  department in `cap_hr.staff`) or `organization`, plus an optional
  `recordFilter`. Hasura permissions and cap-process commands apply the same
  scope.
- Data carries two independent labels: `app_id` (which application it belongs
  to) and, for applications using cap-hr, `org_unit_id` (which department). Both
  are stamped by the server and never change.
- `created_by` and `org_unit_id` are set by triggers from the session (Hasura's
  `hasura.user` or cap-process's `dxos.actor_id`), never from client input.
- Nested inserts, upserts and relationships must not open an extra path around
  permissions. If permissions for an operation have not been designed, do not
  grant that operation.

## 2. Shared entities

Tier 1 (`core`) never references Tier 2 or Tier 3 tables. Entity IDs are UUIDs;
audit IDs are BIGSERIAL.

| Table | Fields and constraints |
| --- | --- |
| `core.users` | `id` (= Keycloak `sub`), `email` (unique, case-insensitive), `display_name`, `status` (`ACTIVE`/`DISABLED`). A one-way mirror of Keycloak identity, written only by `dxos-authz` and the account sync; never deleted |
| `core.roles` | `name`, owner `app_id` or `cap_id` (at most one; neither for platform roles), `description` |
| `core.user_roles` | `(user_id, role_name)`, `granted_by`, `granted_at`: the only record of who holds which role |
| `core.apps` | `app_id`, `display_name`, `version`, validated `spec`, `status` (`Pending`/`Applying`/`Ready`/`Failed`/`Removed`), `last_error`. Rows are never deleted |
| `core.capabilities` | `cap_id`, `display_name`, `category_hpdi`, `base_route`, `enabled` |
| `core.app_capabilities` | `(app_id, cap_id)`; a capability cannot be disabled while an installed app uses it |
| `core.app_deployments` | one row per install or upgrade attempt |
| `core.files` | `id`, `app_id`, `bucket`, `object_key`, `file_name`, `scan_status`, `sha256_hash`, `uploaded_by`, `mime_type`, `size_bytes`, scan scheduling; identity and content columns are immutable |
| `core.audit_logs` | append-only, hash-chained per application (`chain` = `app_id`, or `core`): `actor_id`, `acting_role`, `action`, `entity_type`, `entity_id`, `entity_version`, `payload` |
| `core.outbox_events` | durable events with `app_id`, entity identity and version, payload and delivery state |

Registry, audit and outbox tables are written by the operator or the assigned
service; user roles read only what their application grants (`sharedAccess`).

As established by [ADR 0002](../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md),
domain attributes such as `skill_level` and `work_shift` belong in application
tables; a shared user does not grant cross-app traversal.

### Capability storage

[ADR 0003](../adr/0003-modular-capability-schema-separation-for-iot-data.md)
separates the kernel from capability SQL in `platform/postgres/capabilities/`,
loaded for the capabilities listed in `ENABLED_CAPABILITIES`. Each capability
owns its schema and registers itself in `core.capabilities`.

| Capability | Tables |
| --- | --- |
| `cap-hr` | `cap_hr.org_units` (departments, optional parent), `cap_hr.staff` (one department per user, `position`) |
| `cap-work` | `cap_work.work_items`: `app_id`, `title`, `description`, `priority`, `board_column`, `assignee_id`, source record (`source_type`, `source_id`), `created_by` |
| `cap-process` | `cap_process.command_receipts` (command idempotency), `cap_process.processed_workflow_steps` (effect deduplication) |
| `cap-data` | `cap_data.devices` and telemetry tables |

Apps that do not declare a capability get no permissions or relationships on its
tables. Removing access or disabling a capability does not by itself authorize
deleting its stored data.

## 3. Domain tables and initial state

[ADR 0008](../adr/0008-an-application-declares-several-tables.md) replaces the
single table described below with a list of tables (`spec.tables`: process,
plain and child tables). It is accepted and not yet implemented; this section
and section 7 describe the current single-table contract until it is.

Each application owns one domain table `domain.<name>`, generated by the
operator with the system columns `id`, `created_by`, `org_unit_id` (with cap-hr),
`status` (checked against the state machine), `version`, `created_at` and
`updated_at`, and the application's declared columns and foreign keys. The
initial state comes from `stateMachine.initial`; `version` starts at `1`.

| Sample app | Business columns | Initial state |
| --- | --- | --- |
| Procurement | `vendor_name`, `item_description`, `quoted_amount NUMERIC(14,2) > 0`, `quote_file_id` → `core.files`, `rejection_reason` | `DRAFT` |
| Maintenance | `device_id` → `cap_data.devices`, `issue_summary`, `resolution_note` | `OPEN` |
| Timesheet | `work_date`, `logged_hours NUMERIC(4,2)` within `(0, 24]` | `SUBMITTED` |

This is declared data for the sample apps, not a fixed table list inside the
platform. If a record is created in an already-submitted state, as with
timesheet, the create path must enforce the corresponding business conditions
inside the transaction; CRUD may be opened only when those conditions are fully
enforced.

## 4. Write matrix and field ownership

| Operation | User role | Server responsibility |
| --- | --- | --- |
| Create record | Only business columns in the `insert` allowlist | Stamp creator and department, initial state, version `1`, timestamps; check FKs and initialization conditions |
| Edit content | `update` allowlist, only within `editableStates` | Filter by scope and permissions, increment the version, record time and audit in the same transaction |
| Transition state | Send a command with `expected_version` | Check role, current state, permissions on the record, related files, and write atomically |
| Upsert/nested mutation | Denied by default; if granted, it must satisfy both the insert and update paths | Must not reset state/version, change creator or department, or bypass FKs |
| Delete | Denied by default for ordinary roles | Settle the retention/audit policy before opening it; do not replace a `cancel` command with delete |

Remove `status`, `version`, `created_by`, `org_unit_id` and timestamps from the columns a client may insert/update. Use server-side triggers and presets, not merely a declared SQL `DEFAULT`. For `core.files`, clients do not write `scan_status`, the verification hash, or scan results; the scanner/file service holds that permission.

Read permissions are declared per table, role, and column. For example, `role_maintenance` reads the employee columns needed for maintenance and the relationship to maintenance; `role_timesheet` reads the timesheet area and the relationship to timesheet. Grant a relationship only when the target table is also readable. Manager roles must have explicit grants per app; the name `role_manager` does not imply system-wide permissions.

Hasura enforces permissions per table, operation, role, row, and column;
PostgreSQL `GRANT` does not replace its metadata. Domain tables keep independent
state and version even when they share an employee or work item.

## 5. Version, command, audit, and retry

**Chosen mechanism:** a `BEFORE UPDATE` trigger on the domain table sets `NEW.version = OLD.version + 1` and `updated_at` for every successful UPDATE. The client and the Command API do not increment the version themselves; the Operator generates the trigger together with the table. A retry that has been recognized does not perform another UPDATE.

A command compares `id`, the acting role's scope and permissions, state, and `expected_version` in the same transaction using a row lock or an equivalent conditional UPDATE. If the record is valid within the caller's scope but its version has changed, return `409`; do not write state, audit, or a business event for the failed attempt.

Example: the approver reads `version=1`; the requester edits `quoted_amount` through CRUD, making the record `version=2`; an approval command carrying `expected_version=1` must be rejected. When two commands with the same version run concurrently, at most one state transition succeeds.

Each command carries an idempotency key. The lock scope includes actor, app, entity, command and key; the server stores the payload fingerprint and result in the same transaction. Retrying the same key/payload returns the recorded result after checking current permissions, before re-evaluating the old version; the same key with a different payload returns `409`. The receipt schema and its consumers must implement this full scope; storing the result only in memory is insufficient.

Write the change, audit, and durable event in the same transaction. Use Hasura Event Triggers or an equivalently durable outbox to deliver to Kestra; do not rely on an HTTP call to Kestra after commit as the sole delivery mechanism. The workflow deduplicates each effect by event identity and step; a retry after a crash does not create duplicate business effects.

Audit serializes per chain (one per application, plus `core`) before reading the chain head and computing the hash, with `pg_advisory_xact_lock(hashtext('dxos.audit:' || chain))` within the transaction. Checkpoints are signed and stored outside PostgreSQL in OpenBao. Integrity is proven up to the anchored checkpoint; do not claim that the unanchored tail resists history rewriting by a DB superuser.

## 6. Files, OT, and BI

New files start as `QUARANTINED`; only the scanning service moves them to `CLEAN` or `INFECTED`. Download, OCR, and commands that use a file must check the caller's permission (role and scope) and `CLEAN` status. Scanned content must not be overwritten under the same file identity; replacing a file creates a new identity and requires a rescan. A scanner error keeps the file unclean.

OT validates the device, stores a unique event per the key in section 2, and emits the standard event `ot.event.accepted`. The app's declared workflow evaluates thresholds and creates the appropriate work. The capability does not pre-select the maintenance app; duplicate delivery adds no further business effects.

The full BI source key is `(app_id, entity_type, entity_id, entity_version)`. Export only records in the app-declared `terminalStates`, and read only successfully published batches. Re-exporting the same source version adds no contribution; choose the latest version by `(app_id, entity_type, entity_id)`. Two entity types with the same UUID remain two distinct sources. Two exports with the same version key but different content must be detected as a reconciliation error rather than arbitrarily choosing one.

Lineage must preserve the full source record set behind each aggregate metric, not just one representative ID. Check the registry, batch, receipt, and lineage DDL against these requirements before connecting consumers; the presence of a table alone does not prove compliance.

## 7. `DXApplication` manifest contract

The operator's validator (`operator/internal/manifest`) is the reference
implementation of this serialization; `apps/*/manifest.yaml` are validated
samples and `contracts/fixtures/app-ready.json` is the matching `core.apps` row.
One installation serves one organization, so a manifest names no workspace.

| Field | Meaning and required data |
| --- | --- |
| `kind` | `DXApplication` (`apiVersion: dxos.io/v1alpha1`) |
| `metadata` | `name` (the `app_id`), `displayName`, `version` of the app package |
| `spec.capabilities` | Tier 2 capabilities the app uses. `cap-process` is required; `cap-hr` adds departments (`org_unit_id`, `department` scope); `cap-work` is required by `create_work_item` effects. Each must be enabled when the app is installed |
| `spec.roles` | Roles the app owns (`name`, optional `description`). Every role used anywhere in the manifest must be declared here. A role belongs to exactly one app; `role_operator`, `role_auditor` and `role_workflow` are reserved |
| `spec.sharedAccess` | Optional. Shared tables the app's roles read, from a closed catalog: `core.users`, `core.files`, `cap_hr.org_units`, `cap_hr.staff`, `cap_work.work_items`, `cap_data.devices`. Rows of `core.files` and `cap_work.work_items` are limited to the app's own `app_id`; a role sees only the files its user uploaded or that are attached to records it may read. No write access |
| `spec.domainTable` | `domain.<name>`, business columns (type, nullability, allowlisted CHECK) and optional `foreignKeys` (`column` → `references`, one of the referenceable catalog tables). System columns are managed by the operator: `id`, `created_by`, `status`, `version`, `created_at`, `updated_at`, plus `org_unit_id` for cap-hr apps |
| `spec.domainAccess` | Allowlist of roles and columns for `select`, `insert` and optional `update`. `select` and `update` rules declare a `scope`: `own` (`created_by` is the caller), `department` (the record's `org_unit_id` is the caller's department in `cap_hr.staff`; requires cap-hr) or `organization`. An optional `recordFilter` narrows the scope. Insert rules have no scope: the creator and department are stamped by database triggers. System columns are never writable |
| `spec.stateMachine` | `initial`, `editableStates`, `terminalStates`, and commands with `from`, `to`, `allowedRoles` and an optional `recordFilter` (for example `created_by _neq X-Hasura-User-Id` forbids self-approval). The domain table's `status` CHECK lists exactly these states |
| `spec.attachments` | Optional. File columns (foreign keys to `core.files`) that must be `CLEAN` for the listed commands |
| `spec.workflows` | Optional. Flows with an identity, a YAML file in the package, `command.committed` subscription and literal `create_work_item` effects (`title`, `description`, `priority`, optional `assigneeId`) |
| `spec.uiSchema` | `listColumns`, `formFields`, `actions` referencing declared columns and commands; creates no permissions |

Filters use `_and`, `_or` and declared columns compared with `_eq`/`_neq`; the
only session value is `X-Hasura-User-Id`.

### Installation and registry

Installing an application writes, in one transaction after its DDL and Hasura
metadata succeed, the `core.apps` row (`Ready`), its roles in `core.roles` and
its capabilities in `core.app_capabilities`. Each attempt is recorded in
`core.app_deployments`; a failed upgrade keeps the previous version `Ready` and
records `last_error`, a failed first install leaves the row `Failed`. A role
owned by another app, a capability or the platform, or a capability that is not
enabled, refuses the app before any change. Uninstalling marks the row `Removed`,
drops its Hasura permissions and releases its capabilities; the domain table,
data, roles and role assignments are kept. Roles dropped from a manifest are
deleted with their assignments.

### Consumers

cap-process reads `Ready` rows of `core.apps` whose capabilities are all
enabled ([runtime rules](../../contracts/README.md#runtime-rules-the-services-enforce)).
A command is accepted only on a record the acting role can read under its
`select` scope and record filter, and records the acting role in the audit row
and the event ([contracts](../../contracts/README.md#identity-roles-and-commands)).

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
`KESTRA_ROUTES_FILE`. The private JSON route array binds `app_id`, package
`version`, `workflow_id`, and a complete `endpoint`. Deployment derives
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
