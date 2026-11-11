# ADR 0004: Kubernetes CRD Operator with a Cluster-Scoped Application Definition

- **Status:** Proposed
- **Date:** 2026-11-11

## Context

`dxos-operator` is a CLI (`validate`, `plan`, `apply`, `reconcile`). It writes
domain DDL, Hasura metadata and the per-workspace `core.app_registry` from a
manifest file and a `-workspace` flag. Two properties of the current
implementation block a reliable continuous control plane:

1. **Mixed resource scopes.** Domain tables, triggers, Hasura permissions and
   workflows are global: one `domain.<table>` serves every workspace through its
   `workspace_id` column. `core.app_registry` is per workspace. The planner
   derives "previously managed" state from one workspace's registry, then drops
   global Hasura permissions. Reconciling or upgrading an app in workspace A can
   therefore revoke grants that workspace B still uses.
2. **No schema convergence.** DDL uses `CREATE TABLE IF NOT EXISTS`. A newer
   manifest that changes nullability or adds a column is either ignored while
   the registry reports `Ready`, or fails late in Hasura.

The project also needs a GitOps/self-healing control plane (problem statement
principle 4; Argo CD appears in the reference stack). The manifest already uses
`apiVersion: dxos.io/v1alpha1` and `kind: DXApplication`.

## Decision

1. **One cluster-scoped CRD.** Add `DXApplication` (`dxos.io/v1alpha1`,
   cluster scope) implemented with kubebuilder/controller-runtime in `operator/`.
   Each custom resource is the single owner of an application's global resources.
   The `spec.workspaces` field lists the workspaces where it is published. Every
   workspace runs the same application version. A separate per-workspace
   installation resource is deferred.
2. **The file manifest stays the authoring format.** Kubernetes `ObjectMeta`
   cannot carry `displayName` or `version`, and workflow files are not readable
   in-cluster. The CR therefore moves `displayName`/`version` into `spec` and
   inlines each workflow definition. A converter and the `export-crd` CLI command
   produce a CR from an application package. Reconcile converts the CR back to
   the existing manifest model and reuses `manifest.Validate`, the planner and the
   applier. The CLI keeps working against Docker Compose.
3. **Global definition store.** Add `core.app_definitions` (one row per
   application: version, validated spec, update time). The planner computes
   global drops from this table (all active applications), never from one
   workspace's registry. `core.app_registry` remains the per-workspace
   publication read by runtime consumers.
4. **Converge or refuse schema changes.** Before applying, the operator compares
   the live table with the desired columns. Adding a nullable column is applied
   with `ALTER TABLE`. Any other difference blocks publication with a
   structured error, and the previous active version keeps serving.
5. **Status and lifecycle.** The CR reports `observedGeneration` and the
   conditions `Valid`, `SchemaInSync`, `PermissionsInSync`, `Published`, `Ready`,
   plus per-workspace publication results. A finalizer revokes the application's
   permissions, registry rows and definition on deletion. Domain data is
   retained. Periodic requeue repairs drift without rewriting unchanged
   metadata.
6. **Validation layering.** The CRD OpenAPI schema (generated from Go types)
   rejects structurally invalid objects at admission. Semantic validation
   (`manifest.Validate`) runs in reconcile and reports `Valid=False` without side
   effects. No admission webhook is used, so cert-manager is not required.

## Consequences

- Fixes cross-workspace permission loss for both CLI and controller paths.
- Adds Kubernetes client dependencies (Apache-2.0) to the operator module; the
  third-party inventory in `NOTICE`/`COPYRIGHT.txt` must be updated.
- Generated CRD/RBAC YAML must carry the project SPDX header to pass hygiene.
- The controller requires network access to PostgreSQL and Hasura and uses the
  bootstrap/operator credentials from a Kubernetes Secret, never runtime service
  credentials.
- Not decided here: per-workspace installation CRD, destructive migrations, data
  deletion on uninstall, Kestra deployment ownership (planned as a later task).
