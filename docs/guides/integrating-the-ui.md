---
title: Integrating the UI
description: Connect a screen to the runtime API and respect role and version rules.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# Integrating the UI

What `workspace-ui` calls, in the order a session needs it. Everything goes
through the gateway (`http://localhost:9080` under Compose) with the user's
Keycloak access token. The backend enforces every permission; the UI only
decides what to show. Contracts: [runtime rules](../reference/runtime-rules.md),
[data contract](../reference/data-contracts/index.md),
[ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md) and
[ADR 0008](../adr/0008-an-application-declares-several-tables.md).

The Workspace UI copy is Vietnamese; identifiers, API field names and URLs stay
as published. In the portal, **Không gian của tôi** (My Workspace) shows your
authorized apps, a work preview, and recently opened pages for the current
session. Use **Refresh permissions**
to pick up new app grants or revocations. The **Acting role** control appears
when you can choose between roles in an app. Protected record links carry the
acting role, and the server checks which rows it can read.

## 1. Sign in

| Setting | Value under Compose |
| --- | --- |
| Issuer | `http://localhost:8085/realms/dxos` |
| Client | `dxos-portal` (public client) |
| Flow | Authorization code with PKCE (`S256`); password logins are disabled |
| Redirect URIs | `http://localhost:5173/*`, `http://localhost:15173/*`, `http://localhost:9080/*` |

The access token carries identity only. Send it as `Authorization: Bearer
<token>` on every call below.

The portal keeps the access, refresh and ID tokens in memory only. Before the
shorter of the two lifetimes ends it exchanges the refresh token for a new
access token while keeping the same sign-in identity, so open screens and typed
forms are not remounted. A page reload starts a silent sign-in with
`prompt=none` when the browser still has the non-secret `dxos.signedIn` marker
and the Keycloak session is active, then returns to the same page. If Keycloak
requires interaction, the welcome screen appears without an error. The session
ends when Keycloak's SSO idle or maximum lifetime ends, when the refresh is
refused, or on sign-out. Sign-out clears the tokens and redirects to
Keycloak's `end_session_endpoint` with `id_token_hint`, `client_id` and
`post_logout_redirect_uri` (the portal origin), so the next sign-in in the same
browser asks for the password again.

## 2. Who am I, and what can I open

| Call | Returns |
| --- | --- |
| `GET /api/auth/me` | `id`, `email`, `display_name`, and `roles`: every role the user holds, each with `name`, `description` and its owner (`app_id` or `cap_id`; neither for a platform role) |
| `GET /api/auth/apps` | `apps`: the installed applications the user holds a role in |

Each entry of `apps`:

| Field | Meaning |
| --- | --- |
| `app_id`, `display_name`, `version` | Identity of the application |
| `capabilities` | The capabilities it uses |
| `roles` | The roles the user holds in this application (`name`, `description`) |
| `work_item_roles` | Names of the user's roles in this app that its stored `sharedAccess` lists for `cap_work.work_items`. An empty array means no eligible role. Treat an omitted field from an older server as an empty array |
| `shared_access` | The entries of the app's `sharedAccess` that name a role the user holds: `table`, `roles` (only the user's roles) and `columns`. An empty array means none. Treat an omitted field from an older server as an empty array |
| `tables` | The application's tables exactly as its manifest declares them: `name`, `columns`, `foreignKeys`, `access`, `ui`, and when present `stateMachine`, `attachments` and `belongsTo` ([manifest contract](../reference/manifest/schema.md)) |

Call `/api/auth/me` first after sign-in: it also registers a new user in the
platform. Role changes take effect within a few seconds, so refresh both calls
when the user reopens the role switcher rather than caching them for the
session.

A user may hold several roles. The UI lets them choose the role they act as and
sends it as `X-Hasura-Role` on GraphQL and cap-process calls. A user with
exactly one role may omit the header. A role the user does not hold is refused;
the header never grants anything.

## 3. Build the screens from `tables`

| Kind of table | Tell it by | Screen |
| --- | --- | --- |
| Process table | has `stateMachine` | List, form, one button per command in `ui.actions`, and a `Lịch sử` section on the detail |
| Plain table | no `stateMachine`, no `belongsTo` | List, form, and the `Lịch sử` section on the detail |
| Child table | has `belongsTo` | A grid inside its parent record's detail; not a menu entry |

