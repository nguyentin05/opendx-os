# Contributing to OpenDX-OS

Thank you for contributing to **OpenDX-OS (DX-OS Open-Core)**. This document defines the architectural boundaries, code hygiene rules, and verification requirements for all contributions.

---

## 1. Architectural Boundaries & Ownership

OpenDX-OS enforces a strict **3-Tier Composable Architecture**:

| Tier | Directories | Scope & Rules |
| :--- | :--- | :--- |
| **Tier 1 — Headless PaaS & Control Plane** | `platform/`, `operator/`, `gitops/`, `scripts/` | Shared infrastructure (`PostgreSQL`, `Hasura`, `Keycloak`, `APISIX`, `Kestra`, `SeaweedFS + ClamAV`, `OpenBao`) and the out-of-band Go Operator (`dxos-operator`). Must remain 100% domain-agnostic. |
| **Tier 2 — H-P-D-I Capabilities & App Shell** | `capabilities/`, `workspace-ui/` | Pluggable capability services (`cap-process`, `cap-data`, `cap-ai`) and the schema-driven UI shell. Must operate via registry metadata without hardcoding domain app names. |
| **Tier 3 — Declarative Domain Apps** | `apps/<app>/` | Self-contained domain packages containing `manifest.yaml`, `workflows/*.yaml`, and `knowledge/*.md`. |

When modifying a shared contract (such as `core.*` tables or the `DXApplication` manifest schema), update both the contract definition and all affected producers/consumers.

---

## 2. Source Hygiene & Licensing (`Apache-2.0`)

1. **License:** All original contributions are licensed under the **Apache License, Version 2.0** (see [`LICENSE`](./LICENSE)).
2. **SPDX Headers by File Type:**
   - `.go`, `.ts`, `.tsx`:
     ```go
     // SPDX-License-Identifier: Apache-2.0
     // Copyright 2026 DX-OS Open-Core Contributors
     ```
   - `.py`, `.sh` (after any `#!` shebang or encoding line) and `.yaml`, `.yml`, `Makefile`, `Dockerfile`:
     ```python
     # SPDX-License-Identifier: Apache-2.0
     # Copyright 2026 DX-OS Open-Core Contributors
     ```
   - `.sql`:
     ```sql
     -- SPDX-License-Identifier: Apache-2.0
     -- Copyright 2026 DX-OS Open-Core Contributors
     ```
   - JSON, lockfiles, Markdown, `SKILL.md` (with YAML frontmatter), and binary assets do not take inline license headers.
3. **Line Endings & Dependencies:**
   - All text files must use **UTF-8** and **LF (`\n`)** line endings.
   - Never vendor or commit third-party dependency directories (`node_modules/`, `vendor/`, `.venv/`).

---

## 3. Development & Pull Request Workflow

1. **Issue & Branch:** Open an issue describing the change and create a branch named `<type>/<issue-id>-<short-slug>` (e.g., `feat/1-core-schema`, `fix/4-stale-version-check`).
2. **Conventional Commits:** Write clear, imperative commit messages (`feat(operator): reconcile excess Hasura column permissions`).
3. **Verification Evidence:** Run the applicable checks (`make check-hygiene`, module unit/integration tests, and affected acceptance scenarios `S01`–`S08`) and report exact statuses (`PASS`, `FAIL`, `NOT_RUN`, `NOT_APPLICABLE`) in the Pull Request description.
