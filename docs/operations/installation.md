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
   cp .env.example .env
   docker compose up -d
   ```

4. Validate and apply application manifests using `dxos-operator`:
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
