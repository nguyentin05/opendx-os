---
title: Operate on Kubernetes
description: Day-to-day operation of the Kubernetes deployment, from updates and secrets to demo data, checks and backups.
diataxis: how-to
audience: [operator]
status: active
owner: platform
last_reviewed: 2026-11-26
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
   scripts/migrate-rag.sh --database-url-env MIGRATE_URL --scope core --through <version>
   ```

   A new installation runs every migration when PostgreSQL first initializes.

4. Install or upgrade an application:

   ```bash
   (cd operator && go run ./cmd/dxos-operator export-crd ../apps/<app>) | kubectl apply -f -
   kubectl get dxapplications -w
   ```

Each apply leaves the previous generation of the generated ConfigMap
`dxos-public-<hash>` behind. Delete the ones no pod uses any more.

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
same way.

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
kubectl -n dxos exec postgres-0 -c postgres -- sh -c 'pg_dumpall -U "$POSTGRES_USER" --roles-only --no-role-passwords' > roles.sql
```

Check a dump by restoring it into a throwaway PostgreSQL 17 (`pg_restore`). The
role passwords are not in the dump; they come from the secrets file. Keycloak's
accounts are not in PostgreSQL yet: Keycloak keeps them on its own volume
`keycloak-data` (#170). Snapshot that volume with your provider, or recreate demo
accounts with `seed-demo.sh`. [Backup and restore](backup-restore.md) describes
the full procedure, including object storage, for Compose.

## Remove

Volumes use a `Retain` StorageClass in the example overlays. Deleting the
namespace or the cluster keeps the disks, and they stay billed until you delete
them with your provider. Remove the load balancer's DNS records and
certificates too, and rotate every secret of the file.
