---
title: Security model
description: Identity from Keycloak, authorization from the platform, scopes, and audit.
diataxis: explanation
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Security model

OpenDX-OS separates authentication from authorization. Keycloak authenticates
identity; the platform decides what that identity may do on each request.

## Identity

Keycloak issues OIDC tokens. Tokens carry identity only (`sub`, email, name,
audience) and never create permissions. A user appears in `core.users` on first
request; this is a one-way mirror written only by the identity service and account
sync.

## Authorization

The caller names the role they act as in `X-Hasura-Role`. The role is accepted only
when `core.user_roles` grants it at request time. A role named in a header, token
claim, or request body is a request, never a grant
([ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md)).

Platform operators grant and revoke roles through `dxos-authz`; each change is
audited in the `core` chain. Hasura enforces permissions per table, operation,
role, row, and column. PostgreSQL `GRANT` does not replace Hasura metadata.

## Scope

One installation serves one organization. Isolation is by application and, where
cap-hr is enabled, by department. A role declares a scope per operation:

| Scope | Admits |
| --- | --- |
| `own` | Records the caller created |
| `department` | Records of the caller's department in `cap_hr.staff`; requires cap-hr |
| `organization` | Records across the organization |

An optional `recordFilter` narrows a scope. The server stamps `created_by` and
`org_unit_id`; clients cannot set or change them. See
[identity, roles, and scope](../reference/data-contracts/identity-and-roles.md).

## Integrity

State transitions, version checks, audit rows, events, and idempotency receipts
commit atomically. A stale `expected_version` returns `409` without writing state,
audit, or an event. Audit chains serialize per application and are anchored by
signed checkpoints stored outside PostgreSQL.

The required properties and acceptance scenarios are in
[Security and acceptance](../reference/security/index.md).
