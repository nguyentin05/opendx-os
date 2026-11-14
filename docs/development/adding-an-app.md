# Adding a declarative application

Use the [architecture](../architecture/README.md) and
[manifest contract](../contracts/data-contracts.md#7-dxapplication-manifest-contract)
to choose supported capabilities and the serialization shared by the Operator,
services, and UI. Resolve relevant contract gaps before extending independent
parsers or app packages.

1. Create `apps/<app>/manifest.yaml`, `workflows/*.yaml`, and `knowledge/*.md`.
   Declare capabilities, the roles the app owns, and its tables: for each one
   the fields and foreign keys, per-role access with a scope (own, department,
   organization), UI metadata, and for a process table its state machine and
   attachment bindings. Rows that are part of another record (lines of a
   document) go in a child table (`belongsTo`). Declare workflows on the commands of those tables.
2. Put thresholds and business handling in declared workflows. Map standard OT
   events to those flows; adding an app must not add an app-name branch to shared
   capabilities, UI, or bootstrap DDL.
3. Validate the package: `go -C operator run ./cmd/dxos-operator validate ../apps/<app>/manifest.yaml`.
4. In an assigned deployment environment, review the Operator plan for generated
   DDL, permissions, managed resources, and revocations. Apply and publish only
   configuration that reaches `Ready` once lifecycle support exists.
5. Verify registry discovery without a UI rebuild, role scopes,
   initial state, stale commands, attachment checks, retry-safe events, and
   capability disablement. Reconcile must preserve other apps' valid grants.

The [CLI](../../operator/cmd/dxos-operator/main.go) offers `validate`, `plan`,
`apply` (install or upgrade one app), `reconcile` (install exactly the apps of a
directory; with `--interval` it keeps doing so, which is how the Compose
`operator` service runs), `export-crd` and `controller`. Under Compose, copying
a package into `apps/` is enough to install it.

Use an app beyond maintenance, procurement, and timesheet to test pluggability.
A useful scenario combines user reads, file bindings, department scope,
and `ot.event.accepted`; duplicate delivery must not repeat business work.
Record evidence using [verification](verification.md).
