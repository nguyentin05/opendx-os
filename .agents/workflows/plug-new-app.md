# Workflow — Plugging in a business application

Use when designing or adding an app package that uses existing capabilities.

1. Read [declarative-app](../skills/declarative-app/SKILL.md), determine the manifest schema currently in effect and the capabilities to use. If this is design-only, settle the missing fields in the [data contracts](../memory/data-contracts.md) before both sides write independent parsers/packages.
2. Create/update `apps/<app>/manifest.yaml`, workflow YAML, and knowledge Markdown. Permissions, FKs, state machine, files, events, and UI are all declared by contract; do not modify the core just to add an app name.
3. **In design:** check the illustrative manifest, the permission/data flow, and the acceptance scenarios; state clearly which CLI/validator does not exist yet. Stop at the design output if that is the task scope.
4. **Once implemented and assigned to plug in the app:** run `dxos-operator plan -f <manifest>` from the implementation project, and review the DDL/FK, Hasura permissions, and resources it will manage. Run `apply` in an environment within the assigned scope and verify `Ready` before promoting the app.
5. Verify that the UI receives the new app through the registry without a rebuild; workspace/role permissions, initial state, stale versions, and files still behave correctly. Reconcile fixes app drift but preserves other apps' valid permissions.
6. For apps with OT/workflows, verify events are routed through declarations and that duplicate delivery does not create duplicate work. When a capability is disabled, dependent features report unavailability and independent parts keep working.

Record evidence per [pre-pr-gate](../hooks/pre-pr-gate.md). Test with an app beyond the three illustrative apps to catch app lists hard-coded in the runtime.
