# ADR 0003: Separation of Core Kernel Schema and Pluggable Capability Data Schemas (cap-data)

- **Status:** Accepted
- **Date:** 2026-11-05

## Context

In OpenDX-OS, we distinguish between:
1. **Universal Enterprise Kernel:** Primitives needed by *every* organization regardless of industry (tenants, employees, work task routing, files, append-only audit, transactional outbox, service/app registries).
2. **Specialized Operational Technology (OT) & Telemetry:** Physical machine assets (`devices`) and high-frequency sensor readings (`ot_events`).

In non-manufacturing deployments (pure office administration, procurement, financial approval, timesheets), physical machinery and sensor streams do not exist. Hardcoding `devices` and `ot_events` into the base kernel schema would violate the **Microkernel Principle** and burden purely administrative deployments with industrial IoT concepts.

## Decision

We physically decouple the database initialization into two distinct layers:

### 1. Pure Core Kernel Schema (`01_core_schema.sql`)
Contains strictly universal platform primitives:
- `core.workspaces`: Multi-tenant isolation boundary.
- `core.employees`: Universal personnel identity (`id, workspace_id, full_name, email, department`).
- `core.work_items`: Core work routing with generic employee assignment (`id, workspace_id, title, description, priority, assigned_employee_id`). Contains zero device-specific columns, enforcing the Principle of Least Privilege across all non-manufacturing domains.
- `core.file_objects`: Blob quarantine and ClamAV scan metadata.
- `core.audit_logs`: Serialized SHA-256 hash-chain audit trail.
- `core.outbox_events`: Universal transactional event bus for asynchronous workflows and integrations.
- `core.bump_domain_record_version()`: Server-side OCC version trigger.

### 2. Numbered Core Kernel & Extension Registries (`platform/postgres/init/`)
Organized as sequential initialization files natively executed by Docker PostgreSQL:
- `01_core_schema.sql`: Core tenant isolation, universal employee identity, generic work items, quarantine blobs, serialized audit hash-chain, durable outbox bus, and OCC version bumper.
- `02_capability_registry.sql`: Dynamic Tier 2 capability catalog (`core.capability_registry`).
- `03_app_registry.sql`: Dynamic Tier 3 application manifest catalog (`core.app_registry`).
- `04_capabilities.sh`: Dynamic capability loader executing enabled capabilities.

### 3. Pluggable Capability Modules (`platform/postgres/capabilities/`)
Separated into standalone capability schemas loaded conditionally on startup via `04_capabilities.sh` (filtered by `ENABLED_CAPABILITIES`):
- `cap_process.sql` ([P]): Process orchestration & OCC transitions, command idempotency receipts (`core.command_receipts`), and workflow step idempotency tracking (`core.processed_workflow_steps`).
- `cap_data.sql` ([D]): Physical machinery catalog (`core.devices`), IoT task association (`core.device_work_items`), telemetry ingestion (`core.ot_events`), and lakehouse export/lineage (`core.export_batches`, `core.data_lineage`).
- `cap_ai.sql` ([I]): Registers AI OCR and workspace policy RAG capability.

### 4. Capability-Gated Application Access
Domain applications (Tier 3) declare their capability requirements in `manifest.yaml`:
```yaml
capabilities:
  - name: cap-data
    required: true
```
Applications that do not declare `cap-data` (e.g., `procurement`, `timesheet`) receive zero Hasura permissions or relationship traversals to device or telemetry tables.

## Consequences

- **Pure Kernel:** The core database schema remains clean, lightweight, and universally applicable to any business domain.
- **Native Docker Initialization:** Initial core schema and registries execute sequentially (`01`, `02`, `03`) directly through `/docker-entrypoint-initdb.d/` without complex subfolder runner scripts.
- **Pluggable Capabilities:** Only optional Tier 2 capabilities reside in `platform/postgres/capabilities/`.
- **Conditional Capability Loading:** Enterprise deployments without IoT machines do not load `cap_data.sql` and have zero IoT tables in PostgreSQL.
