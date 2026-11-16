---
title: Reference
description: Canonical contracts for the manifest, data model, security, API, CLI, and RAG.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Reference

Reference pages describe required behavior. When a page and the implementation
disagree, treat the page as the requirement and report the gap rather than
copying the implementation into the contract.

| Area | Covers |
| --- | --- |
| [Data contracts](data-contracts/index.md) | Identity, roles, tables, writes, versions, files, OT, BI |
| [Manifest](manifest/index.md) | `DXApplication` fields, installation and upgrade lifecycle |
| [Security and acceptance](security/index.md) | Invariants, protocol responses, S01–S08 |
| [API](api/index.md) | Capability routes, command boundary, events |
| [CLI](cli/dxos-operator.md) | `dxos-operator` commands |
| [RAG](rag/index.md) | Knowledge API, source authorization, model gateway |
| [Runtime rules](runtime-rules.md) | The subset services re-check before acting |

Machine-readable files remain at the repository root under `contracts/`.
