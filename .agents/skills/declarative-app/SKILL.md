---
name: declarative-app
description: Use when designing, creating, or editing a Tier 3 application package in apps/, including the DXApplication manifest, business workflows, and per-app knowledge.
---

# Declarative application package

An app declares its domain and uses shared capabilities. Read [Rule 01](../../rules/01-architecture-and-boundaries.md) and the [data contracts, manifest section](../../memory/data-contracts.md#7-dxapplication-manifest-contract) before writing the package.

## How to do it

1. Determine the manifest schema shared by both the Operator and the App side. If only the logical shape exists, settle the serialization, validator, and one valid example within the design scope before building independent parsers/packages.
2. Keep the package in `apps/<app>/`: `manifest.yaml`, `workflows/*.yaml`, `knowledge/*.md`. Declare domain tables, per-operation permissions, same-workspace FKs, state machine, attachments, workflow/events, dependent capabilities, and UI according to the source of truth.
3. Put thresholds and business handling in declared workflows. Standard OT events are mapped to the app's workflows; do not add app-name branches to cap-data or the Operator.
4. Match the UI to the actual columns/permissions and allowed commands. Initial values and system columns are server-managed; an app package must not open state/version write permissions to shorten the implementation path.
5. Use [plug-new-app](../../workflows/plug-new-app.md) for plan/apply and discovery checks once those tools exist and are within the task scope.

## Verification example

Add a fourth app that uses employees, files, and OT events. The package must be able to describe employee read permissions, the same-workspace composite FK, attachment columns, and a flow that receives `ot.event.accepted`; duplicate delivery does not create duplicate work. Adding the package must not require editing runtime code by app name.

The design output includes an illustrative manifest or a settled schema, permissions/FKs, producer/consumer, and acceptance scenarios. The implementation output adds validator, plan, and integration results per the [gate](../../hooks/pre-pr-gate.md). Distinguish an illustration with no validator from a manifest that has been confirmed valid.
