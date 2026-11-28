---
title: Operate on Kubernetes
description: Day-to-day operation of the Kubernetes deployment, from updates and secrets to demo data, checks and backups.
diataxis: how-to
audience: [operator]
status: active
owner: platform
last_reviewed: 2026-11-27
---

# Operate on Kubernetes

The Kubernetes deployment lives in
[`deploy/k8s`](https://github.com/nguyentin05/opendx-os/tree/main/deploy/k8s).
Its [README](https://github.com/nguyentin05/opendx-os/blob/main/deploy/README.md)
covers what a cluster needs, how to write an overlay, the secrets file and the
first deployment. This page covers what comes after: updating, secrets, demo
data, checks and backups. The commands assume namespace `dxos` and the secrets
file `~/dxos-k8s.env` given to `deploy/k8s/apply.sh`.

## What runs

Tier 1 (PostgreSQL, Keycloak, dxos-authz, Hasura, APISIX, Kestra, SeaweedFS)
always runs. With the optional component `components/keda`, cap-hr,
cap-process and ClamAV run only while they have work and start when work
arrives. The first request after a quiet period then takes a few seconds, and
the first virus scan a minute or two. Without the component, every service
always runs.

## Update

1. Pin the new images by digest in your overlay (`images:` with `digest:`). The
   base uses the `:main` tags, which move, and a node keeps a cached `:main`
   image across pod restarts.
2. Run `deploy/k8s/apply.sh <overlay> ~/dxos-k8s.env`. It recreates the three
   Secrets (and `dxos-keda` when `KEDA_DATABASE_PASSWORD` is set), runs the
   setup Jobs again (`keycloak-directory`, `keda-setup`), then applies the
   overlay server-side.
3. If the release adds a core migration, apply it to the running database:

   ```bash
   kubectl -n dxos port-forward svc/postgres 15432:5432 &
   export MIGRATE_URL="postgres://dxos_admin:<POSTGRES_PASSWORD>@localhost:15432/dxos_db?sslmode=disable"
   scripts/migrate-core.sh --database-url-env MIGRATE_URL --through <version>
   ```

   A new installation runs every migration when PostgreSQL first initializes.

4. Install or upgrade an application:

   ```bash
   (cd operator && go run ./cmd/dxos-operator export-crd ../apps/<app>) | kubectl apply -f -
   kubectl get dxapplications -w
   ```

Each apply leaves the previous generation of the generated ConfigMap
`dxos-public-<hash>` behind. Delete the ones no pod uses any more.

## Python AI cutover

The base overlay does not deploy Python AI. If your installation adds AI,
prepare an overlay for the API and Celery worker using the same locked image,
plus a one-shot Alembic migration Job. Follow the authority and deployment
boundaries in [ADR 0011](../adr/0011-python-intelligence-runtime.md).

1. Schedule downtime for the legacy AI API and workers, stop new AI work, and
   back up PostgreSQL and private object storage. Verify the restore before
   starting the cutover.
2. Confirm the target PostgreSQL 17 server provides pgvector and PGMQ. The
   stock PostgreSQL image in the base overlay needs an extension-capable
   replacement or an equivalent managed database. Enable `vector` and
   `pgmq` in the application database. Provision the AI database roles and
   the `dxos_ai_ingest` queue with its restricted broker grants before
   starting the new runtime.
3. Apply pending Core migrations separately. Alembic refuses a legacy
   `cap_ai` schema without `cap_ai.alembic_version` or legacy `cap-ai`
   entries in `core.schema_migrations`. Use a fresh AI baseline or have
   the database owner approve an AI-only rebuild that addresses those legacy
   markers. Preserve Core data and Core migration history, application data,
   file metadata and original private objects. Treat legacy AI data as
   outside the in-place migration path, including queued jobs, indexes, model
   settings and conversation history. Re-register app packages and private
   originals under current authority and re-ingest where needed.
4. Run `alembic upgrade head` once with `AI_MIGRATION_DATABASE_URL` set to
   the migration login. The Job manages only `cap_ai` and must succeed before
   the API and worker start. Verify readiness and ingestion against the new
   baseline before reopening AI traffic.

## Secrets

Every secret comes from `~/dxos-k8s.env`, which is kept outside Git. That file is
the only copy, so back it up. A Pod reads a secret when it starts. After changing
one, run `apply.sh` and restart what reads it:

```bash
kubectl -n dxos rollout restart deployment
```

`KEYCLOAK_DIRECTORY_SECRET` is also set in Keycloak, on the client
`dxos-directory`, by the Job `keycloak-directory` that `apply.sh` runs again.
`KEDA_DATABASE_PASSWORD` is set on the login `dxos_keda` by `keda-setup` in the
same way, and `KEYCLOAK_DATABASE_PASSWORD` on the login `dxos_keycloak` by the
init container of the Keycloak Pod, at its next start.

## Accounts

People are created in Keycloak, or in the directory Keycloak federates. The
admin console is not public; reach it with
`kubectl -n dxos port-forward svc/keycloak 8085:8085` and
`http://localhost:8085/admin`. dxos-authz reads the realm every five minutes and
when an operator lists accounts. A new person can therefore get roles and a
department before their first sign-in. An account disabled or deleted in
Keycloak is refused within that time. Roles are granted in the Workspace UI
(*Người dùng & quyền*) and departments under *Tổ chức*.

## Demo data

```bash
scripts/k8s/seed-demo.sh ~/dxos-k8s.env seed   # accounts, departments, roles
scripts/k8s/seed-demo.sh ~/dxos-k8s.env flow   # one request through the approval chain
```

The script reaches Keycloak and the gateway through port-forwards and PostgreSQL
through `kubectl exec`; it takes the other commands of `scripts/seed-demo.py`
too. Use it only on a deployment that holds no real data.

## Check

- Rollout:
  `kubectl -n dxos get pods` and `kubectl get dxapplications` show every
  workload and application ready (with `components/keda`, cap-hr, cap-process
  and ClamAV may show zero replicas).
- Sign-in and gateway, from outside the cluster:

  ```bash
  OIDC_ISSUER_URL=https://<auth host>/realms/dxos APISIX_URL=https://<app host> scripts/test-identity.sh
  ```

- Public surface: `http://` redirects to `https://`; the auth host answers 404 on
  `/admin` and `/realms/master`; only ports 80 and 443 answer.

## Back up

Take these before a demo, a release or an upgrade, and keep them outside the
cluster:

```bash
kubectl -n dxos exec postgres-0 -c postgres -- sh -c 'pg_dump -U "$POSTGRES_USER" -d dxos_db -Fc' > dxos_db.dump
kubectl -n dxos exec postgres-0 -c postgres -- sh -c 'pg_dump -U "$POSTGRES_USER" -d dxos_kestra -Fc' > dxos_kestra.dump
kubectl -n dxos exec postgres-0 -c postgres -- sh -c 'pg_dump -U "$POSTGRES_USER" -d dxos_keycloak -Fc' > dxos_keycloak.dump
kubectl -n dxos exec postgres-0 -c postgres -- sh -c 'pg_dumpall -U "$POSTGRES_USER" --roles-only --no-role-passwords' > roles.sql
```

Check a dump by restoring it into a throwaway PostgreSQL 17 (`pg_restore`). The
role passwords are not in the dump; they come from the secrets file.
`dxos_keycloak` holds Keycloak's accounts, sessions and realm settings, including
password hashes and client secrets: keep that dump like a secret.
[Backup and restore](backup-restore.md) describes the full procedure, including
object storage, for Compose.

## Remove

Volumes use a `Retain` StorageClass in the example overlays. Deleting the
namespace or the cluster keeps the disks, and they stay billed until you delete
them with your provider. Remove the load balancer's DNS records and
certificates too, and rotate every secret of the file.
