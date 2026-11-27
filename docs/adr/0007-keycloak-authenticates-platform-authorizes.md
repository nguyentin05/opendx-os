---
title: Keycloak authenticates, the platform authorizes
description: Identity from Keycloak and effective permissions from core.user_roles.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-13
---

# ADR 0007: Keycloak Authenticates, the Platform Authorizes

- **Status:** Accepted
- **Date:** 2026-11-13

## Context

Roles are owned by applications and capabilities (`core.roles`, written by the
operator), and who holds which role is recorded in `core.user_roles`, assigned
within a department by people using the platform. Hasura and cap-process,
however, took the caller's roles from the access token
(`x-hasura-allowed-roles`, filled from a free-form Keycloak user attribute).
Permissions therefore lived in two places with nothing keeping them equal.

Keeping Keycloak in step would mean the operator creates every application role
in Keycloak and pushes every assignment there. Doing that by re-importing the
realm file restarts Keycloak (about 20 seconds without login or token refresh)
and, when overwriting, can drop users and signing keys. Doing it through the
Keycloak Admin API avoids the restart but still keeps two copies, and a revoked
role keeps working until the caller's token expires.

### How Hasura learns the caller's role

Hasura applies a role's row and column permissions, but it does not decide who
holds a role. It learns the caller's user id and role in one of two ways:

| Hasura mode | Where the role comes from |
| --- | --- |
| JWT (`HASURA_GRAPHQL_JWT_SECRET`) | claims inside the signed token (`x-hasura-allowed-roles`, `x-hasura-default-role`) |
| Webhook (`HASURA_GRAPHQL_AUTH_HOOK`) | an HTTP endpoint Hasura calls with the client's headers; it answers `X-Hasura-User-Id` and `X-Hasura-Role`, or 401 to deny |

If roles are not in the token, JWT mode cannot be used, and the platform must
provide the endpoint the webhook calls.

## Decision

1. **Keycloak only authenticates.** It manages accounts, passwords, SSO, MFA
   and PKCE, and issues access tokens carrying identity only: `sub`, email and
   name, with audience `dxos-runtime`. Tokens carry no application roles. The
   realm file (`platform/keycloak/realm-export.json`) bootstraps the realm and
   its single public client and is imported only into an empty store; it never
   lists application roles, and installing an application never touches
   Keycloak.
2. **The platform authorizes.** `core.user_roles` is the only record of who
   holds which role. A role is in force when it is a platform role, a role of a
   `Ready` application, or a role of an enabled capability.
3. **`dxos-authz` answers Hasura's webhook.** Hasura runs in webhook mode. For
   each request, `dxos-authz` verifies the Keycloak token against the realm's
   published keys, and accepts the role named in `X-Hasura-Role` only if the
   caller holds it. A caller holding exactly one role may omit the header. It
   returns `X-Hasura-User-Id` and `X-Hasura-Role`, and Hasura then applies that
   role's row filters (own, department, organization) and column permissions.
   Grants are cached per user for `ROLE_CACHE_TTL` (default 5 seconds), so a
   revoked role stops working within that time, without a new token.
   `GET /api/auth/me` (through the gateway) returns the caller's identity and
   roles so the UI can offer a role switcher.
4. **Capability services resolve roles with the same code.** A service such as
   cap-process links the package `dxos-authz` is built from and reads
   `core.user_roles` itself over its own connection; it does not call
   `dxos-authz` over HTTP, and it never trusts roles carried by the request.
   Its connection may not write `core.users`, so it only reads the mirror (item
   5): a user is known once `dxos-authz` has seen one request from them, which
   the UI's `/api/auth/me` call at sign-in provides.
5. **`core.users` is a one-way mirror of Keycloak identity.** Its `id` is the
   token subject. `dxos-authz` inserts the row on a user's first request and
   updates email and display name when the token shows a change; it never
   changes `status`. Keycloak is the only writer of identity columns:
   - nothing else edits email or display name;
   - an account is disabled in Keycloak, and the mirror marks it `DISABLED`;
   - a user deleted in Keycloak is marked `DISABLED`, never deleted, because
     records and audit entries refer to it.

   Departments (`cap_hr.staff`) and roles stay in the platform and are never
   written from Keycloak. Mirroring disabled accounts (and listing users who
   have never signed in, so they can be given a department and roles) is a
   periodic sync from the Keycloak Admin API with a read-only service account.
6. **Least privilege.** `dxos-authz` connects as `dxos_authz`, which may read
   the registry and grants and write only `core.users.email` and
   `display_name`. Scripted password logins are disabled on the shipped client
   (`directAccessGrantsEnabled: false`); tests create their own client through
   the Admin API.

