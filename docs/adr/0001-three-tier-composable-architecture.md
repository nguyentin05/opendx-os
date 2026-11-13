# ADR 0001: Three-Tier Composable Architecture with Shared Core and Isolated Domain Tables

- **Status:** Accepted
- **Date:** 2026-11-05

> **Partially superseded:** [ADR 0005](0005-department-and-application-data-model.md)
> replaces workspace isolation and shared-entity placement with the one-organization
> model. Unaffected architectural principles remain in force; examples below are
> historical where they conflict with ADR 0005.

## Context

Enterprise digital transformation platforms face a fundamental tension between:

1. **Data Silos:** Isolated department applications duplicate employee/work-item master records and cannot link cross-department workflows.
2. **Wide Sparse Shared Tables (`shared.tasks`):** Stuffing columns from multiple domain applications into a single database table causes status collisions across domains, violates relational normalization, and breaks partial-column queries.

## Decision

1. **Three-Tier Composable Boundary:**
   - **Tier 1 (`platform/` + `operator/`):** Headless infrastructure services (`PostgreSQL`, `Hasura`, `Keycloak`, `APISIX`, `Kestra`, `SeaweedFS + ClamAV`, `OpenBao`) configured out-of-band by `dxos-operator`.
   - **Tier 2 (`capabilities/` + `workspace-ui/`):** Pluggable H-P-D-I capabilities (`cap-process`, `cap-data`, `cap-ai`) and a schema-driven UI App Shell reading from `core.app_registry` and `core.capability_registry`.
   - **Tier 3 (`apps/*`):** Declarative domain packages (`maintenance`, `procurement`, `timesheet`) defined via `manifest.yaml`, `workflows/*.yaml`, and `knowledge/*.md`.
2. **Shared Core Entities (`core.*`) + Isolated Domain Tables (`domain.*`):**
   - Shared master entities (`core.employees`, `core.devices`, `core.work_items`) reside in `core.*` with composite uniqueness `(workspace_id, id)`.
   - Each Tier 3 application owns an isolated table in `domain.*` with composite same-workspace foreign keys `(workspace_id, <foreign_id>)` and its own independent `status` and server-incremented `version`.
3. **Separated CRUD and Version-Guarded State Commands:**
   - Hasura handles filtered reads and allowed draft edits while a `BEFORE UPDATE` trigger increments `version` exactly once per modification.
   - All state transitions execute atomically through `cap-process` Commands with `expected_version` and idempotency receipts.

## Consequences

- Domain applications share master context (`core.employees`, `core.work_items`) with strict relationship isolation while never overwriting another application's lifecycle state (`S01`, `S04`, `S05`).
- New domain applications can be plugged in via `dxos-operator` without modifying Tier 1 or Tier 2 runtime code.
