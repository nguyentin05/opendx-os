---
name: dynamic-ui-shell
description: Use when designing, implementing, or reviewing the React/TypeScript App Shell in workspace-ui/, including schema-driven UI, switching app/role, and capability state.
---

# Dynamic App Shell

Use the [architecture](../../../docs/architecture/README.md),
[registry/manifest contract](../../../docs/contracts/data-contracts.md), and
[security criteria](../../../docs/contracts/security-and-acceptance.md).

1. Trace published registry data and authenticated context into navigation,
   tables, forms, and commands. Identify placeholder data before wiring APIs.
2. Keep app selection distinct from role selection. Offer authenticated grants;
   a role demo uses an appropriate account or permission-grant flow.
3. On app/role/workspace changes, cancel or discard old requests and refresh the
   cache/context. Render only permitted fields, relationships, and actions.
4. Show unavailable dependent features with a reason while leaving independent
   features usable; cover both disabled and temporarily unavailable services.
5. Keep the viewed version and one stable idempotency key for an action/retry.
   On `409`, reload for a new user decision instead of automatically approving
   the new version. Inspect GraphQL `errors` as well as `data`.

Useful cases: a timesheet-only token cannot select a manager grant or recover
maintenance data from stale cache; disabling cap-ai leaves independent H/P/D
features working. Check switching and stale approvals using the actual UI runner
and [verification guidance](../../../docs/development/verification.md).
