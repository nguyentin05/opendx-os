# ADR 0005: Department and application data model

- **Status:** Accepted
- **Date:** 2026-11-13
- **Decision source:** [Issue #9](https://github.com/nguyentin05/opendx-os/issues/9), the owner-supplied agreed model and subsequent approval to resolve that issue.
- **Supersedes in part:** ADR 0001 shared-entity/workspace placement, ADR 0002 employee identity/department placement, and ADR 0003 workspace/core-work-item placement. Their three-tier boundaries, domain-attribute ownership, optional capabilities and concurrency principles remain applicable.

## Context

One OpenDX-OS installation serves one organization. An application identifies a business context, while a department identifies an organizational unit; these are independent dimensions. The previous workspace-based contract conflated tenancy, application publication and department scope, while newer SQL already uses organization-wide users and app-labelled records. SQL drift alone was not an architecture decision; this ADR records the agreed target and its compatibility obligations.

## Decision

### Tier 1 kernel and dependency direction

`core` contains universal identity (`users`), role definitions and membership (`roles`, `user_roles`), application/capability registries (`apps`, `capabilities`, `app_capabilities`), files, append-only audit and durable outbox. Roles may be owned by an application or a capability; platform roles may be platform-owned. Core never depends on a Tier 2 capability table or Tier 3 domain table, business workflow or service.

A `core.files.app_id -> core.apps.app_id` reference is a reference to a kernel registry identity, not a dependency on the application's domain schema. App/capability registry identifiers are generic platform data; they do not authorize per-app hardcoded runtime branches. Capability/domain tables may reference approved Core identity in the other direction.

Keycloak authenticates identity. Current `core.users` status and `core.user_roles` membership are authoritative for business access; business-role claims in a JWT cannot override current DB denial. Role administration uses an authorized, audited DB boundary. API, File Service and Hasura paths must converge on that authority before a runtime cutover is declared complete. This decision does not select the precise Hasura integration or claim existing JWT middleware already implements it.

### Independent application and department identity

`app_id` names the application that produced a record. `org_unit_id` names a department owned by `cap_hr`; it is not a replacement tenancy identifier and is not inferred from app identity. An application may serve multiple departments; a department may use multiple applications.

Tier 2 data has two forms:

- Organization-wide shared master data has no `app_id`, for example `cap_hr.org_units` and staff/department membership.
- Data produced while an application uses a capability carries `app_id`, for example app-origin work items or telemetry. System-origin capability data needs its own explicit provenance policy; this ADR does not permit fabricated app IDs.

App domain records remain in `domain.*` with their independent business lifecycle/version and manifest-controlled access. Department-labelled capability/domain records reference `cap_hr` only when that capability is declared and available. Core does not gain an `org_unit_id` foreign key into `cap_hr` merely to support department-scoped access.

### Remove workspaces; place optional data in capabilities

There is no workspace or `workspace_id` in the target installation model. Remove workspace-based isolation/presets/claims through a coordinated migration, not by ignoring legacy fields in one consumer. Identity is `core.users`; organizational staff, departments and positions belong to `cap_hr`. Work items belong to `cap_work`, not Core. Physical assets/telemetry remain optional capability data. Application attributes continue to belong to application-owned domain structures as described in ADR 0002.

### Role scope

Each application role declares own-record, own-department or organization-wide scope. Membership alone does not grant all rows/columns, nor access to unrelated app roles or private resource ACLs. Own-record scope uses authoritative recorded ownership, not a caller-supplied user ID. Own-department scope resolves current authenticated staff membership from `cap_hr`; missing/disabled department capability or unresolved membership denies department-dependent access. Organization scope applies only within the role's authorized application/resources, not across every application or PRIVATE document.

Mutation defaults and grants must validate app/department/owner linkage server-side; arbitrary `org_unit_id` or `app_id` input cannot expand authority. The exact manifest serialization, SQL predicates and department-transfer treatment require coordinated contracts before implementation; this ADR does not silently create those fields in today's parser.

## Alternatives considered

### Workspace equals department — rejected

This would preserve legacy field names, but departments are shared organization structure, not tenants. It incorrectly couples app publication and identity to departments, makes cross-department applications awkward and duplicates organization-wide data. Department changes must not move an organization or redefine app identity.

### Workspace equals application — rejected

This would approximate application context with existing registry keys, but conflates app identity with organization scope and prevents departments from remaining an independent dimension. Shared personnel and capability master data would acquire artificial per-app ownership.

### One organization with independent app/department dimensions — selected

This matches the installation boundary and supports multiple apps per department and multiple departments per app while retaining capability ownership. It requires updating all workspace-era consumers and cannot be delivered by renaming one column.

## Consequences and follow-up scope

The architecture is accepted; migration/runtime conformance is partial. At the recorded source baseline `70271c335e58714301d502f0547d9eb76c8b05e8`, SQL initializes `core.files`, `cap_hr` and `cap_work`, but File Service/OCR/auth, manifests, Operator registry logic and documentation still contain workspace-era assumptions.

Follow-up contract/runtime work must inventory and update:

- Data/security contracts and S01 isolation expectations; app/department row/relationship/column scope replaces workspace tenancy checks without weakening access.
- Manifest and registry serialization, Operator planner/DDL/Hasura permissions, app publication/removal, app samples and fixtures.
- Identity verification/session presets, DB-backed role authority, File Service/scanner/OCR and UI context/cache changes.
- Optional capability provisioning, least-privilege DB grants and app/capability dependency handling.
- Audit/event/receipt identity, migration of retained records, source hashes/scan state and rollback after new-format writes.

Preserve old data and block conversion until explicit workspace-to-installation/app/department mappings exist. Never collapse multiple organizations into one installation implicitly. Existing accepted-contract text and working code must be labelled as legacy/partial until their coordinated updates pass real integration checks. This documentation-only issue does not complete those follow-ups, install a new schema or establish runtime authorization.

ADR 0006 independently decides complex application business logic. ADR 0007 independently defines RAG resource/source and model-processing policies; neither is implemented or accepted merely by this ADR.
