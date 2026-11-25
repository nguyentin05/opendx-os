# Deployment

| Target | Files | Status |
| --- | --- | --- |
| Docker Compose, one host | `compose.yaml` (+ `compose.rag.yaml`, `compose.models.yaml`) at the repository root | Supported; see `docs/operations/installation.md` |
| Local Kubernetes for controller development (kind) | `platform/k8s/dev`, `scripts/k8s/dev-cluster.sh` | PostgreSQL, Hasura and the controller only |
| Kubernetes, whole platform, any cluster | `deploy/base` plus an overlay for your cluster | Being tested (#166) |
| Example overlay: Amazon EKS | `deploy/overlays/eks` | The cluster used to test the Kubernetes deployment (#166, #169) |

`deploy/` is added beside the other deployment files; none of them is replaced.

**The platform does not depend on a cloud provider.** `deploy/base` is plain
Kubernetes: Deployments, one StatefulSet, Services, ConfigMaps, Secrets and
PersistentVolumeClaims on the cluster's default StorageClass. It uses no cloud
API, provider annotation or provider storage. Everything specific to one kind of
cluster (storage class, ingress controller, certificates, how the cluster itself
is created) belongs in an overlay. `overlays/eks` is the overlay the project uses
to test on a real managed cluster; it is an example, not a requirement.

## Layout

```
deploy/
  apply.sh              creates Secrets and ConfigMaps, then applies an overlay
  base/
    kustomization.yaml  platform + dxos-controller (operator/config/default)
    platform/           the services of compose.yaml, namespace dxos
  overlays/eks/         example: gp3 storage, one public ALB, eksctl cluster file
```

The base runs the same images and configuration as `compose.yaml`. Service names
match the Compose service names, so `platform/apisix/apisix.yaml`,
`platform/kestra/config.yaml` and the PostgreSQL init scripts are used unchanged:
`apply.sh` turns those files into ConfigMaps, so there is one copy of each.
Applications are installed by dxos-controller from `DXApplication` resources,
not from a watched directory.

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
deploy/apply.sh deploy/overlays/<your-overlay> ~/dxos-k8s.env
kubectl -n dxos get pods -w
kubectl -n dxos get ingress dxos   # ADDRESS: where to point both host names
```

Point both host names at the Ingress address with your DNS provider.

Install an application:

```bash
(cd operator && go run ./cmd/dxos-operator export-crd ../apps/procurement) | kubectl apply -f -
kubectl get dxapplications -w
```

Run `apply.sh` again after changing a file under `platform/` or a value in the
overlay, then restart the affected deployment
(`kubectl -n dxos rollout restart deployment/<name>`).

## Example: Amazon EKS (test cluster)

The project tests the Kubernetes deployment on EKS because a managed cluster with
a public load balancer is the closest to a real installation. Nothing outside
`overlays/eks` refers to AWS; to run elsewhere, write your own overlay as above.

What the overlay adds, and its counterpart on another cluster:

| File | On EKS | On another cluster |
| --- | --- | --- |
| `cluster.yaml` | `eksctl` creates the cluster: 2 × `m7i-flex.large` (2 vCPU, 8 GiB; one of the types an AWS Free plan account may launch), public subnets without a NAT gateway, the EBS CSI driver add-on, IAM for the Load Balancer Controller | However your cluster is created |
| `storageclass.yaml` | Default class `dxos-gp3`: encrypted gp3 volumes, `Retain` | The cluster's default class, or your own |
| `ingress.yaml` | One internet-facing ALB, TLS from ACM, fixed 404 for the Keycloak console | An Ingress for your controller with the routes above |
| `kustomization.yaml` | Hosts, ALB health-check annotations on `apisix` and `keycloak` | Hosts; drop the `alb.ingress.kubernetes.io` annotations |

### Steps

1. Set `metadata.region` in `cluster.yaml` to a region your AWS account may use,
   then create the cluster (about 20 minutes):

   ```bash
   eksctl create cluster -f deploy/overlays/eks/cluster.yaml
   ```

   If the node group fails with "not eligible for Free Tier", the account is on
   the AWS Free plan: pick an instance type from
   `aws ec2 describe-instance-types --filters Name=free-tier-eligible,Values=true`.
   If `eksctl` stops before the node group is ready, the add-on and the Load
   Balancer Controller's IAM may be missing; create them with
   `eksctl create addon -f ...` and `eksctl create podidentityassociation -f ...`.

2. Install the [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/)
   (its IAM role was created with the cluster):

   ```bash
   helm repo add eks https://aws.github.io/eks-charts
   helm install aws-load-balancer-controller eks/aws-load-balancer-controller -n kube-system \
     --set clusterName=dxos-demo --set serviceAccount.create=false \
     --set serviceAccount.name=aws-load-balancer-controller \
     --set region=ap-southeast-2 \
     --set vpcId=$(aws eks describe-cluster --name dxos-demo --region ap-southeast-2 \
       --query cluster.resourcesVpcConfig.vpcId --output text)
   ```

3. Request an ACM certificate in the cluster's region for both hosts and add the
   validation CNAME records ACM gives to your DNS. The ALB finds the certificate
   from the hosts.
4. Set the five `dxos-public` values in `overlays/eks/kustomization.yaml`, write
   the secrets file as above, and run
   `deploy/apply.sh deploy/overlays/eks ~/dxos-k8s.env`.
5. Point both hosts at the ALB host name from `kubectl -n dxos get ingress dxos`
   with CNAME records. If the DNS is Cloudflare: keep the records DNS-only (grey)
   while testing; with the proxy on (orange, SSL mode Full (strict)) the Free plan
   ends requests after 100 s and limits uploads to 100 MB.

### Cost and removal

The cluster is billed while it exists: the control plane, two `m7i-flex.large` nodes,
the ALB, public IPv4 addresses and the volumes. To pause, scale the node group to
zero (`eksctl scale nodegroup --cluster dxos-demo --region ap-southeast-2 --name general --nodes 0`).
To remove everything:

```bash
kubectl -n dxos delete ingress dxos   # lets the controller delete the ALB first
eksctl delete cluster -f deploy/overlays/eks/cluster.yaml --disable-nodegroup-eviction
```

The `dxos-gp3` class keeps volumes after their claims are deleted (`Retain`), so
the EBS volumes survive the cluster and stay billed: delete them in the EC2
console (filter by tag `kubernetes.io/created-for/pvc/namespace=dxos`) once the
data is no longer needed.

## Known gaps

- Sign-in on a public host needs #167 (UI sign-in configuration) and #168
  (Keycloak redirect URIs and hostname for the domain).
- cap-ai and the RAG services are not deployed; requests to `/api/cap/ai/` fail.
- Keycloak runs `start-dev` with its file store, as in Compose (#170).
- `scripts/seed-demo.py` and the `scripts/test-*.sh` probes target the Compose
  ports and have not been adapted.
- The deployment has not yet passed its tests on a cluster; until then the
  manifests are checked by `kubectl kustomize` and `tests/test_deploy.py`.
