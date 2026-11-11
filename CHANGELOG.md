# Changelog

All notable changes to **OpenDX-OS (DX-OS Open-Core)** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Operator: an upgraded manifest no longer publishes `Ready` while the live table differs. Adding a nullable column is converged with `ALTER TABLE`; any other column change is refused before Hasura or the registry is touched, and the deployment attempt records the reason.
- Operator: reconciling or upgrading an application in one workspace no longer revokes Hasura permissions that another workspace still uses. Global definitions are tracked in the new `core.app_definitions` table (`platform/postgres/init/07_app_definitions.sql`), separately from per-workspace publication in `core.app_registry`.

### Added
- Operator: cluster-scoped `DXApplication` CRD (`dxos.io/v1alpha1`) with generated schema validation, status subresource and printer columns; `dxos-operator controller` runs a controller-runtime manager (leader election, health probes); `operator/config/` kustomize bases for CRD, RBAC and the manager Deployment; `make -C operator manifests generate test-envtest` ([ADR 0004](docs/adr/0004-kubernetes-crd-operator.md)).
- Operator: `dxos-operator export-crd [-workspace UUID[,UUID]] <app-dir>` converts an application package into a `DXApplication` resource (workflow definitions inlined, `_` in app IDs mapped to `-` in resource names); `make -C operator samples` regenerates `operator/config/samples/`.
- Operator: the `DXApplication` controller reconciles resources end to end: validates the spec (`Valid`), plans against all active definitions so other applications keep their grants, converges or refuses schema changes (`SchemaInSync`), applies DDL and Hasura metadata (`PermissionsInSync`), publishes the listed workspaces (`Published`, per-workspace status) and reports `Ready` with `observedGeneration`; a finalizer revokes grants and registry rows on deletion while retaining domain data. `make -C operator test-controller` runs the lifecycle test on envtest plus PostgreSQL.
- Architectural rules, data contracts, security acceptance matrix (`S01`–`S08`), and FOSS hygiene rules under `.agents/` and `AGENTS.md`.
- Three-tier composable architecture specification separating Tier 1 Headless PaaS (`platform/`, `operator/`), Tier 2 H-P-D-I capabilities and dynamic UI shell (`capabilities/`, `workspace-ui/`), and Tier 3 declarative domain packages (`apps/`).
- Foundational open-source distribution metadata (`LICENSE`, `LICENSES/LICENSE`, `NOTICE`, `COPYRIGHT.txt`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `.editorconfig`, `.gitattributes`, `.gitignore`, and root `Makefile`).
