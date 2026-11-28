# OpenDX-OS Development Roadmap

[Tiếng Việt](ROADMAP.md) | **English**

This roadmap tracks **OpenDX-OS (DX-OS Open-Core)** by release. Each version's
changes are in [CHANGELOG.md](CHANGELOG.md); open work is tracked in
[GitHub issues](https://github.com/nguyentin05/opendx-os/issues).

---

## v0.1.0: Core platform, operator, and process capability (released 2026-11-09)

- [x] Three-tier boundaries (`platform/`, `operator/`, `capabilities/`, `workspace-ui/`, `apps/`), data contracts, and the `S01`–`S08` acceptance scenarios.
- [x] Open-source metadata, SPDX headers, and tier boundary checks.
- [x] PostgreSQL core schema, Hasura, Keycloak, APISIX, SeaweedFS, and ClamAV.
- [x] `dxos-operator` with `validate`, `plan`, `apply`, and `reconcile`.
- [x] `cap-process`: version-guarded commands, idempotency receipts, and the file quarantine lifecycle.
- [x] Out-of-tree installer, backup and restore scripts, and the first CI workflow.

## v0.2.0: Organization model, Kubernetes operator, and knowledge (released 2026-11-16)

- [x] One organization per installation with application- and department-scoped data ([ADR 0005](docs/adr/0005-department-and-application-data-model.md)).
- [x] Keycloak authenticates and `dxos-authz` authorizes ([ADR 0007](docs/adr/0007-keycloak-authenticates-platform-authorizes.md)).
- [x] Multi-table manifests, command payloads, and Kestra workflow deployment ([ADR 0006](docs/adr/0006-where-application-business-logic-lives.md), [ADR 0008](docs/adr/0008-an-application-declares-several-tables.md)).
- [x] `DXApplication` CRD and controller with Hasura drift repair ([ADR 0004](docs/adr/0004-kubernetes-crd-operator.md)).
- [x] `cap-hr`, and `cap-ai` native knowledge ingestion with scoped retrieval ([ADR 0009](docs/adr/0009-native-rag-identity-and-source-boundaries.md)).
- [x] Cross-department procurement app and demo organization seed.
- [x] Bilingual documentation site.

## v0.3.0: Workspace UI and delivery pipeline (released 2026-11-23)

- [x] Workspace Assistant across application and personal knowledge (#103).
- [x] Controller split into the `dxos-controller` binary and image (#106).
- [x] Required CI checks, security scans, image builds, and an attested release workflow (#84, #87, #101).
- [x] Workspace UI MVP: app discovery, schema-driven lists and forms, record actions, attachments, and My Work (#104).
- [x] Workspace UI follow-ups: audit history (#41).
- [x] Department-scoped work items in My Work (#27).
- [x] In-memory access-token refresh before expiry, without remounting open screens (#38).
- [x] Action inbox derived from record state, with the review work item closed by `complete_work_item` (#42).
- [x] Vietnamese Workspace UI copy and sample application labels (#45).
- [x] Run `cap-ai` from the default Compose stack without manual token setup.
- [x] Documentation matched to the implementation (#46).
- [x] Third-party license inventory and texts, Hasura community edition, and license files in every image (#48).
- [x] Releases start only from a pushed tag (#137).

## v1.0.0: Submission release

- [ ] README quickstart and screenshots (#47).
- [x] `dxos-operator version` reports the build version (#138).
- [x] Documentation and UI build toolchain refresh (#139).
- [x] Durable model operations for knowledge ingestion (#136).

## v1.1.0: Data capability

- [ ] `cap-data` service in the Compose stack: OT ingestion (`S02`), Parquet export (`S07`), and BI lineage (`S08`). The service is not deployed, and its handlers still write the retired `core` tables (`core.ot_events`, `core.devices`, `core.work_items`, `core.export_batches`, `core.data_lineage`) instead of the `cap_data` schema, so they must be ported first (#111).

## Later

- [ ] `cap-work` service and Kanban board (#27, #44).
- [ ] Semantic concept ownership from [ADR 0002](docs/adr/0002-semantic-schema-governance-and-cross-app-data-sync.md).
- [x] Keycloak account sync (#23).
- [ ] Argo CD GitOps example (#28).
- [ ] Audit hash-chain verification anchored outside the database.
