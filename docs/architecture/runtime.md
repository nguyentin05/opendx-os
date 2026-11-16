---
title: Runtime
description: Request-path services, capability availability, storage, and event delivery.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Runtime

Runtime services serve requests from published configuration. They do not call the
Operator per request, and applications continue serving while the Operator is
stopped.

| Area | Intended responsibility |
| --- | --- |
| Keycloak and APISIX | OIDC SSO/MFA, token validation, routing, and rate limits |
| PostgreSQL, pgvector, and Hasura | Transactional data and filtered CRUD/subscriptions by role and column |
| Kestra | Declarative workflows fed by durable, retryable events |
| SeaweedFS and ClamAV | Objects, Parquet, quarantine, and scanning |
| OpenBao | Signed audit checkpoints outside PostgreSQL |
| `dxos-operator` | Manifest lifecycle, DDL, Hasura metadata, roles, routes, flows, and registry publication |
| `dxos-authz` | Resolves the acting role from `core.user_roles` for Hasura and the UI; platform operators assign roles |
| `cap-hr` | Departments and staff for the `department` scope |
| `cap-process` | Commands, validation, and file lifecycle |
| `cap-data` | OT validation/deduplication, reconciled export, DuckDB BI, and lineage |
| `cap-ai` | Schema-driven OCR and scoped RAG with citations |
| `workspace-ui` | Registry-driven navigation, Kanban/Calendar, forms, inbox, OT/BI, and OCR/RAG |

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
`INFECTED`. Downloads, OCR, and commands that use a file check the caller's
permission and `CLEAN` status. BI exports only terminal records and reads only
published batches; lineage preserves the full source set behind each aggregate.

See [files, OT, and BI](../reference/data-contracts/files-ot-bi.md).
