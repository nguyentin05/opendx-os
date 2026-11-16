---
title: Capability routes
description: The intended HTTP surface of cap-hr, cap-process, cap-data, and cap-ai.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## 9. Capability interfaces

| Capability | Intended interface |
| --- | --- |
| `cap-hr` | `GET /api/cap/hr/me`, `GET /api/cap/hr/org-units`, `GET /api/cap/hr/staff`; for `role_hr_admin`: `POST /api/cap/hr/org-units`, `PATCH /api/cap/hr/org-units/{id}`, `PUT` and `DELETE /api/cap/hr/staff/{user_id}` ([UI integration](../../guides/integrating-the-ui.md#8-administration)) |
| `cap-process` | `POST /api/cap/process/files/upload`, `POST /api/cap/process/commands/{app_id}/{command}` |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export`, `GET /api/cap/data/lakehouse/analytics` |
| `cap-ai` | `POST /api/cap/ai/extract`, `POST /api/cap/ai/rag` |

Inspect actual handlers and agree on request/response schemas and service
identity before connecting callers. This logical route inventory includes work
still to implement; it is not an assertion that every endpoint is running.
