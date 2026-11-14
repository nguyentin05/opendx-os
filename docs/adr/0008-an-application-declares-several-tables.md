# ADR 0008: An Application Declares Several Tables

- **Status:** Accepted
- **Date:** 2026-11-14
- **Supersedes:** the "one isolated table per application" rule of
  [ADR 0001](0001-three-tier-composable-architecture.md), decision 2. The rest of
  ADR 0001 stands.

## Context

ADR 0001 gave each Tier 3 application one isolated domain table. The manifest
followed it: `spec.domainTable` is a single table, with one `domainAccess`, one
`stateMachine` and one `uiSchema`.

One table cannot describe ordinary business documents:

- a purchase request has several line items, a timesheet has several days, an
  invoice has several lines;
- a request refers to a vendor or an asset that should come from a list the
  application maintains, not from free text;
- one application often runs two related processes, such as a repair ticket and
  the spare-part requests raised for it.

Today these become free-text columns, one item per request, or several
applications that cannot refer to each other. Meanwhile the shared tables never
assumed one table per application: `core.audit_logs`, `core.outbox_events`,
`cap_work.work_items` and the export keys identify a record by
`(app_id, entity_type, entity_id)`, and cap-process already writes the table
name as `entity_type`.

## Decision

1. **`spec.tables` replaces the single-table fields.** `domainTable`,
   `domainAccess`, `stateMachine`, `attachments` and `uiSchema` move into a list
   of tables. Each table declares its `name` (`domain.<identifier>`), `columns`,
   `foreignKeys`, `access`, and optionally `stateMachine`, `attachments`,
   `belongsTo` and `ui`. `capabilities`, `roles`, `sharedAccess` and `workflows`
   stay at the application level. An application declares at least one table.
2. **Three kinds of table**, told apart by what they declare:

   | Kind | Declares | System columns | Behaviour |
   | --- | --- | --- | --- |
   | Process table | `stateMachine` | `id`, `created_by`, `org_unit_id` (cap-hr), `status`, `version`, `created_at`, `updated_at` | Exactly the table of ADR 0001: content is edited through Hasura in editable states, and `status` changes only through cap-process commands |
   | Plain table | neither `stateMachine` nor `belongsTo` | the same without `status` | Reads and writes through Hasura under the declared scopes; no state guard |
   | Child table | `belongsTo: {table, column}` | `id`, `created_by`, `version`, `created_at`, `updated_at`, and the declared parent column | Rows are part of their parent record (decision 4) |

3. **Foreign keys may point inside the application.** A foreign key references a
   shared table of the closed catalog or another table of the same application.
   A table of another application is never referenceable. Hasura relationships
   are generated for these keys; the target table's own permissions for the
   role decide what is visible through them.
4. **A child row follows its parent record.**
   - A role reads a child row when one of its select rules on the parent table
     admits the parent record. Child select rules name roles and columns, never
     a scope.
   - A role inserts, updates or deletes a child row when its update rule on the
     parent admits the parent record and the parent is in an editable state
     (always, when the parent is a plain table).
   - Every child change increments the parent's `version` in the same
     transaction, so a command sent with a version read before the change is
     refused (S04).
   - The parent column is NOT NULL and immutable: a row cannot move to another
     parent.
   - Both Hasura permissions and database triggers enforce these rules.
   - A child table declares no `stateMachine`, `attachments` or `scope`, and its
     parent is not itself a child table.
5. **Commands stay addressed by application.** Command names are unique within
   an application, so `POST /api/cap/process/commands/{app_id}/{command}` is
   unchanged and a workflow's `whenCommands` stays unambiguous. `entity_type` in
   audit rows, events and work items is the table name, as it already is.
6. **Deletion exists only for child rows.** Process and plain records are never
   deleted through the platform (a `cancel` command, not a delete). A child row
   may be deleted under decision 4, and the deletion is audited.
7. **Applications still declare; the operator still generates.** An application
   package ships no SQL. The operator creates every table, constraint, trigger,
   grant and Hasura permission from the declaration, and compares each live table
   with it before applying. A table removed from a manifest keeps its data and
   loses its permissions.
8. **No second manifest shape.** The single-table fields are removed, not kept as
   an alias. Application packages are converted, and the operator reinstalls
   them; a `core.apps` row stored in the old shape is refused by capability
   services (fail closed) until it has been reinstalled.

Unchanged: Keycloak and `dxos-authz` ([ADR 0007](0007-keycloak-authenticates-platform-authorizes.md)),
the scopes `own`, `department` and `organization`, record filters, the audit
chain, the outbox, and the rule that state changes go through version-checked
commands.

### Example

```yaml
spec:
  tables:
  - name: domain.procurement_vendors            # plain table
    columns:
    - {name: name, type: text, nullable: false}
    access:
      select:
      - {role: role_procurement_requester, scope: organization, columns: [id, name]}
      insert:
      - {role: role_procurement_finance, columns: [name]}

  - name: domain.procurement_requests           # process table
    columns:
    - {name: vendor_id, type: uuid, nullable: false}
    foreignKeys:
    - {column: vendor_id, references: domain.procurement_vendors}
    access: {select: [...], insert: [...], update: [...]}
    stateMachine: {initial: DRAFT, editableStates: [DRAFT], commands: [...]}

  - name: domain.procurement_items              # child table
    belongsTo: {table: domain.procurement_requests, column: request_id}
    columns:
    - {name: item, type: text, nullable: false}
    - {name: quantity, type: "numeric(14,2)", nullable: false, check: quantity > 0}
    access:
      select:
      - {role: role_procurement_requester, columns: [id, request_id, item, quantity]}
      insert:
      - {role: role_procurement_requester, columns: [request_id, item, quantity]}
      delete:
      - {role: role_procurement_requester}
```

## Consequences

- The manifest contract, the CRD, `contracts/manifest-v1.schema.json`, the
  operator's validator and planner, and cap-process's registry, commands,
  effects and file access change together; application packages and fixtures
  are converted. The HTTP contract of commands and the event envelope do not
  change.
- Process-table behaviour is preserved: an application with one process table
  behaves as before.
- The UI builds one list and form per table from its `ui` block and shows child
  tables inside their parent record.
- The data contract ([section 3](../contracts/data-contracts.md#3-domain-tables-and-initial-state)
  and [section 7](../contracts/data-contracts.md#7-dxapplication-manifest-contract))
  is rewritten with the implementation.

Not decided here, and not supported until decided:

- child tables of child tables;
- computed columns, such as a request total derived from its lines;
- command preconditions on child rows, such as "at least one line";
- attachments on plain or child tables;
- references between applications ([ADR 0002](0002-semantic-schema-governance-and-cross-app-data-sync.md)
  remains the rule for shared concepts).

## Alternatives considered

- **Applications ship their own SQL.** The operator applies DDL with a
  privileged connection, so application SQL would have full database authority,
  and the platform could no longer derive permissions, audit, version checks
  and UI from a declaration. Rejected.
- **Split an application into several one-table applications.** Applications
  cannot reference each other's tables, and a line item must follow its
  document's permissions, state and version, which separate applications cannot
  express. Rejected.
- **Keep one table and store lines in a JSON column.** No column permissions,
  constraints or foreign keys on the lines, and nothing for BI to aggregate.
  Rejected.
- **Add the table name to the command path.** It allows the same command name on
  two tables, but changes the HTTP contract for every client. Unique command
  names cost only a naming convention. Rejected for now.