The record detail lists the record's audit history with the time, actor,
action, resulting state and acting role, from
`GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history` with the
acting role ([record history](../reference/api/capability-routes.md#record-history)).
It reloads when the record version changes; a role that cannot read the record
gets `404` and sees no history.

Name each table with `ui.title` when the manifest declares it, and fall back to
the table name. Name states and actions with `ui.stateLabels` and
`ui.actionLabels`; without a label, use a generic word for common workflow names
(such as `DRAFT` or `approve`) or a humanized name. The shell holds no
application-specific labels.

- `ui.listColumns` are the list's columns; `ui.formFields` are the form's inputs
  (`name`, `label`, `type` of `text`, `number`, `date`, `uuid` or `file`,
  `required`).
- Show a table when one of its `access.select` rules names the acting role.
  Show "new" when an `access.insert` rule does, "edit" when an `access.update`
  rule does, and "remove" on a child row when an `access.delete` rule does. The
  rule's `columns` are the fields that role may read or write.
- A process record is editable only while its `status` is one of
  `stateMachine.editableStates`. A child row is editable only while its parent
  is.
- Show a command when the acting role is in its `allowedRoles` and the record's
  `status` is in its `from`. The command's `recordFilter` may still refuse it
  (for example, nobody approves their own request): handle a 403.
- A foreign key to another table of the application (`foreignKeys`) is a
  reference picker over that table's list. The picker and the record detail
  name a referenced record by the first readable `text` column in the target's
  `ui.listColumns`, and show its id when the role can read none.
- `created_by` and foreign keys to shared `core.users`, `core.files` or
  `cap_data.devices` show `display_name`, `file_name` or `name` when
  `shared_access` lists the acting role for that table with both `id` and the
  label column; otherwise the id remains.
- Format values by column type: `timestamp` (including `created_at` and
  `updated_at`) as a `vi-VN` date and time in the viewer's time zone, `date` as
  a `vi-VN` date, and `integer` and `numeric(...)` with `vi-VN` digit grouping.
  Manifests do not mark a column as money, so no currency is added.

The portal opens the first readable top-level table declared by the app. List
views support paging, sorting, and an exact-match text filter when a readable
text column is available. Use **Open** to view a record. New, edit, and
workflow controls follow the permissions and state published for the table.
Knowledge-only apps open their published app knowledge or personal library.

## 4. Read and write records (GraphQL)

`POST /v1/graphql`. A table `domain.<name>` is exposed as `domain_<name>`.

| Operation | Field |
| --- | --- |
| List, filter, sort | `domain_<name>(where:, order_by:, limit:, offset:)` |
| One record | `domain_<name>_by_pk(id:)` |
| Create | `insert_domain_<name>_one(object:)` |
| Edit | `update_domain_<name>_by_pk(pk_columns: {id:}, _set:)` |
| Remove a child row | `delete_domain_<name>_by_pk(id:)` |

Relationships follow foreign keys:

- from a record to what it references, the column name without `_id`
  (`vendor_id` gives `vendor`, a child's `request_id` gives `request`);
- from the referenced record back, `<table>_by_<column>`
  (`procurement_items_by_request_id` on a request).

Rules to build on:

- Never send `id`, `created_by`, `org_unit_id`, `status`, `version`,
  `created_at` or `updated_at`; the server sets them. A child row is created
  with its parent column (`request_id`) and that column never changes.
- A role without a permission does not get the field at all, so GraphQL answers
  with a validation error such as "field not found" or "no mutations exist".
  A row outside the role's scope is simply absent from the result, and an update
  or delete that matches no permitted row returns `null`.
- HTTP status is 200 even for a refusal; check `errors` and the returned data.
- Every change to a record, and every change to one of its child rows,
  increments the record's `version`. Re-read the record before running a
  command on it.

## 5. Change a record's state (commands)

```
POST /api/cap/process/commands/{app_id}/{command}
X-Hasura-Role: <acting role>
Idempotency-Key: <a fresh UUID per user action>

{"entity_id": "<record id>", "expected_version": <version shown>, "payload": {}}
```

`payload` holds what the command declares under
`stateMachine.commands[].payload`: for each entry, the value of that column
(a string for `text`, `uuid` and `date`, a number for numeric columns). Ask the
user for those fields before running the command, and mark the ones with
`required: true`. Procurement's `reject` takes
`{"rejection_reason": "..."}`. A command without a declared payload takes `{}`.

Command names are unique within an application, so the table is not part of
the path. The response carries the record's new `status` and `version`.

| Status | Meaning for the UI |
| --- | --- |
| 200 | Done. Retrying with the same key and body returns the same answer |
| 400 | The payload does not match the command's declaration: a required field is missing or empty, a value has the wrong type, or a field is not declared |
| 401 | Token missing or expired: sign in again |
| 403 | The role may not run this command on this record |
| 404 | The record is not visible to this role (or does not exist) |
| 409 | The record changed since it was shown, or it is no longer in a state the command starts from: reload it |
| 422 | A required attachment is missing or not yet `CLEAN` |

The confirmation shows the record status and version being reviewed. If a
command returns 409, the portal reloads the record and requires a new
confirmation. When the outcome is unknown, an exact retry uses the original
body and idempotency key.

## 6. Files

1. `POST /api/cap/process/files/upload` as `multipart/form-data` with `app_id`
   and `file` (PDF, PNG or JPEG, at most 20 MiB). The answer is `file_id` with
   `scan_status: QUARANTINED`.
2. Store `file_id` in the record's attachment column through GraphQL. Only a
   file the same user uploaded to the same application can be attached. A
   refusal is a `permission-error` whose message starts with `Check constraint
   violation. attachment `; show a fixed explanation, not the server message.
3. Poll `core_files(where: {id: {_eq: $id}}) { scan_status }` until it is
   `CLEAN` or `INFECTED`; add `file_name` when `shared_access` exposes it to the
   acting role. Commands that require the attachment answer 422 until it is
   `CLEAN`.
4. `GET /api/cap/process/files/{file_id}` downloads a `CLEAN` file the role may
   read: its uploader, or a role that can read a record it is attached to. The
   response carries `Content-Disposition` with the stored file name (base name,
   control characters removed); the portal saves the download under that name.

## 7. Work items

When an application's workflow opens a work item (procurement opens "Review
procurement request" when a request is submitted), use `work_item_roles` from
`GET /api/auth/apps` to discover eligible roles for the inbox. Send an eligible
role as `X-Hasura-Role` on the GraphQL request. Hasura enforces access to work
items.

```graphql
query { cap_work_work_items(order_by: {created_at: desc}) {
  id title description priority board_column assignee_id source_type source_id created_at } }
```

`source_type` is the table and `source_id` the record the item is about, so an
inbox entry links to the record's detail. A role reads only the work items whose
source record it may read; items without a source stay visible to the whole
application. The item appears a few seconds after the command, once the workflow
has run.

**Needs your attention** previews up to five recent work items across eligible
app roles. **Việc của tôi** (My Work) loads more items, hides items whose
`board_column` is `DONE`, shows the role description from the app manifest, and
filters the loaded results by application and priority. Failed role queries show
partial or unavailable results. When you open a work item or recent link, the
portal refreshes permissions before reading the source record.

### Chờ tôi xử lý (the record inbox)

The inbox is derived from record state, not from work items. For every command
the acting role may run (the command is in `ui.inboxActions`, or in `ui.actions`
when the table declares no `ui.inboxActions`, and the role is in
`allowedRoles`), query the table as that role with
`where: {_and: [{status: {_in: <the command's from states>}}, <recordFilter>]}`,
ordered by `updated_at` desc and limited to 20 rows. Hasura row permissions apply
the role's scope and select filter, so a record leaves the inbox as soon as its
state changes. Commands that share a record filter share a query; a different
filter is never widened. `X-Hasura-User-Id` in a filter is replaced with the
signed-in user's id; any other `X-Hasura-*` placeholder skips that command's group
and makes the result partial. Merge groups, de-duplicate by application, table and
record id, and keep the command labels whose `from` contains the record status.
Each row links through the record detail URL with the acting role. A 401 clears
the session, a 403 drops that group, and other failures show a partial result
with a retry.

## 8. Administration

For users holding the platform role `role_operator` (it appears in
`/api/auth/me` with no owner). Everyone else gets 403. In the portal, these
users see **Người dùng & quyền** (`/people`): the accounts with search, and for
one account its roles grouped by owner, with grant and revoke. The 404 and 409
reasons are shown as the API returns them.

| Call | Does |
| --- | --- |
| `GET /api/auth/users` | `users`: every account with `id`, `email`, `display_name`, `status` and the `roles` it holds |
| `GET /api/auth/roles` | `roles`: every role that can be granted now, with `name`, `description` and its owner (`app_id` or `cap_id`) |
| `PUT /api/auth/users/{user_id}/roles/{role}` | Grants the role. 204, also when it was already held |
| `DELETE /api/auth/users/{user_id}/roles/{role}` | Revokes the role. 204, also when it was not held |

| Status | Meaning |
| --- | --- |
| 404 | Unknown user, or a role that does not exist or is not in force (its application is not installed) |
| 409 | An assignment nobody changes here: `role_workflow` belongs to the workflow service, and an operator cannot revoke their own `role_operator` |

A change shows in the user's `/api/auth/me` and GraphQL at once and in
cap-process within a few seconds, without a new sign-in. Every change is
recorded with the operator who made it. An account is listed once it has signed
in, or, when the installation sets `KEYCLOAK_DIRECTORY_SECRET`, as soon as it
exists in Keycloak: listing reads the identity provider (at most every 10 s, and
dxos-authz also reads it every `ACCOUNT_SYNC_INTERVAL`, default 5 minutes), so
an operator can grant roles before the person's first sign-in. An account
disabled or deleted in Keycloak shows `status: DISABLED` and is refused.
Accounts themselves are created in Keycloak or the directory it federates,
never here.

### Departments and staff (cap-hr)

Reads are for every signed-in user; changes need the capability role
`role_hr_admin` (403 otherwise). In the portal, users holding a cap-hr role see
**Tổ chức** (`/organization`): the department tree with staff counts, create
and edit a department, the staff of the selected department, and place, move
or remove a person. Picking a person lists every account only for a viewer who
also holds `role_operator`; otherwise it offers people already in a department.
From an account in **Người dùng & quyền**, **Xếp phòng ban** opens this screen
with that person selected (`/organization?user=<id>`).

| Call | Does |
| --- | --- |
| `GET /api/cap/hr/me` | The caller's `org_unit` (`id`, `code`, `name`, `parent_id`) and `position`, both `null` without a department, and `administrator` (whether they hold `role_hr_admin`) |
| `GET /api/cap/hr/org-units` | `org_units`: every department with `id`, `parent_id`, `code`, `name`, `staff_count`; build the tree from `parent_id` |
| `GET /api/cap/hr/staff` | `staff` of the caller's own department: `user_id`, `display_name`, `email`, `org_unit_id`, `position`, `joined_at`. An administrator gets every department, or the one named by `?org_unit_id=`; anyone else naming another department gets 403 |
| `POST /api/cap/hr/org-units` | Creates a department from `{"code", "name", "parent_id"}` (`parent_id` optional). 201 with its `id` |
| `PATCH /api/cap/hr/org-units/{id}` | Changes the fields sent: `code`, `name`, `parent_id` (`null` makes it a top-level department). 204 |
| `PUT /api/cap/hr/staff/{user_id}` | Places the user in `{"org_unit_id", "position"}` (`position` optional). A user has one department, so this moves them. 204 |
| `DELETE /api/cap/hr/staff/{user_id}` | Takes the user out of their department. 204 |

| Status | Meaning |
| --- | --- |
| 400 | A field is missing, blank, of the wrong type or not known |
| 404 | Unknown department or user (also: removing someone who has no department) |
| 409 | With a message to show: the code is taken, the parent does not exist, or the department would end up under itself or one of its sub-departments |

Moving a user changes what they see under the `department` scope from their
next request. Records keep the department they were created in. There is no
call to delete a department.

## 9. Try it

`scripts/seed-demo.py seed` creates six accounts (password `Demo@2026`) across
three departments. `scripts/seed-demo.py scopes`, `flow`, `lines`, `admin` and
`org` make the calls above from the command line and print what each person may see and do;
the script is a working reference for request shapes.

## 10. Assistant

The **Assistant** button opens conversations, transcripts, and citations using
the shared `/api/cap/ai/conversations` service. On a narrow screen the drawer
opens as a modal. Press Escape or choose **Close assistant** to return to the
trigger. The conversation and draft remain available in the current session
when you reopen it. The Assistant uses the question you enter and its available
knowledge. Record and work views remain available if the Assistant service
is interrupted.

## Not available yet

- Changing work items (assign, move between columns): `cap_work.work_items` has
  no service route yet. See [Work items](#7-work-items) for reads.
- Accounts that have never signed in are not listed for role assignment
  (account sync, #23).
- The portal cannot create accounts yet (#188).
- Custom dashboards, board and calendar views, and an Assistant model picker.
