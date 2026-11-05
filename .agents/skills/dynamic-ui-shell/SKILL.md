---
name: dynamic-ui-shell
description: Use when designing, implementing, or reviewing the React/TypeScript App Shell in workspace-ui/, including schema-driven UI, switching app/role, and capability state.
---

# App Shell driven by schema and effective permissions

Read [architecture](../../memory/architecture.md), the [data contracts](../../memory/data-contracts.md), and [Rule 03](../../rules/03-anti-patterns-and-security.md). The UI uses the published configuration of apps/capabilities; the backend remains the place that enforces permissions.

## How to do it

1. Build navigation, tables, forms, and actions from the registry/UI schema/state machine filtered for the caller. A new app must not require a component that branches on a domain name.
2. Separate **choosing an app** from **choosing a role**. Show only roles within the authenticated permissions and keep the constraint in the backend. Demonstrating another role requires signing in with an appropriate account or a valid permission-grant flow; do not set claims/headers yourself to gain permissions.
3. When switching app/role/workspace, cancel or discard results from old requests and refresh data for the new context. The UI schema must not expose columns or relationships that data permissions forbid reading.
4. When a dependent capability is disabled/unavailable, disable the corresponding feature and show the reason; independent parts remain usable. The backend rejects unavailable operations even if an old client still sends the request.
5. Send commands with the version the user is viewing and an idempotency key stable for the same action/retry. On `409` stale version, load the new state so the user can review it before a new approval decision; do not automatically re-approve with the new version. For GraphQL, consider both `errors` and `data`, not only the HTTP status.

## Verification example

A token with only `role_timesheet`: the App Switcher does not add `role_manager` and does not expose the maintenance relationship through data or a stale cache. When cap-ai is disabled, the OCR/RAG area reports unavailability while independent H/P/D features keep working.

Keep the H-P-D-I areas per [architecture](../../memory/architecture.md), but permissions and availability decide which parts the user may use. Hand off the screen-state design during the design phase; once code exists, add tests for context switching, disabled capabilities, and stale approvals per the [gate](../../hooks/pre-pr-gate.md).
