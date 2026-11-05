# ADR 0002: Core Schema Purity and Semantic Cross-App Data Synchronization

- **Status:** Accepted
- **Date:** 2026-11-05

## Context

In an enterprise composable open-core architecture, master entities (such as `core.employees`) risk architectural degradation when domain-specific attributes are prematurely placed into Tier 1:

1. **Column Bloat & Wide Sparse Table Anti-Pattern:** Adding domain-specific fields (e.g., `skill_level` for maintenance or `work_shift` for timesheet) directly to `core.employees` causes the core schema to rapidly bloat with sparse columns as new applications (training, health, recruitment) are added.
2. **Semantic Collision via String-Matching:** Attempting to infer shared fields simply by column name matching (`col_name == 'skill_level'`) creates catastrophic semantic errors (e.g., a technician machinery certification conflated with an employee recreational sports ranking).

## Decision

We establish strict core schema purity alongside a 3-scenario semantic schema governance model:

### 1. Strict Core Master Purity (Tier 1)
`core.employees` stores strictly universal organizational identity master data:
```sql
CREATE TABLE core.employees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    full_name TEXT NOT NULL CHECK (length(trim(full_name)) > 0),
    email TEXT NOT NULL CHECK (length(trim(email)) > 0),
    department TEXT NOT NULL CHECK (length(trim(department)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, email)
);
```
No domain-specific attributes are permitted in Tier 1 master entities.

### 2. Contextual Isolation via Relationship Traversal
Applications access shared core entities with contextual relationship isolation:
- `role_maintenance` reads `core.employees` and traverses only `employee -> domain.maintenance_records`.
- `role_timesheet` reads `core.employees` and traverses only `employee -> domain.timesheet_entries`.
Cross-domain relationship traversals are strictly prohibited in Hasura metadata permissions.

### 3. Three-Scenario Governance for Domain Attributes (Tier 3)

When Tier 3 applications define domain-specific attributes, `dxos-operator` evaluates three scenarios:

- **Scenario 1 (Independent Attributes):**
  Applications manage distinct attributes in their isolated `domain.*` tables. Zero coupling, zero synchronization overhead.
- **Scenario 2 (Shared Concept Binding via Semantic Type):**
  When two applications share an authoritative enterprise concept (e.g., formal technical certification):
  1. Both manifests bind the respective columns to a global **`semanticType` URI** (e.g., `urn:dxos:concept:hr:technical_certification`).
  2. The system enforces a **Single-Writer / Multi-Reader** contract: one application is registered as the authoritative `Write Owner`, while other applications register as `Read Replicas`.
  3. Updates by the Owner emit transactional events to `core.outbox_events`. Kestra workflows fan out the event to update subscriber read replicas asynchronously.
- **Scenario 3 (Fail-Fast Schema Conflict):**
  If two applications declare the same `semanticType` with incompatible data types, or if both applications attempt to claim `Write Owner` authority on the same semantic concept, `dxos-operator` fails immediately during `plan` / `reconcile` (**Fail-Fast**). No conflicting schema can reach runtime.

## Consequences

- `core.employees` remains permanently clean, minimal, and unaffected by future application additions.
- Applications evolve their domain schemas independently without risking write-write data corruption.
- Shared business data synchronizes reliably through explicit event-driven fan-out rather than fragile database triggers or magic column name guessing.
