---
title: Writes, versions, and audit
description: Field ownership, optimistic concurrency, command receipts, and audit.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## 4. Write matrix and field ownership

| Operation | User role | Server responsibility |
| --- | --- | --- |
| Create record | Only business columns in the `insert` allowlist | Stamp creator and department, initial state, version `1`, timestamps; check FKs and initialization conditions |
| Edit content | `update` allowlist, only within `editableStates` | Filter by scope and permissions, increment the version, record time and audit in the same transaction |
| Transition state | Send a command with `expected_version` and the payload the command declares | Check role, current state, permissions on the record, related files and the payload, and write the state and the payload's columns atomically |
| Upsert/nested mutation | Denied by default; if granted, it must satisfy both the insert and update paths | Must not reset state/version, change creator or department, or bypass FKs |
| Delete | Only rows of a child table, by a role that may edit the parent record | Refuse it once the parent leaves its editable states; increment the parent's version; audit the deleted row. Process and plain records are never deleted: use a `cancel` command |

Remove `status`, `version`, `created_by`, `org_unit_id` and timestamps from the columns a client may insert/update. Use server-side triggers and presets, not merely a declared SQL `DEFAULT`. For `core.files`, clients do not write `scan_status`, the verification hash, or scan results; the scanner/file service holds that permission.

Read permissions are declared per table, role, and column. For example, `role_maintenance` reads the employee columns needed for maintenance and the relationship to maintenance; `role_timesheet` reads the timesheet area and the relationship to timesheet. Grant a relationship only when the target table is also readable. Manager roles must have explicit grants per app; the name `role_manager` does not imply system-wide permissions.

Hasura enforces permissions per table, operation, role, row, and column;
PostgreSQL `GRANT` does not replace its metadata. Domain tables keep independent
state and version even when they share an employee or work item.

## 5. Version, command, audit, and retry

**Chosen mechanism:** a `BEFORE UPDATE` trigger on the domain table sets `NEW.version = OLD.version + 1` and `updated_at` for every successful UPDATE. The client and the Command API do not increment the version themselves; the Operator generates the trigger together with the table. A retry that has been recognized does not perform another UPDATE.

A command compares `id`, the acting role's scope and permissions, state, and `expected_version` in the same transaction using a row lock or an equivalent conditional UPDATE. If the record is valid within the caller's scope but its version has changed, return `409`; do not write state, audit, or a business event for the failed attempt.

Example: the approver reads `version=1`; the requester edits `quoted_amount` through CRUD, making the record `version=2`; an approval command carrying `expected_version=1` must be rejected. When two commands with the same version run concurrently, at most one state transition succeeds.

A command that declares a `payload` writes those columns in the statement that
changes the status, so the new state and its data share one version, one audit
row and one event. The request's payload may hold only the declared columns; a
required one must be present and not empty, and each value must fit its
column's type (a refused payload is a `400` and changes nothing). A column the
payload omits keeps its value. A command without a declared payload takes an
empty one.

Each command carries an idempotency key. The lock scope includes actor, app, entity, command and key; the server stores the payload fingerprint and result in the same transaction. Retrying the same key/payload returns the recorded result after checking current permissions, before re-evaluating the old version; the same key with a different payload returns `409`. The receipt schema and its consumers must implement this full scope; storing the result only in memory is insufficient.

Write the change, audit, and durable event in the same transaction. Use Hasura Event Triggers or an equivalently durable outbox to deliver to Kestra; do not rely on an HTTP call to Kestra after commit as the sole delivery mechanism. The workflow deduplicates each effect by event identity and step; a retry after a crash does not create duplicate business effects.

Audit serializes per chain (one per application, plus `core`) before reading the chain head and computing the hash, with `pg_advisory_xact_lock(hashtext('dxos.audit:' || chain))` within the transaction. Checkpoints are signed and stored outside PostgreSQL in OpenBao. Integrity is proven up to the anchored checkpoint; do not claim that the unanchored tail resists history rewriting by a DB superuser.
