---
title: Runtime
description: Request-path services, capability availability, storage, and event delivery.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Runtime

Runtime services serve requests from published configuration. They do not call the
Operator per request, and applications continue serving while the Operator is
stopped.

| Area | Responsibility | Status |
| --- | --- | --- |
| Keycloak | OIDC sign-in; tokens carry identity only | Implemented |
| APISIX | Routes `/v1/graphql`, `/api/auth/*`, `/api/cap/{process,hr,ai}/*` and `/realms/*`; closes the internal effect route | Implemented; rate limits are not configured |
| `dxos-authz` | Resolves the acting role from `core.user_roles` for Hasura (auth webhook) and the UI; platform operators assign roles | Implemented |
| PostgreSQL and Hasura | Transactional data and filtered CRUD by role, row and column | Implemented; pgvector with the `compose.models.yaml` overlay |
| Kestra | Application workflows fed by the cap-process outbox | Implemented |
| SeaweedFS and ClamAV | File objects, quarantine and scanning | Implemented |
| `dxos-operator` | Manifest lifecycle: DDL, triggers, Hasura metadata, roles, flows and registry publication | Implemented (CLI and Compose reconcile loop) |
| `dxos-controller` | The same lifecycle for `DXApplication` resources on Kubernetes | Implemented |
| `cap-hr` | Departments and staff for the `department` scope | Implemented |
| `cap-process` | Commands, validation, files, record history and event delivery | Implemented |
| `cap-ai`, parser, worker, `dxos-knowledge-dispatcher` | Knowledge ingestion, role-scoped search with citations, personal library, Assistant | Implemented in the `compose.rag.yaml` overlay; no OCR |
| `cap-data` | OT validation and deduplication, reconciled export, BI and lineage | Planned for v1.1.0 (#111) |
| `workspace-ui` | Registry-driven navigation, lists, forms, record detail, inbox, My Work, knowledge pages, Assistant | Implemented; no Kanban, Calendar or OT/BI views |
| Audit checkpoint store | Signed audit checkpoints outside PostgreSQL | Not implemented |

Dependency manifests and deployment configuration record actual technology
versions.

## Capability availability

Each capability provides health and registry metadata. Disabled or unavailable
capabilities make dependent features unavailable with a reason, while independent
features continue working. The backend also checks availability and permissions;
the UI is never the authority.

## Events and workflows

A state change writes the record, its audit row, the durable outbox event, and the
idempotency receipt in one transaction. Application workflows are Kestra flows
deployed by the Operator. cap-process routes an event to the flow recorded in
`core.app_workflows`; the destination never comes from the event or its caller.

Delivery may repeat. Every business effect must tolerate retries, and a workflow
step runs at most once per event. See
[workflow deployment and events](../reference/api/events.md) for the contract.

## Storage and export

New files start `QUARANTINED`; only the scanning service moves them to `CLEAN` or
`INFECTED`. Downloads and commands that use a file check the caller's permission
and `CLEAN` status. BI exports (v1.1.0) will export only terminal records and read
only published batches; lineage will preserve the full source set behind each
aggregate.

See [files, OT, and BI](../reference/data-contracts/files-ot-bi.md).
