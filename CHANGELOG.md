# Changelog

All notable changes to **OpenDX-OS (DX-OS Open-Core)** are documented in this
file. Each GitHub release carries the detailed notes; this file is the
repository-level summary for a release.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Changes under `[Unreleased]` move into a dated version section when the release
is published; see [Releasing](docs/contribute/releasing.md).

## [Unreleased]

### Added

- Record history: `GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history`
  returns a fixed audit projection for a record the caller may read, and the
  Workspace UI shows it as a `Lịch sử` section on the detail page. Audit
  payloads and hash-chain columns never leave the server (#41).
- Compose: `make rag-up` starts the stack with cap-ai, the personal library and
  the FTS-only Assistant, creating local secrets and renewing the worker, File
  Service and dispatcher tokens with Keycloak client credentials. The realm
  ships those service clients disabled until `rag-up` enables them, and
  Keycloak's public hostname is fixed with `KEYCLOAK_HOSTNAME` so tokens carry
  one issuer.
- Manifest: tables gain an optional `ui.title`, shown in the portal instead of
  the table name, and an optional `ui.inboxActions`, so a record enters the
  "Chờ tôi xử lý" inbox only for the commands that move it forward rather than
  for optional ones such as cancel or reject. Optional `ui.stateLabels` and
  `ui.actionLabels` name states and actions, so the portal no longer keeps
  application-specific labels. The sample applications declare all four.
- Workspace UI: a "Chờ tôi xử lý" inbox derived from each held role's commands
  and the record states those commands start from, with records merged across
  roles and links to the record detail (#42).
- Manifest and runtime: a `complete_work_item` workflow effect that marks a
  record's open work items `DONE` once per event and step (#42).
- AI: a global Workspace Assistant in `cap-ai` and the Workspace UI that answers
  across the application and personal sources the signed-in user can read, with
  owner-only persistent conversations and protected citations; opt-in Compose
  overlays and APISIX routes run `cap-ai` (#103).
- Kubernetes: `DXApplication` gains `spec.interval`, `spec.suspend`, and an
  on-demand `dxos.io/reconcileAt` annotation; the controller ships as its own
  `opendx-dxos-controller` image with envtest and kind integration checks (#106).
- CI: required checks for pull requests, a per-image build matrix that
  publishes to GHCR on `main` and `v*` tags, a tag-driven release workflow that
  packages and attests the source archive, and Release Drafter for release
  notes (#84, #87, #101).
- Security: Trivy, gitleaks, govulncheck, CodeQL, and grouped Dependabot
  updates (#87).
- AI: optional global model registry foundation for knowledge ingestion (#83).

### Changed

- Procurement: finance no longer receives the manager's review work item; its
  own budget step stays available through the record inbox (#27).
- Workspace UI: My Work and the acting-role switcher show the role's manifest
  description instead of its identifier.
- Workspace UI: record screens format dates, times and numbers by column type,
  and the record detail names a referenced record instead of showing its id
  (#129).
- Procurement: the request form picks a vendor from the application's vendor
  list, labelled "Nhà cung cấp" instead of "Vendor id".
- Demo seed: the five business accounts also hold `role_library_user` when
  `cap-ai` is enabled, so they can open the personal library.
- Docs: one person holding both procurement approval roles may approve both
  steps; role administration keeps the roles apart (#114). The operator is the
  documented control-plane exception to least-privilege database roles (#116).
- The Workspace UI copy and the sample application labels are Vietnamese; table
  headers come from manifest labels, and My Work hides `DONE` items (#45).
- The Kubernetes controller moves from `dxos-operator controller` to a separate
  `dxos-controller` binary (#106).
- PostgreSQL migration SQL is formatted consistently without changing the
  schema (#100).

### Fixed

- Attachments: attaching another user's file now returns a readable refusal
  (SQLSTATE `23514`, `permission-error` in GraphQL) instead of a generic
  "database query error", and a failed `core.files` write deletes the object it
  just stored (#112, #33).
- Commands: when a required attachment is still missing, the portal names the
  field before sending the command instead of showing the not-yet-`CLEAN` 422
  message.
- Compose: the default stack enables the `cap-ai` schema, so the sample
  `personal_library` application installs `Ready` instead of `Failed` (#110).
  The `cap-ai` service itself still runs from the opt-in overlay.
- Workspace UI: the access token is refreshed in memory before the access or
  refresh-token window ends, so a long session keeps its open screens and typed
  forms instead of signing out at expiry (#38).
- CI runs `scripts/test-platform.sh`, which now also asserts the cap-hr and
  cap-work SQL contracts, on a fresh database (#15).

### Security

- Work items are no longer readable app-wide: a role reads one only when it may
  read the source record, and items without a source remain app-wide (#27).
- Procurement roles no longer read every account's email through the shared
  `core.users` table; they see `id` and `display_name` only (#115).
- Keycloak access tokens last five minutes again now that the Workspace UI
  refreshes them, so a leaked token is usable for a shorter time (#38).
  Existing installations set Access Token Lifespan in the admin console.
- The Workspace UI keeps access and refresh tokens in memory only; neither is
  written to `localStorage` or `sessionStorage` (#38).
- `dxos-authz` bootstraps the platform operator only from a token whose
  `email_verified` claim is true.
- The Keycloak realm lets only administrators edit a user's email and name,
  locks an account after repeated wrong passwords, and requires passwords of at
  least eight characters that differ from the username and email (#113).
  Existing installations apply these settings in the admin console.

## [0.2.0] - 2026-11-16

### Added

- One organization per installation with application- and department-scoped
  data, replacing the per-workspace tenant model; `dxos-authz` resolves the
  acting role from `core.user_roles` on every request, operators grant roles,
  and `GET /api/auth/apps` serves the App Shell ([ADR 0005](docs/adr/0005-department-and-application-data-model.md),
  [ADR 0007](docs/adr/0007-keycloak-authenticates-platform-authorizes.md)).
- Operator: multi-table manifests with process, plain, and child tables;
  command payloads; role-scoped Hasura row and column permissions; a directory
  reconcile loop that installs, upgrades, and uninstalls `apps/` packages; and
  deployment of application workflows to Kestra
  ([ADR 0006](docs/adr/0006-where-application-business-logic-lives.md), [ADR 0008](docs/adr/0008-an-application-declares-several-tables.md)).
- Kubernetes: cluster-scoped `DXApplication` CRD (`dxos.io/v1alpha1`), a
  controller-runtime manager with status conditions, Hasura drift repair, and
  `scripts/k8s/dev-cluster.sh` for a local kind cluster
  ([ADR 0004](docs/adr/0004-kubernetes-crd-operator.md)).
- Capabilities: `cap-hr` departments and staff; `cap-ai` native knowledge
  ingestion, scoped retrieval, and governed answers with isolated parser and
  worker processes (#70); `cap-process` on the application-scoped model.
- Applications: cross-department procurement approval, updated maintenance,
  timesheet, and personal-library packages, and the demo organization seed.
- Compose stack that starts PostgreSQL, Hasura, `dxos-authz`, Keycloak, APISIX,
  SeaweedFS, ClamAV, Kestra, the operator, `cap-process`, `cap-hr`, and the App
  Shell with one command.
- Documentation site: MkDocs Material with a Diataxis structure, bilingual
  pages, Algolia DocSearch, and documentation CI.

### Changed

- Keycloak now only authenticates; authorization lives in the platform.
- The data model change has no in-place upgrade: start from fresh volumes and
  reinstall the packages in `apps/`.

### Removed

- The unscoped legacy `/rag` and `/extract` content endpoints.

### Fixed

- The operator no longer reports `Ready` while the live table differs from the
  manifest; additive nullable columns converge with `ALTER TABLE`, and other
  column changes are refused before Hasura or the registry is touched.
- Reconciling an application no longer revokes Hasura permissions that another
  application still uses; global definitions are tracked in
  `core.app_definitions`.
- The operator image builds with Go 1.27.3 and runs as numeric UID 10001 so
  Kubernetes `runAsNonRoot` admits it.

### Security

- Knowledge uploads keep scanner admission, immutable document versions, and
  quarantine; personal content does not reach hosted models by default, and
  model access stays behind policy, budget, and deployment gates.

## [0.1.0] - 2026-11-09

### Added

- Tier 1 core PostgreSQL schemas for workspaces, employees, work items, file
  objects, audit logs, the application registry, and the capability registry.
- `dxos-operator` with `validate`, `plan`, `apply`, and `reconcile`; manifest
  schema validation, DDL generation, Hasura metadata generation, and
  publication to `core.app_registry`.
- APISIX routing, Keycloak OIDC with workspace claims, SeaweedFS object
  storage, and ClamAV scanning.
- `cap-process` with role-separated state transitions, optimistic locking,
  idempotent command replay, and the upload quarantine lifecycle.
- Out-of-tree installer, backup and restore scripts, and the first continuous
  integration workflow.
- Architecture rules, data contracts, the S01–S08 security acceptance matrix,
  and FOSS hygiene rules.

### Security

- Apache-2.0 SPDX headers, LF line endings, tier boundary checks, and file
  quarantine for non-`CLEAN` uploads.

[Unreleased]: https://github.com/nguyentin05/opendx-os/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/nguyentin05/opendx-os/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/nguyentin05/opendx-os/releases/tag/v0.1.0
