# DX-OS architecture contract

Read when the work changes components, interfaces between tiers, or the application lifecycle. [Rule 01](../rules/01-architecture-and-boundaries.md) is the source of truth for boundaries and ownership.

## Components

| Area | Components and responsibilities |
| --- | --- |
| Identity & gateway | Keycloak provides OIDC SSO/MFA and authenticated permissions; APISIX validates Bearer tokens, routes, and rate-limits |
| Transactional data | PostgreSQL + pgvector store `core.*` and `domain.*`; Hasura provides CRUD/subscriptions with permissions by workspace, role, and column |
| Workflow | Kestra runs declarative workflows; events from transactions are delivered through a durable mechanism with retries |
| Files & audit | SeaweedFS stores objects and Parquet; ClamAV scans files; OpenBao signs and keeps audit checkpoints outside PostgreSQL |
| Control plane | `dxos-operator` deploys manifests, DDL, Hasura metadata, Keycloak roles, Kestra flows, APISIX routes, and the registry |
| `cap-process` — P | State commands, version control, business input validation, and the file lifecycle |
| `cap-data` — D | OT validation/deduplication, reconciled export, DuckDB BI, and data lineage |
| `cap-ai` — I | Schema-driven OCR on eligible files, RAG by workspace/app/caller permissions, with cited sources |
| `workspace-ui` — H and App Shell | Navigation, Kanban/Calendar, forms, approval inbox, OT/BI, and OCR/RAG driven by the registry and effective permissions |

Technology versions are fixed in the implementation's manifests/dependencies once source code exists. The inventory above is not evidence that a component has been installed or tested.

## Control plane and runtime

The Operator supports `plan`, `apply`, `reconcile`, `uninstall`, and `verify-audit`; `plan` only produces a plan and does not change services. The deployment lifecycle is `Pending -> Applying -> Ready | Failed`, with structured errors and manifest versions.

Announce an app as ready only after the required configuration has succeeded. When an update fails, the desired state and the active configuration must be distinguishable; do not promote an unready version. The runtime reads the published configuration and does not call the Operator per request.

Reconcile adds missing permissions and revokes excess permissions **within the resources/roles the Operator manages**, based on the active manifest set. When several apps share `core.*`, the result must combine their valid permissions; do not remove another app's permissions because only one manifest was checked.

## Plug/unplug and enable/disable

Each capability provides a health endpoint and metadata in `core.capability_registry`. An app declares the capabilities it needs. When a capability is disabled or unavailable, the UI shows the related feature as unavailable and explains why; independent features keep working. The backend still enforces permissions and availability itself.

Removing an app revokes the permissions, routes, workflows, and registrations owned by that app. Deleting domain data is a separate operation that requires a retention policy and user-approved scope; it must not be inferred from an ordinary uninstall command.

The three illustrative apps are maintenance, procurement, and timesheet. Business processes, OT thresholds, and workflow bindings live in the app package and must not be hard-coded into shared capabilities.
