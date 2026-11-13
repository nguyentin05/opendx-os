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
3. **`dxos-authz` resolves the role per request.** Hasura runs in
   authentication-webhook mode (`HASURA_GRAPHQL_AUTH_HOOK`). For each request,
   `dxos-authz` verifies the Keycloak token, and accepts the role named in
   `X-Hasura-Role` only if the caller holds it. A caller holding exactly one role
   may omit the header. It returns `X-Hasura-User-Id` and `X-Hasura-Role`, and
   Hasura then applies the role's row filters (own, department, organization).
   Grants are cached per user for `ROLE_CACHE_TTL` (default 5 seconds), so a
   revoked role stops working within that time, without a new token.
   `GET /api/auth/me` (through the gateway) returns the caller's identity and
   roles so the UI can offer a role switcher.
4. **Capability services resolve roles the same way.** A service such as
   cap-process verifies the token and reads `core.user_roles` itself (or asks
   `dxos-authz`); it never trusts roles carried by the request.
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

## Consequences

- Installing, upgrading or removing an application never restarts or
  reconfigures Keycloak.
- Granting or revoking a role takes effect within seconds and is recorded
  (`granted_by`) where the rest of the organization data lives.
- `dxos-authz` sits on the path of every GraphQL request. It is small,
  stateless apart from its short cache, and health-checked; if it is down,
  GraphQL requests fail closed. Each cache miss costs one indexed query.
- Hasura uses a single authentication mode; the JWT mode configuration is
  removed.
- cap-process must stop reading roles from token claims (part of its migration
  to the application-scoped model, issues #29 and #31). Until then its
  authenticated endpoints reject the new tokens.
- Users exist in `core.users` from their first request; the periodic sync is
  needed before an administrator can assign roles to someone who has never
  signed in.

## Alternatives considered

- **Re-import the realm file when an application registers.** This needs a
  restart and loses run-time state. Rejected.
- **Operator syncs roles and assignments to Keycloak through the Admin API.**
  This works without a restart, but keeps two copies that must be
  reconciled, and revocation waits for token expiry. Rejected in favour of a
  single copy.
- **A Keycloak protocol mapper that reads the platform database.** This would
  need a custom Java SPI deployed into Keycloak, and couples Keycloak to the
  platform schema. Rejected.
