# Deployment

| Target | Files | Status |
| --- | --- | --- |
| Docker Compose, one host | `compose.yaml` (+ `compose.rag.yaml`, `compose.models.yaml`) at the repository root | Supported; see `docs/operations/installation.md` |
| Local Kubernetes for controller development (kind) | `platform/k8s/dev`, `scripts/k8s/dev-cluster.sh` | PostgreSQL, Hasura and the controller only |
| Kubernetes, whole platform, any cluster | `deploy/k8s/base` plus an overlay for your cluster | Being tested (#166) |
| Example overlay: Amazon EKS | `deploy/k8s/overlays/eks` | Reference; the cluster used to test the Kubernetes deployment (#166, #169) |
| Example overlay: Google GKE | `deploy/k8s/overlays/gke` | Reference only, not run on a cluster |
| Example overlay: Azure AKS | `deploy/k8s/overlays/aks` | Reference only, not run on a cluster |
| Example overlay: your own cluster (on-premises) | `deploy/k8s/overlays/onprem` | Reference only |

`deploy/` is added beside the other deployment files; none of them is replaced.
In this file, paths such as `overlays/eks` and `apply.sh` are relative to
`deploy/k8s`.

**The platform does not depend on a cloud provider.** `deploy/k8s/base` is plain
Kubernetes: Deployments, one StatefulSet, Services, ConfigMaps, Secrets and
PersistentVolumeClaims on the cluster's default StorageClass. It uses no cloud
API, provider annotation or provider storage. Everything specific to one kind of
cluster (storage class, ingress controller, certificates, how the cluster itself
is created) belongs in an overlay. **The overlays under `overlays/` are reference
examples, not requirements:** `eks` is the one the project uses to test on a real
managed cluster; `gke` and `aks` show the same setup on Google Cloud and Azure and
have not been run; `onprem` is for a cluster you run yourself. Write your own overlay for your cluster (see below); none of
these cloud providers is needed.

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
  overlays/             reference examples, not requirements:
    eks/                Amazon EKS: gp3 volumes, one ALB, ACM certificate (the test cluster)
    gke/                Google GKE: pd-balanced disks, GKE Ingress, managed certificate (untested)
    aks/                Azure AKS: Standard SSD disks, app routing Gateway API, Key Vault certificate (untested)
    onprem/             your own cluster: its default storage, any Gateway API implementation, cert-manager
  components/
    gateway-routes/     the public routes as Gateway API HTTPRoutes, shared by aks and onprem
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

### Pod security

Every workload runs with the `RuntimeDefault` seccomp profile, without
privilege escalation and with all Linux capabilities dropped. Where the image
allows it, it also runs as a fixed non-root user on a read-only root filesystem,
with `emptyDir` volumes only for the paths it must write:

| Service | User | Read-only root | Writable |
| --- | --- | --- | --- |
| authz, cap-hr, cap-process | 10001 | yes | none |
| workspace-ui | 101 (nginx) | yes | `/tmp`, `/etc/nginx/conf.d` (rendered at start) |
| postgres (and its seed init container) | 70 | yes | `/var/run/postgresql`, `/tmp`, the data volume |
| hasura | 1001 | yes | `/tmp` |
| kestra | 1000 | yes | `/tmp`, the data volume |
| seaweedfs | 1000 | yes | `/tmp`, the data volume |
| dxos-controller | 10001 (image) | yes | none |
| keycloak | 1000 | no: `start-dev` rebuilds the server into `/opt/keycloak/lib` | the data volume |
| apisix | 636 | no: writes its generated `nginx.conf`, logs and temp files under `/usr/local/apisix` | |
| clamav | starts as root | no: prepares its directories, then runs as `clamav`; keeps only CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID | the signature volume |

The three exceptions are recorded with their reasons in `.trivyignore.yaml` and
tracked in #170; `tests/test_deploy.py` fails if a workload loses any of these
settings.

### Keeping Compose and Kubernetes in step

The platform is deployed two ways, so the workflow `Deploy drift`
(`.github/workflows/deploy-drift.yml`) fails a change that updates one side only.
It runs on changes to `compose.yaml`, `platform/`, `deploy/` and the controller
configuration, and checks that:

- every generated `configmap.yaml` matches the files under `platform/`
  (`scripts/k8s/render-configmaps.py --check`);
- every service of `compose.yaml` exists in `base/`, with the same image (or the
  image built from the same source), the same container port and the same
  environment variable names (`scripts/k8s/check-compose-parity.py`). Values are
  not compared. An intended difference goes in `ALLOWED` in that script, with its
  reason; an entry that is no longer needed fails too.

Run both locally with `make k8s-config` and
`python3 scripts/k8s/check-compose-parity.py`.

## Kubernetes (any cluster)

### What the cluster needs

| Need | Why | Notes |
| --- | --- | --- |
| x86-64 (`amd64`) nodes | The controller image is built for `linux/amd64` only | |
| About 5 GiB of memory requested, up to about 12 GiB used | Sum of the pods' requests and limits | Two nodes of 2 vCPU / 8 GiB are comfortable |
| A default StorageClass with `ReadWriteOnce` volumes | PostgreSQL, Keycloak, SeaweedFS, ClamAV and Kestra keep data on volumes | Or name a class in your overlay. Prefer `reclaimPolicy: Retain` so a deleted claim keeps its data |
| An Ingress controller or a Gateway API implementation, and a TLS certificate | Only to serve the platform outside the cluster | Any one works; the routes are listed below |
| Two host names | The UI and gateway on one (`app.`), Keycloak on the other (`auth.`) | Any DNS provider |
| `kubectl` 1.27 or later | `apply.sh` uses the built-in `kubectl kustomize` | |

The project has tested the deployment on Amazon EKS 1.36 (see the example below);
other clusters are expected to work but have not been tested yet.

### Write an overlay for your cluster

An overlay sets the public hosts and adds the entry point for your cluster.
Copy the example closest to it and keep its `dxos-public` values:

- with **Gateway API** (`aks`, `onprem`): write only the `Gateway` named `dxos`
  with the listeners `http`, `https-app` and `https-auth`, and include
  `components/gateway-routes`, which holds the routes and fills in the hosts;
- with an **Ingress** (`eks`, `gke`): write the Ingress and keep the
  `replacements` that fill in its hosts.

Gateway API is the better default for a new overlay: it is the portable
successor to Ingress, and the community ingress-nginx controller, the most common
Ingress controller, was retired in March 2026. Add a `storageclass.yaml` only if
the cluster has no suitable default class.

The entry point must route exactly these paths, in namespace `dxos`, and
nothing else:

| Host | Path | Service and port |
| --- | --- | --- |
| `APP_HOST` | `/api/` (prefix) | `apisix:9080` |
| `APP_HOST` | `/v1/graphql` (exact) | `apisix:9080` |
| `APP_HOST` | `/` (prefix) | `workspace-ui:8080` |
| `AUTH_HOST` | `/` (prefix) | `keycloak:8085` |

On `AUTH_HOST`, `/admin` and `/realms/master` must not be reachable: they are
the Keycloak administration console and master realm. The EKS overlay answers
404 with an ALB fixed response. Neither a plain Ingress nor Gateway API's
standard routes have a fixed response, so the other overlays send both paths to
`not-found`, a Service that selects no pod; the load balancer answers 502 or 503
(Gateway API specifies 503) and Keycloak is never reached. Use that, or
your controller's equivalent rule. `tests/test_deploy.py` checks every overlay
for these routes. Terminate TLS at the Ingress for both hosts. Do not add routes to
Hasura, Kestra, PostgreSQL, SeaweedFS or ClamAV; reach them with
`kubectl -n dxos port-forward`.

The six `dxos-public` values are the public origins Keycloak and the services
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
  - DXOS_APP_URL=https://app.example.org
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

`KEYCLOAK_DIRECTORY_SECRET` lets operators see people who have not signed in
yet, to grant them roles: the Job `keycloak-directory` sets up the read-only
Keycloak client `dxos-directory` (`view-users`) with it. A secrets file written
before that key existed needs it added:
`echo "KEYCLOAK_DIRECTORY_SECRET=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >> ~/dxos-k8s.env`.

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

Pin the images by digest. The base runs the `:main` images, which move with
every merge, and a node that already holds a `:main` image keeps using it when a
pod restarts. In your overlay:

```yaml
images:
- name: ghcr.io/nguyentin05/opendx-workspace-ui
  digest: sha256:...   # docker buildx imagetools inspect ghcr.io/nguyentin05/opendx-workspace-ui:<tag>
```

Do the same for `opendx-dxos-controller`, `opendx-dxos-authz`, `opendx-cap-hr` and
`opendx-cap-process`, with the digests of one release or one commit of `main`.

Install an application:

```bash
(cd operator && go run ./cmd/dxos-operator export-crd ../apps/procurement) | kubectl apply -f -
kubectl get dxapplications -w
```

After changing a file under `platform/`, run `make k8s-config`; then run
`apply.sh` again (also after changing a value in the overlay) and restart the
affected deployment
(`kubectl -n dxos rollout restart deployment/<name>`). The realm file is imported
only into an empty Keycloak store, so a new `DXOS_APP_URL` reaches an existing
realm only through the Keycloak admin console (port-forwarded) or a new store.

## Example overlays (reference only)

These overlays are **examples to copy from, not requirements**. Creating and
running a cluster is the operator's job and is not covered here; each overlay
starts from an existing cluster and lists what it expects that cluster to have.
Nothing in `base/` refers to any of them.

| Overlay | Storage | Entry point and TLS | Status |
| --- | --- | --- | --- |
| `eks` | `dxos-gp3`: encrypted gp3 (EBS CSI) | AWS Load Balancer Controller (ALB), ACM certificate | Used to test the Kubernetes deployment |
| `gke` | `dxos-pd`: pd-balanced (PD CSI) | GKE Ingress, Google-managed certificate, HTTP→HTTPS redirect | Not run on a cluster |
| `aks` | `dxos-disk`: Standard SSD (Azure Disk CSI) | Application routing add-on, Gateway API (`approuting-istio`), Key Vault certificate | Not run on a cluster |
| `onprem` | The cluster's default class | Any Gateway API implementation, cert-manager | Reference |

The cloud overlays keep volumes after their claims are deleted (`Retain`): the
disks outlive the platform and stay billed until you delete them.

Checked against the providers' documentation on 2026-11-25: GKE Ingress
([Ingress for external Application Load Balancers](https://cloud.google.com/kubernetes-engine/docs/how-to/load-balance-ingress),
[Ingress configuration](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/ingress-configuration),
[Google-managed certificates](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/managed-certs)),
the AKS application routing Gateway API implementation
([overview](https://learn.microsoft.com/en-us/azure/aks/app-routing-gateway-api),
[DNS and TLS](https://learn.microsoft.com/en-us/azure/aks/app-routing-gateway-api-dns-tls),
[migration from NGINX](https://learn.microsoft.com/en-us/azure/aks/app-routing-nginx-to-gateway-api-migration)),
[cert-manager with Gateway API](https://cert-manager.io/docs/usage/gateway/) and
the [Gateway API specification](https://gateway-api.sigs.k8s.io/reference/api-spec/).

### Amazon EKS (test cluster)

The project tests the Kubernetes deployment on EKS because a managed cluster with
a public load balancer is the closest to a real installation. Nothing outside
`overlays/eks` refers to AWS.

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

Then set the six `dxos-public` values in `overlays/eks/kustomization.yaml`,
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

### Google GKE (reference, untested)

The overlay expects a VPC-native GKE cluster (the default), which has the PD CSI
driver and the GKE Ingress controller built in. It adds:

- `storageclass.yaml`: default class `dxos-pd`, balanced persistent disks;
- `ingress.yaml`: one external Application Load Balancer for both hosts;
- `managedcertificate.yaml`: a Google-managed certificate for both hosts. It
  turns Active only after both host names point at the Ingress address, which can
  take a while;
- `frontendconfig.yaml`: HTTP redirected to HTTPS;
- `backendconfig.yaml`: health checks (`/realms/dxos`, since APISIX has no route
  at `/`) and a 120 s timeout, attached to the `apisix` and `keycloak` Services;
- `not-found.yaml`: the Service the Keycloak console paths go to.

GKE Ingress is selected with the `kubernetes.io/ingress.class: gce` annotation
(GKE does not accept `spec.ingressClassName`), and `Exact` paths need GKE
1.21.3-gke.1600 or later. Google points new setups to the GKE Gateway API with
Certificate Manager; the platform's routes are the same Gateway API routes as in
`components/gateway-routes`, should you prefer it. Set the six `dxos-public` values in `overlays/gke/kustomization.yaml`, run
`deploy/k8s/apply.sh deploy/k8s/overlays/gke ~/dxos-k8s.env`, and point both hosts
at the address from `kubectl -n dxos get ingress dxos`.

### Azure AKS (reference, untested)

The overlay uses the **Gateway API implementation** of the AKS application
routing add-on. Its older NGINX Ingress is supported by Microsoft only through
November 2026, after the upstream ingress-nginx project was retired in March
2026. The overlay expects a cluster with:

- the managed Gateway API CRDs and the add-on's Istio-based implementation
  (`az aks update --enable-gateway-api --enable-app-routing-istio`, Azure CLI
  2.86.0 or later); the GatewayClass is `approuting-istio`;
- for the certificate: the OIDC issuer and workload identity, the Key Vault
  secrets provider, and a user-assigned managed identity with the
  `Key Vault Secrets User` role on a vault holding one certificate for both
  hosts, federated with the ServiceAccount `dxos-gateway-tls`.

It adds:

- `storageclass.yaml`: default class `dxos-disk`, Standard SSD managed disks;
- `gateway.yaml`: the `Gateway` with an HTTP listener (redirected to HTTPS) and
  one HTTPS listener per host, each reading the certificate through the
  `kubernetes.azure.com/tls-cert-keyvault-uri` and
  `kubernetes.azure.com/tls-cert-service-account` listener options;
- `serviceaccount.yaml`: the workload identity that reads the certificate;
- the routes, from `components/gateway-routes`.

The add-on cannot set a request body limit through this implementation; the
120 s request timeout is set on the routes. Set the six `dxos-public` values in
`overlays/aks/kustomization.yaml`, the certificate's unversioned URI in
`gateway.yaml` and the identity's client ID in `serviceaccount.yaml`, run
`deploy/k8s/apply.sh deploy/k8s/overlays/aks ~/dxos-k8s.env`, and point both
hosts at the address of the Gateway (`kubectl -n dxos get gateway dxos`).

### Your own cluster (on-premises, reference)

This repository does not cover building or running a Kubernetes cluster. The
`onprem` overlay only connects the platform to what a self-managed cluster
already provides, through Gateway API, so it works with whichever
implementation the cluster runs. It expects:

| The cluster provides | Examples |
| --- | --- |
| A default StorageClass with `ReadWriteOnce` volumes; the overlay adds none | Longhorn, Rook-Ceph, OpenEBS, an NFS CSI driver; local-path for a lab |
| A Gateway API implementation and its GatewayClass | Envoy Gateway (`eg`), Traefik, Cilium, Istio, NGINX Gateway Fabric |
| An address for the Gateway's Service | MetalLB, kube-vip, or the implementation's NodePort or host-network mode |
| cert-manager with Gateway API support (`config.gatewayAPI.enabled: true`) and a ClusterIssuer | Let's Encrypt (ACME) for public hosts, or your internal CA |

It adds `gateway.yaml` (set `gatewayClassName` to your class, and the
`cert-manager.io/cluster-issuer` annotation to your issuer; cert-manager then
issues one certificate for both hosts into the Secret `dxos-tls`) and the routes
from `components/gateway-routes`. Set the six `dxos-public` values in
`overlays/onprem/kustomization.yaml`, run
`deploy/k8s/apply.sh deploy/k8s/overlays/onprem ~/dxos-k8s.env`, and point both
hosts at the Gateway's address (`kubectl -n dxos get gateway dxos`).

## Known gaps

- cap-ai and the RAG services are not deployed; requests to `/api/cap/ai/` fail.
- Keycloak runs `start-dev` with its file store, as in Compose (#170).
- `scripts/seed-demo.py` and the `scripts/test-*.sh` probes target the Compose
  ports and have not been adapted.
- Only the EKS overlay has run on a cluster; GKE, AKS and on-premises are checked
  by `kubectl kustomize` and `tests/test_deploy.py`.
