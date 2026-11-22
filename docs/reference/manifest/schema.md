---
title: Manifest schema
description: Fields of the DXApplication manifest and its table declarations.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## 7. `DXApplication` manifest contract

The operator's validator (`operator/internal/manifest`) is the reference
implementation of this serialization; `apps/*/manifest.yaml` are validated
samples and `contracts/fixtures/app-ready.json` is the matching `core.apps` row.
One installation serves one organization, so a manifest names no workspace.

| Field | Meaning and required data |
| --- | --- |
| `kind` | `DXApplication` (`apiVersion: dxos.io/v1alpha1`) |
| `metadata` | `name` (the `app_id`), `displayName`, `version` of the app package |
| `spec.capabilities` | Tier 2 capabilities the app uses. `cap-process` is required; `cap-hr` adds departments (`org_unit_id`, `department` scope); `cap-work` is required by the work-item effects (`create_work_item` and `complete_work_item`). Each must be enabled when the app is installed |
| `spec.roles` | Roles the app owns (`name`, optional `description`). Every role used anywhere in the manifest must be declared here. A role belongs to exactly one app; `role_operator`, `role_auditor` and `role_workflow` are reserved |
| `spec.sharedAccess` | Optional. Shared tables the app's roles read, from a closed catalog: `core.users`, `core.files`, `cap_hr.org_units`, `cap_hr.staff`, `cap_work.work_items`, `cap_data.devices`. Rows of `core.files` and `cap_work.work_items` are limited to the app's own `app_id`; a role sees only the files its user uploaded or that are attached to records it may read. No write access |
| `spec.tables` | Domain tables, or an empty list for a validated capability-only knowledge application (see Knowledge v1); each entry has the fields below. Table names are unique, and a table belongs to one application |
| `spec.workflows` | Optional. Flows with an identity, a YAML file in the package, `command.committed` subscription and business effects on commands of any table. `create_work_item` requires `fields` (`title`, `description`, `priority`, optional `assigneeId`) and opens a work item. `complete_work_item` declares no `fields` and marks the record's open work items `DONE`. At most one flow subscribes to an event type. The file is a Kestra flow the operator deploys ([workflow deployment](../api/events.md#workflow-deployment)) |

Each entry of `spec.tables`:

| Field | Meaning and required data |
| --- | --- |
| `name` | `domain.<name>` |
| `columns` | Business columns (type, nullability, allowlisted CHECK). System columns are managed by the operator: `id`, `created_by`, `version`, `created_at`, `updated_at`, plus `org_unit_id` for cap-hr apps and `status` on a process table. `status` and `org_unit_id` are reserved names on every table |
| `foreignKeys` | Optional. `column` → `references`: a referenceable table of the shared catalog, or another table of the same application. A table of another application cannot be referenced. A foreign key to `core.files` must be declared in `attachments` |
| `access` | Allowlist of roles and columns for `select`, `insert` and optional `update` (and `delete` on a child table). `select` and `update` rules declare a `scope`: `own` (`created_by` is the caller), `department` (the record's `org_unit_id` is the caller's department in `cap_hr.staff`; requires cap-hr) or `organization`. An optional `recordFilter` narrows the scope. Insert rules have no scope: the creator and department are stamped by database triggers. System columns are never writable |
| `belongsTo` | Optional; makes the table a child table. `table` is another table of the application that is not itself a child; `column` is the parent column the operator adds. A child table has no `stateMachine` or `attachments`; its `select`, `insert` and `update` rules name a role and columns only (no `scope`, `recordFilter` or `presets`), and may add `delete` rules naming a role. A role in a `select` rule needs a `select` rule on the parent; a role in an `insert`, `update` or `delete` rule needs an `update` rule on the parent. The parent column is insertable and never updatable |
| `stateMachine` | Optional; makes the table a process table. `initial`, `editableStates`, `terminalStates`, and commands with `from`, `to`, `allowedRoles`, an optional `recordFilter` (for example `created_by _neq X-Hasura-User-Id` forbids self-approval) and an optional `payload`: the business columns the caller sets with the command (`column`, `required`), which may not be system columns, attachments or foreign keys ([ADR 0006](../../adr/0006-where-application-business-logic-lives.md)). The table's `status` CHECK lists exactly these states. Command names are unique within the application |
| `attachments` | Optional, process tables only. File columns (foreign keys to `core.files`) that must be `CLEAN` for the listed commands of the table |
| `ui` | `listColumns` and `formFields` referencing declared columns, `actions` listing the table's commands (process tables only), an optional `title` that names the table in the portal, optional `inboxActions`, a subset of `actions` that puts a record in the user's inbox (every action counts when absent), and optional `stateLabels` and `actionLabels` maps that name the state machine's states and the listed actions; creates no permissions |

Filters use `_and`, `_or` and declared columns compared with `_eq`/`_neq`; the
only session value is `X-Hasura-User-Id`.
