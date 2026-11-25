# Deployment

| Target | Files | Status |
| --- | --- | --- |
| Docker Compose, one host | `compose.yaml` (+ `compose.rag.yaml`, `compose.models.yaml`) at the repository root | Supported; see `docs/operations/installation.md` |
| Local Kubernetes for controller development (kind) | `platform/k8s/dev`, `scripts/k8s/dev-cluster.sh` | PostgreSQL, Hasura and the controller only |
| Kubernetes, whole platform | `deploy/base` | Not yet tested on a cluster (#166) |
| Amazon EKS | `deploy/overlays/eks` | Not yet tested on a cluster (#166, #169) |

`deploy/` is added beside the other deployment files; none of them is replaced.

## Layout

```
deploy/
  apply.sh              creates Secrets and ConfigMaps, then applies an overlay
  base/
    kustomization.yaml  platform + dxos-controller (operator/config/default)
    platform/           the services of compose.yaml, namespace dxos
  overlays/eks/         gp3 storage, one public ALB, the deployment's domain
```

The base runs the same images and configuration as `compose.yaml`. Service names
match the Compose service names, so `platform/apisix/apisix.yaml`,
`platform/kestra/config.yaml` and the PostgreSQL init scripts are used unchanged:
`apply.sh` turns those files into ConfigMaps, so there is one copy of each.
Applications are installed by dxos-controller from `DXApplication` resources,
not from a watched directory.

## EKS

### Before the first deployment

1. An EKS cluster with x86 nodes (the controller image is `linux/amd64`). The
   pods request about 5 GiB of memory and may use up to about 12 GiB; two `t3.large`
   nodes are the smallest comfortable size.
2. The `aws-ebs-csi-driver` add-on, and the
   [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/).
3. An ACM certificate in the cluster's region for both hosts (for example
   `app.example.com` and `auth.example.com`), validated with the CNAME records
   ACM gives, added in Cloudflare. The ALB finds it from the hosts.
4. In `deploy/overlays/eks/kustomization.yaml`, set the five `dxos-public`
   values to those hosts.
5. A secrets file outside the repository:

   ```bash
   scripts/init-env.sh ~/dxos-eks.env
   # add two more random values of at least 16 letters or digits:
   echo "S3_ACCESS_KEY_ID=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >> ~/dxos-eks.env
   echo "S3_SECRET_ACCESS_KEY=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >> ~/dxos-eks.env
   ```

   `apply.sh` refuses any secret that is shorter or keeps a development default.
   Keep the file: PostgreSQL takes its passwords only when it first initializes
   its volume.

### Deploy

```bash
deploy/apply.sh deploy/overlays/eks ~/dxos-eks.env
kubectl -n dxos get pods -w
kubectl -n dxos get ingress dxos   # ADDRESS: the ALB host name
```

In Cloudflare, point both hosts to the ALB host name with CNAME records. Keep
them DNS-only (grey) while testing; for the demo, proxied (orange) with SSL mode
Full (strict). With the proxy on, the Free plan ends requests after 100 s and
limits uploads to 100 MB.

Install an application:

```bash
(cd operator && go run ./cmd/dxos-operator export-crd ../apps/procurement) | kubectl apply -f -
kubectl get dxapplications -w
```

Run `apply.sh` again after changing a file under `platform/` or a value in the
overlay, then restart the affected deployment
(`kubectl -n dxos rollout restart deployment/<name>`).

### Public surface

The ALB serves only the UI (`app.` `/`), the gateway (`app.` `/api/`,
`/v1/graphql`) and Keycloak (`auth.`), where `/admin` and `/realms/master` answer
404. Hasura, Kestra, PostgreSQL, SeaweedFS and ClamAV have no route; reach them
with `kubectl -n dxos port-forward`.

## Known gaps

- Sign-in on a public host needs #167 (UI sign-in configuration) and #168
  (Keycloak redirect URIs and hostname for the domain).
- cap-ai and the RAG services are not deployed; requests to `/api/cap/ai/` fail.
- Keycloak runs `start-dev` with its file store, as in Compose (#170).
- `scripts/seed-demo.py` and the `scripts/test-*.sh` probes target the Compose
  ports and have not been adapted.
- Nothing here has run on a real cluster yet; the manifests are checked only by
  `kubectl kustomize` and `tests/test_deploy.py`.
