---
title: Data contracts
description: Identity, roles, shared and domain tables, writes, versions, files, OT, and BI.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Data contracts

These pages are the canonical data contract shared by the Operator, capabilities,
UI, and application packages. The implementation is partial; existing Go types and
sample YAML are not evidence that every requirement is enforced. See
[architecture and contract changes](../../architecture/index.md#decisions-and-contract-changes)
and [security acceptance criteria](../security/index.md).

| Page | Covers |
| --- | --- |
| [Identity, roles, and scope](identity-and-roles.md) | One organization per installation, role ownership, and `own`/`department`/`organization` scopes |
| [Shared tables](shared-tables.md) | `core.*` tables and capability-owned storage |
| [Domain tables](domain-tables.md) | Process, plain, and child tables, plus sample application tables |
| [Writes, versions, and audit](writes-and-versions.md) | Field ownership, optimistic concurrency, command receipts, and audit chains |
| [Files, OT, and BI](files-ot-bi.md) | Quarantine, OT deduplication, export reconciliation, and lineage |
| [Semantic concepts](semantic-concepts.md) | Shared concepts, write owners, and replicas |

The manifest contract is under [Manifest](../manifest/index.md).
