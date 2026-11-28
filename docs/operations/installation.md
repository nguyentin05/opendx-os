---
title: Installation
description: Install OpenDX-OS on a workstation or a development cluster.
diataxis: how-to
audience: [operator]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# OpenDX-OS Installation Guide

This page installs OpenDX-OS with Docker Compose, from a release archive or a
checkout, and on a development Kubernetes cluster. A local stack is disposable:
PostgreSQL provisions the restricted runtime roles before Hasura and the
capability services start, and Kestra waits until its own database exists.
Record evidence with [Verification](../contribute/verification.md).

The Compose files:

| File | Adds |
| --- | --- |
| `compose.yaml` | PostgreSQL, Hasura, `dxos-authz`, Keycloak, SeaweedFS, ClamAV, Kestra, the operator, cap-process, cap-hr, APISIX and the Workspace UI |
| `compose.rag.yaml` | Python AI API and worker, one-shot Alembic, PostgreSQL with pgvector and PGMQ, and the Go publication dispatcher. Start with [the AI installation steps](../guides/knowledge/models.md#compose-installation). |
| `compose.integration.yaml` | Fixtures for the integration test suite only |

## Prerequisites

- Docker Engine & Docker Compose v2
- Go 1.24+ (with `GOTOOLCHAIN=auto` for Go 1.27.4 module builds)
- Node.js 22+ and npm (for building `workspace-ui`)
- Python 3.12+ with `PyYAML` (for contract verification and demo seeding)
- `postgresql-client` (`psql`, `pg_dump`) for database operations

## Out-of-Tree Archive Installation

1. Build the release archive:
   ```bash
   ./scripts/package_release.sh 0.3.0
   (cd dist && sha256sum -c dxos-open-core-0.3.0.tar.gz.sha256)
   ```

2. Extract the archive outside the Git repository and run the installer:
   ```bash
   tar -xzf dist/dxos-open-core-0.3.0.tar.gz -C /tmp
   /tmp/dxos-open-core-0.3.0/scripts/install.sh /opt/opendx-os
   ```

3. Configure environment variables and start services:
   ```bash
   cd /opt/opendx-os
   scripts/init-env.sh      # .env with random secrets
   docker compose up -d
   scripts/test-compose.sh
   ```
   At first start PostgreSQL loads the capabilities listed in
   `ENABLED_CAPABILITIES` (default `cap-process,cap-hr,cap-work,cap-data,cap-ai`, which
   the sample apps need), sets the runtime role passwords
   (`HASURA_DATABASE_PASSWORD`, `AUTHZ_DATABASE_PASSWORD`,
   `PROCESS_DATABASE_PASSWORD`, `HR_DATABASE_PASSWORD`, and the `AI_*` ones for
   cap-ai) and creates Kestra's own `dxos_kestra` database
   (`KESTRA_DATABASE_PASSWORD`), all in
   `platform/postgres/init/08_runtime_passwords.sh`. This happens once per data
   volume: after changing these values, recreate it with `docker compose down -v`.
   `scripts/test-compose.sh` waits for every service, logs in as each runtime role
   from another container and probes each HTTP entry point. It expects every
   package in `apps/` to be Ready, except a package that declares a capability
   `ENABLED_CAPABILITIES` leaves off, which the operator refuses to install.

   For AI, start with `make ai-up` on a fresh named Compose project. It creates
   service credentials in `.env` (set `AI_ENV_FILE` to another path to keep them
   in a separate file), initializes PostgreSQL and enables the
   Personal Library after its application is Ready. Configure an LLM in
   Workspace AI Management to enable generated answers.
   [AI installation and setup](../guides/knowledge/models.md).

   Sign-in uses Keycloak (realm `dxos`, client `dxos-portal`, browser flow with
   PKCE). Create accounts in the Keycloak admin console
   (`http://localhost:8085/admin`, `KEYCLOAK_ADMIN`/`KEYCLOAK_ADMIN_PASSWORD`), or
   in the directory Keycloak federates. A user appears in `core.users` on their
   first request; with `KEYCLOAK_DIRECTORY_SECRET` set (`scripts/init-env.sh` sets
   it), they appear as soon as an operator lists accounts, so roles and a
   department can be given before their first sign-in, and an account disabled
   or deleted in Keycloak is refused within minutes (`ACCOUNT_SYNC_INTERVAL`,
   default `5m`). An existing database needs the core migration 0008 for that:
   `scripts/migrate-core.sh --database-url-env <VAR> --through 0008`,
   with `<VAR>` naming a variable that holds the administrator's connection URL.
   The one-shot service
   `keycloak-directory` sets up the Keycloak client `dxos-directory` for this; it
   may only read the users of realm `dxos`. Set
   `BOOTSTRAP_OPERATOR_EMAIL` to the email of the account that should administer
   the installation, and mark that email verified in Keycloak: its first sign-in
   makes it the platform operator (`role_operator`), once, while nobody holds
   that role. Users cannot change their own email or name, and the realm locks
   an account for a while after five wrong passwords. Keycloak imports
   `realm-export.json` only into a new `keycloak-data` volume, so an existing
   installation applies these realm settings in the admin console. Operators then grant
   and revoke roles through `/api/auth/users/{id}/roles/{role}`
   ([integrating the UI](../guides/integrating-the-ui.md#8-administration)),
   and users holding `role_hr_admin` maintain departments and staff through
   `/api/cap/hr`. Roles take
   effect within `ROLE_CACHE_TTL` (default `5s`), with no new token
   ([ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md)).

   The Workspace UI sends a `Content-Security-Policy` that lets the browser call
   only its own origin and the Keycloak origin in `DXOS_OIDC_ORIGIN` (default
   `http://localhost:8085`), and signs in with the realm in `OIDC_ISSUER_URL`,
   which it reads from `/config.js` at start-up. To serve the platform on other
   addresses, set these in the environment of `docker compose` (or as container
   environment variables when you run the images elsewhere), for example:

   ```bash
   KEYCLOAK_HOSTNAME=https://id.example.org
   OIDC_ISSUER_URL=https://id.example.org/realms/dxos
   DXOS_OIDC_ORIGIN=https://id.example.org
   DXOS_APP_URL=https://app.example.org
   ```

   `DXOS_APP_URL` is the Workspace UI address that the realm import accepts as a
   sign-in redirect; like the other realm settings it only applies to a new
   `keycloak-data` volume. Browsers sign in only on HTTPS or `localhost`.

   For a demo organization, run `scripts/seed-demo.py seed`. It creates three
   departments and six Keycloak accounts (password `DEMO_PASSWORD`, default
   `Demo@2026`) with procurement, maintenance and timesheet roles (and the
   personal library role when `cap-ai` is enabled), and is safe to re-run. `scripts/seed-demo.py scopes` then files sample purchase requests (each
   with a quote uploaded to cap-process and scanned by ClamAV) and prints what each
   person sees through the gateway; `scripts/seed-demo.py flow` takes one request
   through submit, department approval and budget approval with cap-process
   commands, including the refusals on the way; `scripts/seed-demo.py admin` has
   the operator grant and revoke a role and shows what the user may do before
   and after; `scripts/seed-demo.py org` has an HR administrator add a
   department and move someone into it; `scripts/seed-demo.py lines` files
   a request with a vendor from the application's vendor list and line items, and
   shows who may read and change them before and after it is submitted. Use them
   on local stacks only.

4. The `operator` service installs exactly the application packages in
   `OPERATOR_APPS_DIR` (default `./apps`, mounted read-only), checking every
   `OPERATOR_RECONCILE_INTERVAL` (default `30s`). It runs
   `dxos-operator reconcile --dir /apps --interval 30s`, the same planner and
   apply code as the Kubernetes controller. No container needs recreating:
   - copy a package directory in to install it; edit it to upgrade;
   - remove it to uninstall: the `core.apps` row becomes `Removed`, its Hasura
     permissions and flows are dropped, its packaged knowledge is retired, and
     the domain tables, data and roles are kept;
   - Hasura metadata changed by hand is repaired on the next pass.

   The operator also deploys each application's workflows to Kestra and keeps
   them there: a flow deleted or edited in Kestra is put back on the next pass,
   and cap-process starts sending an application's events to its flow as soon
   as it is deployed. Nothing is uploaded by hand
   ([workflow deployment](../reference/api/events.md#workflow-deployment)).

   Each new or changed package is installed on its own, so a refused one (for
   example, a capability that is not enabled) does not hold back the others; it
   is retried when its package changes, or after ten minutes. While any package
   is invalid (for instance half-copied), nothing is uninstalled. Follow it with
   `docker compose logs -f operator`.

   Without Compose, build the binaries (`make -C operator build` writes
   `operator/bin/dxos-operator` and `operator/bin/dxos-controller`). The build
   stamps the operator version from `git describe --tags --always --dirty`
   (falling back to `dev`), so `operator/bin/dxos-operator version` reports the
   built revision. Then validate and apply application manifests with your own
   `.env` values:
   ```bash
   operator/bin/dxos-operator validate apps/procurement/manifest.yaml
   HASURA_URL=http://localhost:8080 HASURA_ADMIN_SECRET=<HASURA_ADMIN_SECRET> \
   DATABASE_URL='postgres://<POSTGRES_USER>:<POSTGRES_PASSWORD>@localhost:5432/<POSTGRES_DB>?sslmode=disable' \
   operator/bin/dxos-operator apply apps/procurement/manifest.yaml
   ```

## Rebuilding an existing model installation

The Alembic deployment command checks the legacy AI migration ledger and schema
before creating any AI objects. It rejects a Go AI database with a clear error.
Keep the original installation and recovery backup available during cutover.

1. Identify the database, Compose project, private object bucket, and deployment
   identities. Coordinate any rebuild with the installation owner.
2. Create a fresh named target. Preserve the Core schema and migration ledger,
   application authority, private file metadata and original object bytes with
   the installation's reviewed restore procedure.
3. Run the AI deployment against the fresh target. For a rebuild in place,
   prepare a separate DBA-reviewed procedure for retiring only the legacy
   `cap_ai` schema and its `cap-ai` ledger entries after taking a verified backup.
   No startup or installation command removes them.
4. Republish application packages and register private originals again under
   their current owners. Reissue document grants and review every hosted policy
   for the new model revision. Old AI UUIDs, citations and conversations stop
   resolving. Originals protected by Core stay in their existing storage.
5. Check health, ingestion, current role access, and model admission before
   routing user traffic to the target.

The [AI deployment guide](../guides/knowledge/models.md) lists the bootstrap
inputs. [ADR 0011](../adr/0011-python-intelligence-runtime.md) records cutover
ownership and the parser deployment limitation.

## Kubernetes Operator (development cluster)

The `dxos-controller` binary (`operator/cmd/dxos-controller`) runs the operator
as a Kubernetes controller for the cluster-scoped `DXApplication` resource
([ADR 0004](../adr/0004-kubernetes-crd-operator.md)). The `dxos-operator` CLI
above keeps working without a cluster.

1. Create a disposable kind cluster with PostgreSQL, Hasura and the operator:
   ```bash
   GOBIN=$PWD/operator/bin go install sigs.k8s.io/kind@v0.33.0
   make -C operator kustomize
   scripts/k8s/dev-cluster.sh up
   ```
   Credentials are generated into Secrets (`dxos-platform-credentials`,
   `dxos-operator-config`); data lives in an `emptyDir` and is lost with the pod.
2. Publish an application package as a resource and wait until it is ready:
   ```bash
   go -C operator run ./cmd/dxos-operator export-crd ../apps/procurement \
     | kubectl --context kind-dxos-dev apply -f -
   kubectl --context kind-dxos-dev wait dxapplication/procurement --for=condition=Ready
   kubectl --context kind-dxos-dev get dxapplications
   ```
   The capabilities the package declares (procurement: cap-process, cap-hr,
   cap-work) must be enabled; `scripts/k8s/dev-cluster.sh demo` applies the
   package and demonstrates drift repair.
3. Status conditions: `Valid`, `SchemaInSync`, `PermissionsInSync`, `Published`,
   `Ready`, plus `status.activeVersion`. A blocked schema change keeps the previous
   version serving. Deleting the resource revokes its grants and marks the
   `core.apps` row `Removed`; the domain tables, data and roles are kept. A
   resource may set `spec.interval` and `spec.suspend`, and the
   `dxos.io/reconcileAt` annotation requests an immediate reconciliation
   ([control plane](../architecture/control-plane.md#kubernetes-controller)).
4. Remove the cluster with `scripts/k8s/dev-cluster.sh down`.

For another cluster, create the `dxos-operator-config` Secret (keys `DATABASE_URL`,
`HASURA_URL`, `HASURA_ADMIN_SECRET`) in `dxos-operator-system`, then run
`make -C operator docker-build deploy IMG=<registry>/dxos-controller:<tag>`
(`docker-build` builds the `dxos-controller` image from `Dockerfile.controller`). The
operator needs an operator-privileged database connection; never reuse runtime
service credentials.
