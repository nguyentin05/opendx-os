---
name: go-operator
description: Use when designing, implementing, or reviewing dxos-operator, DDL/Hasura metadata generation, manifest lifecycle, and drift reconcile in operator/.
---

# Go Operator and reconcile

The Operator configures the system outside the runtime request path. Read [architecture](../../memory/architecture.md), the [data contracts](../../memory/data-contracts.md), and [Rule 01](../../rules/01-architecture-and-boundaries.md) before changing an interface or the configuration generator.

## How to do it

1. Determine the phase and the manifest schema in effect. In design, record the schema/validator/example that must be settled; do not assume a future parser or CRD already exists.
2. Separate pure DDL/metadata planning from the apply step that has side effects. The plan must show which resources will be added, changed, or revoked, and which part is currently managed by the Operator.
3. Generate DDL, same-workspace composite FKs, defaults, the version trigger, and permissions for the contract's **entire write matrix**; check insert/update/upsert/nested/delete separately. Generate relationship read permissions based on target-table permissions.
4. Reconcile against the active manifest set within the managed scope. Add missing parts, revoke excess parts, and keep valid permissions that other apps need; do not compare one manifest and then delete all remaining permissions.
5. Report lifecycle and structured errors. Publish only configuration that has reached `Ready`; `plan` has no side effects.

## Verification and output

- In design: a sample plan containing the expected DDL/permissions, the contract source, consumers, and the relevant S01/S04 scenarios.
- Once code exists: tests for the planning part and integration tests with PostgreSQL/Hasura for permissions and triggers; use the [gate](../../hooks/pre-pr-gate.md) to record evidence.
- Required example: creating a procurement record must not let the client send state/version; editing `quoted_amount` through Hasura increments the version once; approving with an old version returns `409`. A command does not add another version increment beyond the trigger.
- Check FKs to another workspace and try reconciling one app while another app also uses `core.employees`.

Mistakes to avoid: treating a SQL DEFAULT as insert permission, checking only update, or revoking grants outside the Operator's managed scope. The fix belongs in the source of truth; do not add app-name exceptions to the Operator.
