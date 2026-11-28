---
title: Domain tables
description: Process, plain, and child tables generated from an application manifest.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Domain tables and initial state

An application declares one or more domain tables `domain.<name>`
([ADR 0008](../../adr/0008-an-application-declares-several-tables.md)); the
operator generates each of them from the manifest. A table name belongs to one
application.

| Kind | Declares | System columns |
| --- | --- | --- |
| Process table | a `stateMachine` | `id`, `created_by`, `org_unit_id` (with cap-hr), `status` (checked against the state machine), `version`, `created_at`, `updated_at` |
| Plain table | neither `stateMachine` nor `belongsTo` | the same without `status` |
| Child table | `belongsTo: {table, column}` | `id`, `created_by`, `version`, `created_at`, `updated_at` and the parent column (NOT NULL, foreign key to the parent table) |

A process record starts in `stateMachine.initial`, is edited through Hasura
only in its editable states, and changes `status` only through cap-process
commands. A plain record (an entry of a list the application maintains, for
example) is read and written through Hasura under the table's access rules and
has no state. `version` starts at `1` on every table.

A child row is part of a record of its parent table (a line of a request, for
example), one level deep:

- a role reads it when one of its `select` rules on the parent admits the parent
  record;
- a role inserts, updates or deletes it when its `update` rule on the parent
  admits the parent record and, for a process parent, the parent is in an
  editable state;
- every change increments the parent's `version` in the same transaction, so a
  command sent with a version read before the change answers `409`;
- the parent column never changes, and a child row has no department of its own.

Hasura permissions and the triggers `core.enforce_parent` and
`core.touch_parent` both enforce this; the trigger locks the parent record, so a
child change and a command on the parent never interleave. A refused child change
raises SQLSTATE `23514` (`check_violation`) with the stable prefix
`the parent record can no longer be changed (status <status>)` or
`a row cannot move to another parent record`. Existing installations apply
`0005_parent_refusal.sql` with
`scripts/migrate-core.sh --database-url-env DATABASE_URL --through 0005`
(see [runtime rules](../runtime-rules.md)).

The process tables of the sample applications:

| Sample app | Business columns | Initial state |
| --- | --- | --- |
| Procurement | `vendor_name`, `item_description`, `quoted_amount NUMERIC(14,2) > 0`, `quote_file_id` → `core.files`, `vendor_id` → `domain.procurement_vendors`, `rejection_reason` | `DRAFT` |
| Maintenance | `assignee_id` → `core.users`, `device_id` → `cap_data.devices`, `issue_summary`, `resolution_note` | `OPEN` |
| Timesheet | `work_date`, `logged_hours NUMERIC(4,2)` within `(0, 24]` | `SUBMITTED` |

Procurement also declares a plain table, `domain.procurement_vendors` (a list
finance maintains, referenced by a request's `vendor_id`), and a child table,
`domain.procurement_items` (the lines of a request).

This is declared data for the sample apps, not a fixed table list inside the
platform. Tables of one application may reference each other by foreign key. If a record is created in an already-submitted state, as with
timesheet, the create path must enforce the corresponding business conditions
inside the transaction; CRUD may be opened only when those conditions are fully
enforced.
