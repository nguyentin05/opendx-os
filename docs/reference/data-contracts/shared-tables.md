---
title: Shared tables
description: The core tables and capability-owned storage every installation shares.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Shared tables

Tier 1 (`core`) never references Tier 2 or Tier 3 tables. Entity IDs are UUIDs;
audit IDs are BIGSERIAL.

| Table | Fields and constraints |
| --- | --- |
| `core.users` | `id` (= Keycloak `sub`), `email` (unique, case-insensitive), `display_name`, `status` (`ACTIVE`/`DISABLED`). A one-way mirror of Keycloak identity: `dxos-authz` inserts the row on a user's first request and updates `email` and `display_name`; it never changes `status`, and a `DISABLED` account is refused. No runtime role may delete a row. Marking accounts disabled or deleted in Keycloak as `DISABLED` needs the account sync (#23), which is not implemented |
| `core.roles` | `name`, owner `app_id` or `cap_id` (at most one; neither for platform roles), `description` |
| `core.user_roles` | `(user_id, role_name)`, `granted_by`, `granted_at`: the only record of who holds which role. Changed by platform operators through `dxos-authz`, which calls `core.grant_role` and `core.revoke_role`; each change is audited in the `core` chain |
| `core.apps` | `app_id`, `display_name`, `version`, validated `spec`, `status` (`Pending`/`Applying`/`Ready`/`Failed`/`Removed`), `last_error`. Rows are never deleted |
| `core.capabilities` | `cap_id`, `display_name`, `category_hpdi` (`H`, `P`, `D` or `I`), `base_route`, `enabled` |
| `core.app_capabilities` | `(app_id, cap_id)`; a capability cannot be disabled while an installed app uses it |
| `core.app_deployments` | one row per install or upgrade attempt, with its status and `last_error` |
| `core.app_workflows` | the flows the operator deployed for each application workflow (`namespace`, `flow_id`, `definition`, `revision`); cap-process routes events by it |
| `core.schema_migrations` | the ledger of `platform/postgres/migrations` applied to this database, with a checksum that detects an edited migration |
| `core.files` | `id`, `app_id`, `bucket`, `object_key`, `file_name`, `scan_status`, `sha256_hash`, `uploaded_by`, `mime_type`, `size_bytes`, scan scheduling; identity and content columns are immutable |
| `core.audit_logs` | append-only, hash-chained per application (`chain` = `app_id`, or `core`): `actor_id`, `acting_role`, `action`, `entity_type`, `entity_id`, `entity_version`, `payload` |
| `core.outbox_events` | durable events with `app_id`, entity identity and version, payload and delivery state |

Registry, audit and outbox tables are written by the operator or the assigned
service; user roles read only what their application grants (`sharedAccess`).

As established by [ADR 0002](../../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md),
domain attributes such as `skill_level` and `work_shift` belong in application
tables; a shared user does not grant cross-app traversal.

### Capability storage

[ADR 0003](../../adr/0003-modular-capability-schema-separation-for-iot-data.md)
separates the kernel from capability SQL in `platform/postgres/capabilities/`,
loaded for the capabilities listed in `ENABLED_CAPABILITIES`. Each capability
owns its schema and registers itself in `core.capabilities`.

| Capability | Tables |
| --- | --- |
| `cap-hr` | `cap_hr.org_units` (departments, optional parent), `cap_hr.staff` (one department per user, `position`). Maintained by users holding the capability role `role_hr_admin` through the cap-hr service, which changes them only through audited functions (`cap_hr.create_org_unit`, `update_org_unit`, `assign_staff`, `remove_staff`); each change is in the `core` audit chain |
| `cap-work` | `cap_work.work_items`: `app_id`, `title`, `description`, `priority`, `board_column`, `assignee_id`, source record (`source_type`, `source_id`), `created_by` |
| `cap-process` | `cap_process.command_receipts` (command idempotency), `cap_process.processed_workflow_steps` (effect deduplication) |
| `cap-data` | `cap_data.devices`, `cap_data.ot_events`, `cap_data.export_batches`, `cap_data.data_lineage`. The cap-data service handlers still target retired core tables and are scheduled for v1.1.0 (#111) |
| `cap-ai` | `cap_ai.*`: knowledge collections, documents, versions, chunks and embeddings; ingestion jobs; model registry, reservations and usage; Assistant conversations. Created by the migrations in `platform/postgres/migrations/cap_ai/` ([RAG reference](../rag/index.md)) |

Apps that do not declare a capability get no permissions or relationships on its
tables. Removing access or disabling a capability does not by itself authorize
deleting its stored data.
