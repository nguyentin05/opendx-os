---
title: Manifest reference
description: DXApplication fields, table declarations, installation, and upgrade lifecycle.
diataxis: reference
audience: [app-author, platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Manifest reference

A `DXApplication` manifest declares an application's capabilities, roles, shared
access, domain tables, workflows, and optional knowledge sources. The operator's
validator (`operator/internal/manifest`) is the reference implementation of this
serialization; `apps/*/manifest.yaml` are validated samples.

| Page | Covers |
| --- | --- |
| [Schema](schema.md) | Top-level fields, `spec.tables`, access rules, and state machines |
| [Lifecycle](lifecycle.md) | Installation, upgrade rules, registry rows, and runtime consumers |

The machine-readable schema is `contracts/manifest-v1.schema.json` in the
repository. Schema shape alone does not establish a manifest's semantic safety.
