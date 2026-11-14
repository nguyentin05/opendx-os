# Integrating the UI

What `workspace-ui` calls, in the order a session needs it. Everything goes
through the gateway (`http://localhost:9080` under Compose) with the user's
Keycloak access token. The backend enforces every permission; the UI only
decides what to show. Contracts: [backend contracts](../../contracts/README.md),
[data contract](../contracts/data-contracts.md),
[ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md) and
[ADR 0008](../adr/0008-an-application-declares-several-tables.md).

## 1. Sign in

| Setting | Value under Compose |
| --- | --- |
| Issuer | `http://localhost:8085/realms/dxos` |
| Client | `dxos-portal` (public client) |
| Flow | Authorization code with PKCE (`S256`); password logins are disabled |
| Redirect URIs | `http://localhost:5173/*`, `http://localhost:15173/*`, `http://localhost:9080/*` |

The access token carries identity only. Send it as `Authorization: Bearer
<token>` on every call below.

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
| `tables` | The application's tables exactly as its manifest declares them: `name`, `columns`, `foreignKeys`, `access`, `ui`, and when present `stateMachine`, `attachments` and `belongsTo` ([manifest contract](../contracts/data-contracts.md#7-dxapplication-manifest-contract)) |

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
| Process table | has `stateMachine` | List, form, and one button per command in `ui.actions` |
| Plain table | no `stateMachine`, no `belongsTo` | List and form |
| Child table | has `belongsTo` | A grid inside its parent record's detail; not a menu entry |

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
  reference picker over that table's list.

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

Command names are unique within an application, so the table is not part of
the path. The response carries the record's new `status` and `version`.

| Status | Meaning for the UI |
| --- | --- |
| 200 | Done. Retrying with the same key and body returns the same answer |
| 401 | Token missing or expired: sign in again |
| 403 | The role may not run this command on this record |
| 404 | The record is not visible to this role (or does not exist) |
| 409 | The record changed since it was shown, or it is no longer in a state the command starts from: reload it |
| 422 | A required attachment is missing or not yet `CLEAN` |

## 6. Files

1. `POST /api/cap/process/files/upload` as `multipart/form-data` with `app_id`
   and `file` (PDF, PNG or JPEG, at most 20 MiB). The answer is `file_id` with
   `scan_status: QUARANTINED`.
2. Store `file_id` in the record's attachment column through GraphQL. Only a
   file the same user uploaded to the same application can be attached.
3. Poll `core_files(where: {id: {_eq: $id}}) { scan_status }` until it is
   `CLEAN` or `INFECTED`. Commands that require the attachment answer 422 until
   it is `CLEAN`.
4. `GET /api/cap/process/files/{file_id}` downloads a `CLEAN` file the role may
   read: its uploader, or a role that can read a record it is attached to.

## 7. Try it

`scripts/seed-demo.py seed` creates six accounts (password `Demo@2026`) across
three departments. `scripts/seed-demo.py scopes`, `flow` and `lines` make the
calls above from the command line and print what each person may see and do;
the script is a working reference for request shapes.

## Not available yet

- Work items for approvers (inbox, Kanban): workflows are not deployed to Kestra
  yet, and `cap_work.work_items` has no service route.
- Screens to assign roles and departments: there is no API for it yet.
- `rejection_reason`: commands do not accept a payload yet.
- cap-data and cap-ai routes.
