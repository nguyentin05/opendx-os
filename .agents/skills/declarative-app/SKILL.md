---
name: declarative-app
description: Use when designing, creating, or editing a Tier 3 application package in apps/, including the DXApplication manifest, business workflows, and per-app knowledge.
---

# Declarative application packages

Follow [adding an app](../../../docs/development/adding-an-app.md) and the
[manifest contract](../../../docs/contracts/data-contracts.md#7-dxapplication-manifest-contract).

1. Compare the schema, current parser, consumers, and a sample before writing
   the package; settle any serialization change in the shared contract.
2. Package the manifest, workflow YAML, and knowledge Markdown under `apps/<app>/`.
   Bind supported capabilities, fields, permissions, relationships, commands,
   attachments, and UI metadata through declarations.
3. Put thresholds and business handling in workflows subscribed to standard
   events. Review UI fields/actions against the actual backend grants.
4. Validate the package, inspect the generated plan, and check registry discovery
   using the lifecycle support that actually exists.

Use a fourth app with employees, files, and OT events to verify composite FKs,
attachment bindings, and retry-safe workflow delivery. Record illustrative
examples separately from validator and integration evidence using
[verification](../../../docs/development/verification.md).
