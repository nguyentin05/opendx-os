# Local identity and runtime authority

This is the disposable integration setup, not production deployment guidance.
Use the guarded environment from `scripts/integration/environment.py`; it targets
only Compose project `opendx-integration` and database `opendx_integration`.

After infrastructure startup, run:

```sh
TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/environment.py roles
python3 scripts/integration/identity.py provision
python3 scripts/integration/identity.py tokens
python3 -m unittest tests.integration.identity_cases -v
```

The roles command must run after PostgreSQL initialization and before starting
Hasura/process. It generates private runtime passwords in `.env.integration` and
host DSNs in `build/integration/connections.json` (mode 0600). `bootstrap` is for
Operator/schema administration; `module_test` separately permits disposable
CREATE DATABASE. Use `assertion` for read-only observations, `process` for the
trusted process/scanner backend, and `hasura` for Hasura. `caller` has schema
usage but no business DML grants. Runtime roles have neither superuser nor
BYPASSRLS, cannot create roles/databases, and share no memberships. The module
runner role has CREATEDB but no superuser or BYPASSRLS.

The base Compose file also uses `dxos_process` and `dxos_hasura`, with explicitly
provisioned `PROCESS_DATABASE_PASSWORD` and `HASURA_DATABASE_PASSWORD`; it no
longer defaults either runtime connection to the bootstrap account. The
integration override uses the generated `INTEGRATION_*` values instead.

`06_runtime_roles.sql` runs after enabled capability schemas. It grants process
access to registry reads, receipts, outbox, work items and file service storage.
Only that trusted backend connection can update file scan result columns; the
scanner currently runs inside cap-process, not a separate daemon. Hasura cannot
mark files CLEAN. Task 4 must grant generated domain resources to the relevant
connections and install their actual Hasura permissions. No broad future-domain
grants substitute for that work.

Audit accepts `dxos.actor_id`/`dxos.action` only from `dxos_process` and
`hasura.user` only from `dxos_hasura`, checked by `session_user`. The trigger uses
SECURITY DEFINER to append audit without giving ordinary callers audit INSERT.
Its fixed search path includes only pg_catalog/core/public; public schema CREATE
is revoked. Business callers cannot become either trusted connection by setting
custom PostgreSQL variables or selecting a role. Backend compromise or disclosure
of a trusted connection credential is outside this boundary.

The real Keycloak issuer and browser login URL are
`http://localhost:18085/realms/dxos` by default. The portal client `dxos-portal`
allows the integration browser origin on port 15173 and uses the runtime audience
`dxos-runtime`. Internal JWKS is
`http://keycloak:8085/realms/dxos/protocol/openid-connect/certs`; the public issuer
stays the browser URL even though containers fetch keys internally. Host and
gateway JWKS reachability are tested separately. Use the canonical direct issuer
for browser authorization/token acquisition; gateway forwarding does not define
a second accepted issuer. Local port overrides come from `.env.integration`.

Provisioning reads `tests/fixtures/actors.json` and assigns its exact UUID subjects,
workspace attributes and signed role grants. The escaped literal Hasura namespace
is significant in Keycloak mapper configuration. `X-Hasura-Role` selects one of
those signed grants; it cannot add authority. Gateway strips privileged admin and
identity headers while retaining the signed token and role selection.

`build/integration/tokens.json` stores the fixture's `TEST_*_TOKEN` strings.
`token-expiry.json` records `expires_at` and `renew_before` per key. Before
`renew_before` (60 seconds before expiration), rerun `identity.py tokens` and
reload the new token into the caller. Workflow runners must perform this renewal
before starting or resuming execution and refresh their supplied credential;
these files are not permanent bearer credentials. Workflow effect identity is
`22222222-2222-4222-8222-000000000005`, selected role `role_workflow`. Workflow
publisher authentication is a separate credential from effect caller tokens.
All credentials and issued tokens stay private and ignored by Git.

The identity suite uses a minimal `domain.identity_boundary` SQL/Hasura fixture
for an authenticated read and forbidden writes. It does not qualify generated
procurement/process_fixture resources, business transitions or file lifecycle.
It may temporarily shorten the disposable realm token lifespan to two seconds
for the expiry test and restores the prior value in a `finally` block.
