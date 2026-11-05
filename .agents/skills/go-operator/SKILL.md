---
name: go-operator
description: Use when designing, implementing, or reviewing dxos-operator, DDL/Hasura metadata generation, manifest lifecycle, and drift reconcile in operator/.
---

# Go Operator

Use the [architecture](../../../docs/architecture/README.md) and
[data/manifest contract](../../../docs/contracts/data-contracts.md).

1. Inspect the current parser, planner, CLI, and sample manifests. Identify
   serialization gaps before changing generators or connecting consumers.
2. Keep planning pure and apply side effects separate. Show added, changed, and
   revoked resources and the managed scope in the plan.
3. Generate constraints and permissions from the complete
   [write matrix](../../../docs/contracts/data-contracts.md#4-write-matrix-and-field-ownership),
   including composite FKs, initial state, version trigger, relationships, and
   each mutation path.
4. Reconcile against the active manifest set. Test shared-resource grants across
   multiple apps and partial update failures before registry publication.
5. Test planning in Go and actual permissions/triggers with PostgreSQL/Hasura
   integration. Record each level using [verification](../../../docs/development/verification.md).

Useful cases: procurement CRUD increments version once and invalidates old
approval; a command does not increment twice; a foreign-workspace FK fails;
reconciling one app preserves another app's valid employee access. These scenarios
exercise S01/S04 rather than only comparing generated strings.