7. **The workflow engine is a service caller, not a user.** It has no Keycloak
   login. cap-process admits its effect calls by the workflow service
   credential (`WORKFLOW_SERVICE_TOKEN`) and lets it act as one platform
   account holding `role_workflow`. That account is seeded by the platform
   schema and is the only row of `core.users` that does not mirror a Keycloak
   identity. The credential reaches only the effect endpoints, which the gateway
   does not route, and an effect is limited to what installed applications
   declare ([workflow deployment](../reference/api/events.md#workflow-deployment)).

8. **Operators assign roles through `dxos-authz`.** A user holding the platform
   role `role_operator` lists accounts and roles and grants or revokes a role
   (`/api/auth/users`, `/api/auth/roles`,
   `/api/auth/users/{id}/roles/{role}`). `dxos-authz` still has no write access
   to `core.user_roles`: it calls `core.grant_role` and `core.revoke_role`,
   which make the change and its audit row in the `core` chain in one
   transaction and refuse any other connection. `role_workflow` is never
   assigned to a person, and an operator cannot revoke their own
   `role_operator`. A new installation has no operator, so the account named by
   `BOOTSTRAP_OPERATOR_EMAIL` becomes one on its first sign-in, once, while
   nobody holds the role. The token must carry `email_verified: true`, and
   users cannot edit their own email or name in Keycloak.

## Where `dxos-authz` sits

`dxos-authz` is Tier 1 platform infrastructure, next to Keycloak, APISIX and
Hasura. It is not a capability: it has no domain behaviour, every application
depends on it whether or not it uses any capability, and it must run even when
every capability is disabled.

```
Before:  UI --token with roles--> APISIX --/v1/graphql--> Hasura   (role read from the token)

After:   UI --identity token--> APISIX --/v1/graphql--> Hasura --webhook--> dxos-authz --> core.user_roles
         UI --identity token--> APISIX --/api/auth/me------------------> dxos-authz
         UI --identity token--> APISIX --/api/cap/process/*--> cap-process (same package, linked in) --> core.user_roles
```

Keycloak says who the caller is; `dxos-authz` says which of the caller's roles
applies to this request; Hasura and cap-process enforce what that role may see
and do.

Its source currently lives in the `capabilities/` Go module
(`cmd/dxos-authz`, `internal/authz`, with token verification in
`internal/auth`), only so that cap-process and `dxos-authz` share one
implementation without a new module. This is a packaging shortcut, not a tier
assignment; moving both packages to a platform module that capability services
import is a follow-up that changes no behaviour.

## Consequences

- Installing, upgrading or removing an application never restarts or
  reconfigures Keycloak.
- Granting or revoking a role takes effect within seconds and is recorded
  (`granted_by`) where the rest of the organization data lives.
- `dxos-authz` sits on the path of every GraphQL request. It is small,
  stateless apart from its short cache, and health-checked; if it is down or
  cannot reach the database, GraphQL requests fail closed. Each request costs
  one local HTTP call; each cache miss adds one indexed query.
- Hasura calls the webhook when a subscription connection starts. Its answer
  carries `Cache-Control: max-age=60`, so Hasura closes the WebSocket after a
  minute and the client authorizes again: a revoked role or a disabled account
  loses an open subscription within that time.
- Hasura uses a single authentication mode; the JWT mode configuration is
  removed.
- cap-process must stop reading roles from token claims, as part of its
  migration to the application-scoped model (issues #29 and #31). Until then
  its authenticated endpoints reject the new tokens.
- Users exist in `core.users` from their first request. With the read-only
  Keycloak client `dxos-directory` (`view-users` only), `dxos-authz` also mirrors
  the realm's people every few minutes and when an operator lists accounts, so
  roles and a department can be given before a first sign-in (#23, #188). An
  account disabled or deleted in Keycloak becomes `DISABLED` in `core.users`
  (never deleted) and is refused; only a complete read disables anyone, and the
  platform's own service accounts never change. People are still created only
  in Keycloak, or in the directory it federates; the platform never writes there.

## Alternatives considered

- **Re-import the realm file when an application registers.** This needs a
  restart and loses run-time state. Rejected.
- **Operator syncs roles and assignments to Keycloak through the Admin API,
  keeping Hasura in JWT mode.** This works without a restart, but keeps two
  copies that must be reconciled, and revocation waits for token expiry.
  Rejected in favour of a single copy.
- **A Keycloak protocol mapper that reads the platform database.** This would
  need a custom Java SPI deployed into Keycloak, and couples Keycloak to the
  platform schema. Rejected.
- **Serve the webhook from cap-process.** This saves a container, but every
  GraphQL request of every application would then depend on a Tier 2
  capability, including applications that use no workflow. Authorization
  belongs to the platform. Rejected.
- **A gateway plugin that looks up roles and injects Hasura headers.** APISIX
  would need database access and security logic written as a Lua plugin, and
  Hasura would have to trust headers set upstream instead of asking itself.
  This is harder to test and to reason about. Rejected.
- **One shared Hasura role, with access decided by row filters alone.** Hasura
  permissions are per role; collapsing roles would lose per-role column
  permissions and turn every filter into a lookup of the caller's grants.
  Rejected.
