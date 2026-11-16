---
title: Identity, roles, and scope
description: How identity, role grants, application ownership, and scopes work.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## 1. Identity, roles and scope

- One installation serves one organization. Keycloak authenticates; tokens
  carry identity only (`sub`, email, name). The role a caller acts as is named
  in `X-Hasura-Role` and accepted only when `core.user_roles` grants it now
  ([ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)). Role
  names in a body, header or token never create permissions by themselves.
- Every role is owned by one application, one capability or the platform
  (`core.roles`). An application declares, per role and operation, a **scope**:
  `own` (records the caller created), `department` (records of the caller's
  department in `cap_hr.staff`) or `organization`, plus an optional
  `recordFilter`. Hasura permissions and cap-process commands apply the same
  scope.
- Data carries two independent labels: `app_id` (which application it belongs
  to) and, for applications using cap-hr, `org_unit_id` (which department). Both
  are stamped by the server and never change.
- `created_by` and `org_unit_id` are set by triggers from the session (Hasura's
  `hasura.user` or cap-process's `dxos.actor_id`), never from client input.
- Nested inserts, upserts and relationships must not open an extra path around
  permissions. If permissions for an operation have not been designed, do not
  grant that operation.
