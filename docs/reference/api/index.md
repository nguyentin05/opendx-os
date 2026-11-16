---
title: API reference
description: Capability routes, workflow deployment, and durable events.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# API reference

| Page | Covers |
| --- | --- |
| [Capability routes](capability-routes.md) | Intended HTTP surface of cap-hr, cap-process, cap-data, and cap-ai |
| [Workflow deployment and events](events.md) | Kestra flow admission, event routing, and effect callbacks |

Inspect the actual handlers and agree on request/response schemas before
connecting callers. This inventory includes work still to implement; it is not an
assertion that every endpoint is running.

The machine-readable boundary remains `contracts/runtime-v1.openapi.yaml` and
`contracts/events-v1.schema.json`.
