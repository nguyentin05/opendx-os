---
title: Manifest schema
description: Fields of the DXApplication manifest and its table declarations.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Manifest schema

The operator's validator (`operator/internal/manifest`) is the reference
implementation of this serialization. `contracts/manifest-v1.schema.json`
checks the shape only; the rules below that name other fields are enforced by
the validator. `apps/*/manifest.yaml` are validated samples, and
`contracts/fixtures/app-ready.json` is the matching `core.apps` row. One
installation serves one organization, so a manifest names no workspace.

Check a package with `dxos-operator validate <manifest>` and see the DDL and
Hasura operations it produces with `dxos-operator plan <manifest>`
([CLI](../cli/dxos-operator.md)).

## Top level

| Field | Meaning and required data |
| --- | --- |
| `apiVersion`, `kind` | `dxos.io/v1alpha1` and `DXApplication` |
| `metadata` | `name` (the `app_id`, `^[a-z][a-z0-9_]*$`), `displayName`, and `version` of the app package. All three are required |
| `spec.capabilities` | Non-empty list of unique Tier 2 capabilities: `cap-process`, `cap-hr`, `cap-work`, `cap-data`, `cap-ai`. `cap-process` is required because it runs the application's commands. `cap-hr` adds departments (`org_unit_id`, `department` scope); `cap-work` is required by the work-item effects; `cap-ai` by knowledge. Each must be enabled (`ENABLED_CAPABILITIES`) when the app is installed |
| `spec.roles` | At least one role the app owns: `name` (`role_` followed by lowercase letters, digits or `_`) and an optional `description` that the Workspace UI shows instead of the name. Every role used anywhere in the manifest must be declared here. A role belongs to exactly one app; `role_operator`, `role_auditor` and `role_workflow` are reserved |
| `spec.sharedAccess` | Optional. Read access to shared tables (see [below](#shared-access)). No write access |
| `spec.tables` | Domain tables (see [below](#tables)). At least one is required, except in a knowledge-only application that declares `spec.knowledge` and no workflows |
| `spec.workflows` | Optional. Flows the operator deploys to the workflow engine (see [below](#workflows)) |
| `spec.knowledge` | Optional; requires `cap-ai`. Knowledge sources packaged with the app (`version: 1`, `sources`, `grantPolicies`) and the personal library switch (`personalLibrary: {enabled, allowedRoles}`). See [package knowledge](../../guides/knowledge/package-knowledge.md) and [personal library](../../guides/knowledge/personal-library.md) |

## Shared access

Each entry of `spec.sharedAccess` names a `table`, the `roles` that read it and
the `columns` they read. The table comes from a closed catalog; a table that
belongs to a capability needs that capability in `spec.capabilities`.

| Table | Readable columns | Requires |
| --- | --- | --- |
| `core.users` | `id`, `email`, `display_name`, `status` | |
| `core.files` | `id`, `file_name`, `mime_type`, `size_bytes`, `scan_status`, `sha256_hash`, `uploaded_by`, `scanned_at`, `created_at` | |
| `cap_hr.org_units` | `id`, `parent_id`, `code`, `name` | `cap-hr` |
| `cap_hr.staff` | `user_id`, `org_unit_id`, `position`, `joined_at` | `cap-hr` |
| `cap_work.work_items` | `id`, `title`, `description`, `priority`, `board_column`, `assignee_id`, `source_type`, `source_id`, `created_by`, `created_at`, `updated_at` | `cap-work` |
| `cap_data.devices` | `id`, `device_code`, `name`, `location`, `status`, `last_reading` | `cap-data` |

Rows of `core.files` and `cap_work.work_items` are limited to the app's own
`app_id`. A role sees only the files its user uploaded or that are attached to
records it may read, and only the work items whose source record, a row of a
top-level table, it may read; items without a source stay app-wide.

## Tables

Each entry of `spec.tables`:

| Field | Meaning and required data |
| --- | --- |
| `name` | `domain.<name>`, unique in the application. A table belongs to one application |
| `columns` | At least one business column: `name`, `type` (`uuid`, `text`, `date`, `numeric(14,2)` or `numeric(4,2)`), `nullable`, and an optional `check` from an allowlist: `length(trim(<column>)) > 0` on `text`, `<column> > 0` or `<column> > 0 AND <column> <= 24` on `numeric`. The operator manages the system columns: `id`, `created_by`, `version`, `created_at`, `updated_at`, plus `org_unit_id` for cap-hr apps on a top-level table, `status` on a process table, and the parent column on a child table. `status` and `org_unit_id` are reserved names on every table |
| `foreignKeys` | Optional. `column` (a business `uuid` column, referenced once) → `references`: `core.users`, `core.files`, `cap_hr.org_units`, `cap_work.work_items`, `cap_data.devices` (with the capability that owns it), or another table of the same application. A table of another application cannot be referenced. A foreign key to `core.files` must be declared in `attachments` |
| `access` | Role and column allowlists for `select` and `insert` (both required) and optional `update`; `delete` on a child table only. See [access rules](#access-rules) |
| `belongsTo` | Optional; makes the table a child table (see [child tables](#child-tables)) |
| `stateMachine` | Optional; makes the table a process table (see [state machine](#state-machine)) |
| `attachments` | Optional, process tables only. Each `column` is a foreign key to `core.files`; `requireCleanOnCommands` lists commands of the table that need the file's scan to be `CLEAN` |
| `ui` | Rendering hints; they create no permissions (see [UI](#ui)) |

### Access rules

Each rule names a `role`, the `columns` it reads or writes, and optionally a
`recordFilter`.

- `select` and `update` rules declare a `scope`:
  - `own`: `created_by` is the caller;
  - `department`: the record's `org_unit_id` is the caller's department in
    `cap_hr.staff`; requires `cap-hr`;
  - `organization`: every record.
- An optional `recordFilter` narrows the scope.
- `insert` rules have no scope: database triggers stamp the creator and the
  department.
- An `insert` rule may declare `presets`, which set a business `uuid` column to
  `X-Hasura-User-Id`. A preset column is not also in the rule's `columns`.
- A role has at most one rule per operation, and system columns are never
  writable.

A filter (`recordFilter` on a rule or a command) uses `_and`, `_or` and declared
columns compared with `_eq` or `_neq`, nested at most 32 levels. Values are
literals of the column's type or, for a `uuid` column, the session value
`X-Hasura-User-Id`; no other session value is accepted.

### Child tables

`belongsTo` names a `table` of the same application that is not itself a child,
and a `column`, the parent column the operator adds.

- A child table has no `stateMachine` or `attachments`; its rows follow the
  parent record.
- Its `select`, `insert` and `update` rules name a role and columns only: no
  `scope`, `recordFilter` or `presets`. It may add `delete` rules that name a
  role.
- A role in a `select` rule needs a `select` rule on the parent. A role in an
  `insert`, `update` or `delete` rule needs an `update` rule on the parent.
- The parent column is insertable and never updatable.

### State machine

`initial`, `editableStates`, `terminalStates` and `commands`. Every state in
`editableStates` and `terminalStates` must be reached by the state machine, and
no state is both editable and terminal. The table's `status` CHECK lists exactly
the states.

Each command declares:

- `name`, unique within the application;
- `from` and `to`; a terminal state has no outgoing command;
- `allowedRoles`;
- an optional `recordFilter`. For example, `created_by _neq X-Hasura-User-Id`
  forbids self-approval;
- an optional `payload`: the business columns the caller sets with the command
  (`column`, `required`). These may not be system columns, attachments or
  foreign keys ([ADR 0006](../../adr/0006-where-application-business-logic-lives.md)).

### UI

| Field | Meaning |
| --- | --- |
| `listColumns` | Required. Declared columns shown in the list |
| `formFields` | Required. Inputs: `name` (a business column), `label`, `type` (`text`, `number`, `date`, `uuid` or `file`; `file` only for an attachment column) and `required` |
| `actions` | The table's commands; a process table lists them, other tables list none |
| `title` | Optional name of the table in the portal |
| `inboxActions` | Optional subset of `actions` that puts a record in the user's inbox; every action counts when absent |
| `stateLabels`, `actionLabels` | Optional maps naming the states and the listed actions |

## Workflows

Each entry of `spec.workflows`:

- `id`: an identifier, unique in the application.
- `file`: `workflows/<name>.yaml`, a Kestra flow in the package that the
  operator deploys ([workflow deployment](../api/events.md#workflow-deployment)).
- `subscribedEvents`: exactly `[command.committed]`.
- `effects`: at least one effect, each with:
  - `stepName`, unique across the application's flows;
  - `kind`;
  - `whenCommands`, which are commands of the application.

Both effect kinds need `cap-work`:

- `create_work_item` requires `fields`: `title`, `description`, `priority`
  (`LOW`, `MEDIUM`, `HIGH` or `CRITICAL`) and an optional `assigneeId` (a
  UUID). It opens a work item whose source is the command's record.
- `complete_work_item` declares no `fields` and marks the record's open work
  items `DONE`.

Event delivery needs exactly one subscriber. The validator accepts several
flows, but cap-process does not deliver the events of an application in which
more than one flow subscribes to `command.committed`.
