# Rule 02 — Data and authorization

Applies to schema, Hasura metadata, manifests, APIs, and their consumers. Read the [data contracts](../memory/data-contracts.md) before changing any interface in this group; that document is the source of truth for data fields and how they are implemented.

## Invariants

1. Use `core.*` for shared entities and `domain.*` for per-app records. Each domain table has independent `status` and `version`.
2. Identity, workspace, and effective permissions come from the authenticated context. Every link between two entities that have a workspace must stay within the same workspace, including file links and nested mutations.
3. Ordinary roles may write only allowed business fields. `INSERT`, `UPDATE`, upsert, and delete permissions are considered separately; SQL default values do not replace permission control.
4. Every successful change to a domain record increments `version` exactly once on the server side. Clients do not choose or reset the version. Commands check the version and write changes atomically.
5. The initial state is derived by the server from the manifest; state transitions go through the Command API. Content changes through CRUD are allowed only in states where editing is permitted.
6. Hasura enforces permissions by table, operation, role, row, and column. Relationship access requires valid permissions on the target table; PostgreSQL `GRANT` is not a substitute for Hasura metadata.
7. Control fields such as file scan status, registry, and audit are written by the service assigned that responsibility. User roles may not mark a file clean or alter audit history themselves.

## When changing a contract

Update the source of truth, then update the affected producers/consumers and examples. Describe how old data/manifests migrate if users already exist. During the design phase, record open decisions; do not describe a sketch without a schema/validator as a machine-enforceable contract.
