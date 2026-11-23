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

A qualified local environment is disposable and provisions restricted roles before
Hasura and cap-process, and delays persistent Kestra until its database is created.
Use [Verification](../contribute/verification.md) to record evidence. The generic
archive procedure below is a release procedure; it does not by itself establish
clean-machine installation or frontend readiness. Only procurement/process_fixture
are supported in this scope; registry discovery and beta automatic-workflow
readiness remain open.

## Prerequisites

- Docker Engine & Docker Compose v2
- Go 1.24+ (with `GOTOOLCHAIN=auto` for Go 1.27.4 module builds)
- Node.js 22+ and npm (for building `workspace-ui`)
- Python 3.12+ with `PyYAML` (for contract verification and demo seeding)
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
   `ENABLED_CAPABILITIES` (default `cap-process,cap-hr,cap-work,cap-data,cap-ai`, which
   the sample apps need), sets the runtime role passwords
   (`HASURA_DATABASE_PASSWORD`, `PROCESS_DATABASE_PASSWORD`) and creates Kestra's
   own `dxos_kestra` database (`KESTRA_DATABASE_PASSWORD`), all in
   `platform/postgres/init/08_runtime_passwords.sh`. This happens once per data
   volume: after changing these values, recreate it with `docker compose down -v`.
   `scripts/test-compose.sh` waits for every service, logs in as each runtime role
   from another container and probes each HTTP entry point. It expects every
   package in `apps/` to be Ready, except a package that declares a capability
   `ENABLED_CAPABILITIES` leaves off, which the operator refuses to install.

   To run cap-ai, the personal library and the FTS-only Assistant, use
   `make rag-up` instead of the plain `docker compose up -d` above. It creates
   local secrets in `.env.rag`, starts `compose.rag.yaml`, and enables the
   Assistant once `personal_library` is `Ready`
   ([knowledge models](../guides/knowledge/models.md#compose-installation)).

   Sign-in uses Keycloak (realm `dxos`, client `dxos-portal`, browser flow with
   PKCE). Create accounts in the Keycloak admin console
   (`http://localhost:8085/admin`, `KEYCLOAK_ADMIN`/`KEYCLOAK_ADMIN_PASSWORD`). A
   user appears in `core.users` on their first request. Set
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
     permissions are dropped, and the domain table, data and roles are kept;
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

   Without Compose, validate and apply application manifests using `dxos-operator`:
   ```bash
   ./bin/dxos-operator validate apps/procurement/manifest.yaml
   HASURA_URL=http://localhost:8080 HASURA_ADMIN_SECRET=dxos_admin_secret \
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   ./bin/dxos-operator apply apps/procurement/manifest.yaml
   ```

## Rebuilding an existing model installation

The optional model migrations `cap_ai/0004_models_vectors.sql` and
`0005_model_registry.sql` were rebased for the coordinated baseline
([ADR 0010](../adr/0010-model-operations-without-monetary-admission.md)). An
installation that applied the earlier checksums must rebuild, not upgrade.

What changes for that installation:

- both migration runners refuse an old applied model checksum before any domain
  SQL, with or without the optional schema flags, and exit non-zero;
- the model capability has no budget, reservation, allocation, usage ledger or
  priced-profile objects, and a fixed set of durable operation rows instead;
- model calls are admitted by durable operation state, not by an account;
- the Agent consumer setting is `AI_AGENT_PROCESSING_APP_ID`; the retired
  `AI_AGENT_BILLING_APP_ID` is not read;
- catalog documents must declare `schema_version: 2` without price fields.

What the owner must do, for the named target only:

1. Inventory the actual installation (Compose project, kind cluster, CI or shared
   development target) and record the applied `cap-ai` versions and checksums.
2. Confirm scope with the responsible human and stop only the services that use
   that database; verify writes have ceased. This document triggers no drop, no
   volume removal, no ledger edit and no checksum bypass.
3. If the retained data matters, take a backup and prove its recoverability in an
   isolated target before any rebuild. Keep the old stack and its recovery path
   available.
4. Provision or rebuild a named database on the approved revision, bootstrap it
   with the required capability and schema settings, and point the matching
   binaries, configuration and catalog revision at it.
5. Re-derive the model capability state: re-publish application packages,
   re-register knowledge sources, re-import `schema_version: 2` candidates,
   re-approve processing policies and re-issue document grants.
6. Do not restore legacy application data wholesale over the rebased schema. A
   full `cap_ai` dump reinstates the removed monetary relations and rows and
   carries operation rows that no longer match the schema. Data in other
   capability schemas follows that schema owner's own migration decision; it is
   never a justification for restoring `cap_ai`.
7. Verify before resuming traffic: schema objects and grants, the operation
   interfaces, model qualification, catalog v2 acceptance, lexical fallback and
   the smoke checks against the named target. Record the revision and results.

The rebuild is a human execution gate. Plan an interruption window with each
owner and record it with the confirmation; no automated reset is provided.

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
   `core.apps` row `Removed`; the domain table, data and roles are kept.
4. Remove the cluster with `scripts/k8s/dev-cluster.sh down`.

For another cluster, create the `dxos-operator-config` Secret (keys `DATABASE_URL`,
`HASURA_URL`, `HASURA_ADMIN_SECRET`) in `dxos-operator-system`, then run
`make -C operator docker-build deploy IMG=<registry>/dxos-operator:<tag>`. The
operator needs an operator-privileged database connection; never reuse runtime
service credentials.
