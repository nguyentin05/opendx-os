# Adding a declarative application

Use the [architecture](../architecture/README.md) and
[manifest contract](../contracts/data-contracts.md#7-dxapplication-manifest-contract)
to choose supported capabilities and the serialization shared by the Operator,
services, and UI. Resolve relevant contract gaps before extending independent
parsers or app packages.

1. Create `apps/<app>/manifest.yaml`, `workflows/*.yaml`, and `knowledge/*.md`.
   Declare domain fields, permissions, same-workspace relationships, state
   machine, attachment bindings, workflows/events, capabilities, and UI metadata.
2. Put thresholds and business handling in declared workflows. Map standard OT
   events to those flows; adding an app must not add an app-name branch to shared
   capabilities, UI, or bootstrap DDL.
3. Validate the package using the current schema/validator. Record what remains
   illustrative if the validator cannot yet enforce the required contract.
4. In an assigned deployment environment, review the Operator plan for generated
   DDL, permissions, managed resources, and revocations. Apply and publish only
   configuration that reaches `Ready` once lifecycle support exists.
5. Verify registry discovery without a UI rebuild, workspace/role isolation,
   initial state, stale commands, attachment checks, retry-safe events, and
   capability disablement. Reconcile must preserve other apps' valid grants.

The CLI currently supports a partial interface: inspect
[its command implementation](../../operator/cmd/dxos-operator/main.go) before
running it. The intended plan/apply lifecycle above is not evidence that the
current CLI provisions a full installation. It currently offers `plan`, a
`reconcile` scaffold, and `version`; `apply` is not implemented.

Use an app beyond maintenance, procurement, and timesheet to test pluggability.
A useful scenario combines employee reads, file bindings, same-workspace FKs,
and `ot.event.accepted`; duplicate delivery must not repeat business work.
Record evidence using [verification](verification.md).
