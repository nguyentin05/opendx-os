# OpenDX-OS Installation Guide

For the qualified local backend/platform environment use the
[guarded disposable integration setup](../handoff/integration-setup.md) and
[final evidence](../handoff/integration-evidence.md). That path provisions restricted
roles before Hasura/process and delays persistent Kestra until its database is
created. The generic archive procedure below is a release procedure, **NOT_RUN**
in this handoff; it must not be used as evidence of clean-machine installation or
frontend readiness. Only procurement/process_fixture are supported in this scope;
registry discovery and beta automatic-workflow readiness remain open.

## Prerequisites

- Docker Engine & Docker Compose v2
- Go 1.24+ (with `GOTOOLCHAIN=auto` for Go 1.27.3 module builds)
- Node.js 22+ and npm (for building `workspace-ui`)
- Python 3.12+ with `PyYAML` (for hygiene and contract verification)
- `postgresql-client` (`psql`, `pg_dump`) for database operations

## Out-of-Tree Archive Installation

1. Build the release archive:
   ```bash
   ./scripts/package_release.sh 0.1.0-demo
   sha256sum -c dist/dxos-open-core-0.1.0-demo.tar.gz.sha256
   ```

2. Extract the archive outside the Git repository and run the installer:
   ```bash
   tar -xzf dist/dxos-open-core-0.1.0-demo.tar.gz -C /tmp
   /tmp/dxos-open-core-0.1.0-demo/scripts/install.sh /opt/opendx-os
   ```

3. Configure environment variables and start services:
   ```bash
   cd /opt/opendx-os
   scripts/init-env.sh      # .env with random secrets
   docker compose up -d
   scripts/test-compose.sh
   ```
   At first start PostgreSQL loads the capabilities listed in
   `ENABLED_CAPABILITIES` (default `cap-process,cap-hr,cap-work,cap-data`, which
   the sample apps need), sets the runtime role passwords
   (`HASURA_DATABASE_PASSWORD`, `PROCESS_DATABASE_PASSWORD`) and creates Kestra's
   own `dxos_kestra` database (`KESTRA_DATABASE_PASSWORD`), all in
   `platform/postgres/init/08_runtime_passwords.sh`. This happens once per data
   volume: after changing these values, recreate it with `docker compose down -v`.
   `scripts/test-compose.sh` waits for every service, logs in as each runtime role
   from another container and probes each HTTP entry point.

   Sign-in uses Keycloak (realm `dxos`, client `dxos-portal`, browser flow with
   PKCE). Create accounts in the Keycloak admin console
   (`http://localhost:8085/admin`, `KEYCLOAK_ADMIN`/`KEYCLOAK_ADMIN_PASSWORD`). A
   user appears in `core.users` on their first request. Grant roles by inserting
   into `core.user_roles` and departments into `cap_hr.staff`. The roles take
   effect within `ROLE_CACHE_TTL` (default 5s), with no new token
   ([ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md)).

   For a demo organization, run `scripts/seed-demo.py seed`. It creates three
   departments and six Keycloak accounts (password `DEMO_PASSWORD`, default
   `Demo@2026`) with procurement, maintenance and timesheet roles, and is safe to
   re-run. `scripts/seed-demo.py scopes` then files sample purchase requests (each
   with a quote uploaded to cap-process and scanned by ClamAV) and prints what each
   person sees through the gateway; `scripts/seed-demo.py flow` takes one request
   through submit, department approval and budget approval with cap-process
   commands, including the refusals on the way. Use them on local stacks only.

4. The `operator` service installs exactly the application packages in
   `OPERATOR_APPS_DIR` (default `./apps`, mounted read-only), checking every
   `OPERATOR_RECONCILE_INTERVAL` (default `30s`). It runs
   `dxos-operator reconcile --dir /apps --interval 30s`, the same planner and
   apply code as the Kubernetes controller. No container needs recreating:
   - copy a package directory in to install it; edit it to upgrade;
   - remove it to uninstall: the `core.apps` row becomes `Removed`, its Hasura
     permissions are dropped, and the domain table, data and roles are kept;
   - Hasura metadata changed by hand is repaired on the next pass.

   Each new or changed package is installed on its own, so a refused one (for
   example, a capability that is not enabled) does not hold back the others; it
   is retried when its package changes, or after ten minutes. While any package
   is invalid (for instance half-copied), nothing is uninstalled. Follow it with
   `docker compose logs -f operator`.

   Without Compose, validate and apply application manifests using `dxos-operator`:
   ```bash
   ./bin/dxos-operator validate contracts/fixtures/procurement.yaml
   HASURA_URL=http://localhost:8080 HASURA_ADMIN_SECRET=dxos_admin_secret \
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   ./bin/dxos-operator apply contracts/fixtures/procurement.yaml
   ```

## Kubernetes Operator (development cluster)

The operator also runs as a Kubernetes controller for the cluster-scoped
`DXApplication` resource ([ADR 0004](../adr/0004-kubernetes-crd-operator.md)).
The CLI above keeps working without a cluster.

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
   `core.apps` row `Removed`; the domain table, data and roles are kept.
4. Remove the cluster with `scripts/k8s/dev-cluster.sh down`.

For another cluster, create the `dxos-operator-config` Secret (keys `DATABASE_URL`,
`HASURA_URL`, `HASURA_ADMIN_SECRET`) in `dxos-operator-system`, then run
`make -C operator docker-build deploy IMG=<registry>/dxos-operator:<tag>`. The
operator needs an operator-privileged database connection; never reuse runtime
service credentials.
