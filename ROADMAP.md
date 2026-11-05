# OpenDX-OS Development Roadmap

This roadmap tracks the phased progression of **OpenDX-OS (DX-OS Open-Core)** from architecture & contract design through full implementation and release verification.

---

## Phase 1: Architecture & Shared Contracts (Design Phase)

- [x] Define 3-Tier Composable Architecture boundaries (`platform/`, `operator/`, `capabilities/`, `workspace-ui/`, `apps/`).
- [x] Define shared data contracts (`core.*` master tables, isolated `domain.*` tables, composite same-workspace foreign keys, server-side version triggers, and command/audit invariants).
- [x] Define mandatory security & business acceptance scenarios (`S01`–`S08`).
- [x] Establish repository FOSS metadata (`LICENSE`, `NOTICE`, `COPYRIGHT.txt`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md`).
- [ ] Settle full base SQL DDL (`platform/postgres/init/01_core_schema.sql`) and versioned `DXApplication` manifest validator/examples.

---

## Phase 2: Core Platform, Operator & Process Capability (`v0.1.0`)

- [ ] **Tier 1 Infrastructure (`platform/`):** PostgreSQL base schema (`core.*`), Hasura metadata templates, Keycloak realm/OIDC mapping, APISIX gateway routes, and Kestra durable event integration.
- [ ] **Control Plane (`operator/`):** Go Operator (`dxos-operator`) implementing `plan`, `apply`, and bidirectional `reconcile` for domain DDL, version triggers, and Hasura permissions.
- [ ] **Process Capability (`capabilities/cap-process`):** Version-guarded state transitions (`submit`, `approve`, `reject`, `cancel`, `close`) with command idempotency receipts and `ClamAV` file quarantine lifecycle (`QUARANTINED -> CLEAN | INFECTED`).
- [ ] **Verification:** Automated tests for `S01` (Workspace & role isolation), `S03` (Workflow/command idempotency), `S04` (Stale version & concurrency), `S05` (Independent cross-app state), and `S06` (File quarantine).

---

## Phase 3: Data/BI, AI Capabilities, Dynamic UI Shell & Apps (`v0.2.0`)

- [ ] **Data Capability (`capabilities/cap-data`):** Authenticated OT event ingestion (`ot.event.accepted`) with deduplication (`S02`), reconciled Parquet batch export (`S07`), and workspace-scoped DuckDB BI with full source lineage (`S08`).
- [ ] **AI Capability (`capabilities/cap-ai`):** Schema-driven OCR extraction on `CLEAN` files and scoped RAG policy Q&A with source citations.
- [ ] **Dynamic App Shell (`workspace-ui/`):** Schema-driven portal rendering H-P-D-I spaces and registered apps from `core.app_registry` and `core.capability_registry`.
- [ ] **Declarative Domain Apps (`apps/`):** Package `maintenance`, `procurement`, and `timesheet` manifests, workflows, and knowledge documents; verify hot-plugging with a fourth test app.

---

## Phase 4: Hardening, GitOps & Release Packaging (`v1.0.0`)

- [ ] **Audit & Checkpoints:** Serialized per-workspace SHA-256 hash-chain verification anchored in OpenBao (`verify-audit`).
- [ ] **GitOps & Out-of-Tree Installation:** Kubernetes/Argo CD overlays (`gitops/`), `scripts/check_pof.py`, `scripts/install.sh`, and `scripts/package_release.sh`.
- [ ] **Release Gate:** Full `S01`–`S08` passing evidence on the release revision, CycloneDX SBOMs (`sbom/*.cdx.json`), and verified out-of-tree installation from `dxos-open-core-1.0.0.tar.gz`.
