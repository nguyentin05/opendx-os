# Deployment

| Target | Files | Status |
| --- | --- | --- |
| Docker Compose, one host | `compose.yaml` (+ `compose.rag.yaml`, `compose.models.yaml`) at the repository root | Supported; see `docs/operations/installation.md` |
| Local Kubernetes for controller development (kind) | `platform/k8s/dev`, `scripts/k8s/dev-cluster.sh` | PostgreSQL, Hasura and the controller only |
| Kubernetes, whole platform, any cluster | `deploy/k8s/base` plus an overlay for your cluster | Being tested (#166) |
| Example overlay: Amazon EKS | `deploy/k8s/overlays/eks` | The cluster used to test the Kubernetes deployment (#166, #169) |

`deploy/` is added beside the other deployment files; none of them is replaced.
In this file, paths such as `overlays/eks` and `apply.sh` are relative to
`deploy/k8s`.

**The platform does not depend on a cloud provider.** `deploy/k8s/base` is plain
Kubernetes: Deployments, one StatefulSet, Services, ConfigMaps, Secrets and
PersistentVolumeClaims on the cluster's default StorageClass. It uses no cloud
API, provider annotation or provider storage. Everything specific to one kind of
cluster (storage class, ingress controller, certificates, how the cluster itself
is created) belongs in an overlay. `overlays/eks` is the overlay the project uses
to test on a real managed cluster; it is an example, not a requirement.

## Layout

Kubernetes manifests live under `deploy/k8s`, grouped by the platform's tiers
(see `docs/architecture/index.md`):

```
deploy/k8s/
  apply.sh              creates the Secrets, then applies an overlay
  base/                 the whole platform; no public entry point
    kustomization.yaml  tiers + dxos-controller, public settings, images
    namespace.yaml
    platform/           Tier 1, namespace dxos, one folder per service:
      postgres/ keycloak/ authz/ hasura/ apisix/ kestra/ seaweedfs/ clamav/
    capabilities/       Tier 2, namespace dxos, one folder per service:
      cap-process/ cap-hr/ workspace-ui/
  overlays/eks/         example: gp3 storage, one public ALB (the test cluster)
```

Each service folder holds one object per file: `service.yaml`,
`deployment.yaml` (`statefulset.yaml` for PostgreSQL), `pvc.yaml` when it keeps
data, and `configmap.yaml` when it reads configuration files.

| Tier | What runs | Where it comes from |
| --- | --- | --- |
| 1, platform | PostgreSQL, Keycloak, dxos-authz, Hasura, APISIX, Kestra, SeaweedFS, ClamAV; dxos-controller | `base/platform/`; the controller from `operator/config/default`, in namespace `dxos-operator-system` |
| 2, capabilities | cap-process, cap-hr, Workspace UI | `base/capabilities/` |
| 3, applications | One `DXApplication` per package in `apps/` | Not a file here: exported from the package and applied when needed ([Install an application](#deploy)); dxos-controller installs it |

The base runs the same images and configuration as `compose.yaml`. Service names
match the Compose service names, so `platform/apisix/apisix.yaml`,
`platform/kestra/config.yaml`, the Keycloak realm and the PostgreSQL init scripts
are used unchanged. Each `configmap.yaml` is a committed manifest generated from
those files, so what is applied can be read and reviewed. It carries the license
header once, at the top of the manifest, instead of inside each embedded file;
`postgres-seed` keeps its files byte for byte, because the migration ledger
checksums whole files. `platform/` stays the only copy: after changing a file there, run
`make k8s-config`; `tests/test_deploy.py` fails while a generated ConfigMap is
out of date. In `postgres-seed`, a key's `__` stands for `/`
(`migrations__core__0001_….sql`); its init container restores the directories.

## Kubernetes (any cluster)

### What the cluster needs

| Need | Why | Notes |
| --- | --- | --- |
| x86-64 (`amd64`) nodes | The controller image is built for `linux/amd64` only | |
| About 5 GiB of memory requested, up to about 12 GiB used | Sum of the pods' requests and limits | Two nodes of 2 vCPU / 8 GiB are comfortable |
| A default StorageClass with `ReadWriteOnce` volumes | PostgreSQL, Keycloak, SeaweedFS, ClamAV and Kestra keep data on volumes | Or name a class in your overlay. Prefer `reclaimPolicy: Retain` so a deleted claim keeps its data |
| An Ingress controller and a TLS certificate | Only to serve the platform outside the cluster | Any controller works; the routes are listed below |
| Two host names | The UI and gateway on one (`app.`), Keycloak on the other (`auth.`) | Any DNS provider |
| `kubectl` 1.27 or later | `apply.sh` uses the built-in `kubectl kustomize` | |

The project has tested the deployment on Amazon EKS 1.36 (see the example below);
other clusters are expected to work but have not been tested yet.

### Write an overlay for your cluster

An overlay sets the public hosts and adds the Ingress for your controller. Copy
`overlays/eks/kustomization.yaml` as a starting point and keep its
`dxos-public` values and `replacements`; replace `storageclass.yaml` and
`ingress.yaml` with your own, or drop `storageclass.yaml` to use the cluster's
default class.

The Ingress must route exactly these paths, in namespace `dxos`, and nothing
else:

| Host | Path | Service and port |
| --- | --- | --- |
| `APP_HOST` | `/api/` (prefix) | `apisix:9080` |
| `APP_HOST` | `/v1/graphql` (exact) | `apisix:9080` |
| `APP_HOST` | `/` (prefix) | `workspace-ui:8080` |
| `AUTH_HOST` | `/` (prefix) | `keycloak:8085` |

On `AUTH_HOST`, `/admin` and `/realms/master` must not be reachable (answer 404
or 403): they are the Keycloak administration console and master realm. The EKS
overlay does this with an ALB fixed response; with another controller, use its
equivalent rule. Terminate TLS at the Ingress for both hosts. Do not add routes to
Hasura, Kestra, PostgreSQL, SeaweedFS or ClamAV; reach them with
`kubectl -n dxos port-forward`.

The five `dxos-public` values are the public origins Keycloak and the services
use:

```yaml
configMapGenerator:
- name: dxos-public
  namespace: dxos
  behavior: merge
  literals:
  - APP_HOST=app.example.org
  - AUTH_HOST=auth.example.org
  - KEYCLOAK_HOSTNAME=https://auth.example.org
  - OIDC_ISSUER_URL=https://auth.example.org/realms/dxos
  - DXOS_OIDC_ORIGIN=https://auth.example.org
```

### Secrets

Write a secrets file outside the repository:

```bash
scripts/init-env.sh ~/dxos-k8s.env
# add two more random values of at least 16 letters or digits:
echo "S3_ACCESS_KEY_ID=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >> ~/dxos-k8s.env
echo "S3_SECRET_ACCESS_KEY=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >> ~/dxos-k8s.env
```

`apply.sh` refuses any secret that is shorter or keeps a development default.
Keep the file: PostgreSQL takes its passwords only when it first initializes its
volume.

### Deploy

```bash
deploy/k8s/apply.sh deploy/k8s/overlays/<your-overlay> ~/dxos-k8s.env
kubectl -n dxos get pods -w
kubectl -n dxos get ingress dxos   # ADDRESS: where to point both host names
```

`apply.sh` only turns the secrets file into three Secrets, which are never
committed, and then runs `kubectl apply --server-side` on the overlay. Without
the script:

1. Create `dxos-secrets` in namespace `dxos` with the keys listed in `apply.sh`
   (`SECRETS=`), `seaweedfs-s3` from `platform/seaweedfs/s3.json` with your two S3
   keys in place of the development ones, and `dxos-operator-config` in
   `dxos-operator-system` (`DATABASE_URL`, `HASURA_URL`, `HASURA_ADMIN_SECRET`,
   `KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD`).
2. `kubectl apply --server-side -k deploy/k8s/overlays/<your-overlay>`.
   Server-side apply is required: `postgres-seed` is larger than the 256 KiB a
   client-side apply can record.

Point both host names at the Ingress address with your DNS provider.

Install an application:

```bash
(cd operator && go run ./cmd/dxos-operator export-crd ../apps/procurement) | kubectl apply -f -
kubectl get dxapplications -w
```

After changing a file under `platform/`, run `make k8s-config`; then run
`apply.sh` again (also after changing a value in the overlay) and restart the
affected deployment
(`kubectl -n dxos rollout restart deployment/<name>`).

## Example: Amazon EKS (test cluster)

The project tests the Kubernetes deployment on EKS because a managed cluster with
a public load balancer is the closest to a real installation. Creating and
running the cluster is the operator's job and is not covered here; the platform
starts from an existing cluster. Nothing outside `overlays/eks` refers to AWS; to
run elsewhere, write your own overlay as above.

What the overlay adds, and its counterpart on another cluster:

| File | On EKS | On another cluster |
| --- | --- | --- |
| `storageclass.yaml` | Default class `dxos-gp3`: encrypted gp3 volumes, `Retain` | The cluster's default class, or your own |
| `ingress.yaml` | One internet-facing ALB, TLS from ACM, fixed 404 for the Keycloak console | An Ingress for your controller with the routes above |
| `kustomization.yaml` | Hosts, ALB health-check annotations on `apisix` and `keycloak` | Hosts; drop the `alb.ingress.kubernetes.io` annotations |

The overlay expects the cluster to have:

- the `aws-ebs-csi-driver` add-on, for `dxos-gp3`;
- the [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/),
  which creates the ALB from `ingress.yaml`;
- an ACM certificate in the cluster's region covering both hosts, validated
  through your DNS. The ALB finds it from the hosts.

Then set the five `dxos-public` values in `overlays/eks/kustomization.yaml`,
write the secrets file as above, run
`deploy/k8s/apply.sh deploy/k8s/overlays/eks ~/dxos-k8s.env`, and point both hosts at the
ALB host name from `kubectl -n dxos get ingress dxos`. With Cloudflare as the DNS:
keep the records DNS-only (grey) while testing; with the proxy on (orange, SSL
mode Full (strict)) the Free plan ends requests after 100 s and limits uploads to
100 MB.

To remove the platform, delete the Ingress first so the controller removes the
ALB (`kubectl -n dxos delete ingress dxos`). `dxos-gp3` keeps volumes after their
claims are deleted (`Retain`): the EBS volumes outlive the platform and stay
billed until you delete them (tag `kubernetes.io/created-for/pvc/namespace=dxos`).

## Known gaps

- Sign-in on a public host needs #167 (UI sign-in configuration) and #168
  (Keycloak redirect URIs and hostname for the domain).
- cap-ai and the RAG services are not deployed; requests to `/api/cap/ai/` fail.
- Keycloak runs `start-dev` with its file store, as in Compose (#170).
- `scripts/seed-demo.py` and the `scripts/test-*.sh` probes target the Compose
  ports and have not been adapted.
- The deployment has not yet passed its tests on a cluster; until then the
  manifests are checked by `kubectl kustomize` and `tests/test_deploy.py`.
