---
title: Control plane
description: The Operator, reconcile, upgrade rules, and the Kubernetes controller.
diataxis: explanation
audience: [platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-24
---

# Control plane

The `dxos-operator` is the control plane. It plans and applies application
configuration out of band; it is not on the synchronous request path.

The `dxos-operator` CLI offers `validate`, `plan`, `apply`, `reconcile`,
`export-crd` and `version` ([CLI reference](../reference/cli/dxos-operator.md)).
`validate` and `plan` have no side effects. Under Compose, the `operator`
service runs `reconcile --dir /apps --interval 30s`; a package removed from the
directory is uninstalled by the next pass. An audit verification command is not
implemented. Configuration progresses through
`Pending -> Applying -> Ready | Failed`, with manifest versions and structured
errors. Only ready configuration is published; a failed update keeps the
previous version serving. Runtime consumers read published configuration
without calling the Operator for each request.

## Reconcile

Reconcile adds missing and revokes excess configuration within managed resources
and roles, using the active manifest set. Multiple applications sharing `core.*`
require combined valid permissions; reconciling one application must not remove
another's grants.

Global resources (domain tables, triggers, Hasura metadata) are planned from the
active application definitions in `core.apps`, never from a single deployment
registry. Unpublishing one application does not remove another application's
grants.

Before applying, the Operator compares each live domain table with its manifest.
Adding a nullable column is converged automatically; a type or nullability change,
a new required column, or an undeclared live column blocks publication with a
structured error while the previous version keeps serving. A removed state
still used by records is refused the same way. CHECK constraints are not
compared yet.

## Model provisioning CLI

`dxos-model` is a second out-of-band control-plane tool, in the capabilities Go
module, for the optional hosted-model path
([CLI reference](../reference/cli/dxos-model.md)). An operator runs the binary
explicitly in a protected deployment context to validate and import model catalog
declarations, make one synthetic verification call, and perform audited model-slot
and PERSONAL processing-approval changes.

It is not an Operator dependency and not a resident service:

- the `operator` service does not run it, wait for it or call it, and no Runtime
  or reconciliation path imports its package;
- Runtime services read published configuration and database slots, so a
  `dxos-model` run is never on a request path;
- it is not deployed as a container in the base stack. The optional
  `compose.model-adapter.yaml` overlay starts only the model adapter, never the
  CLI;
- it holds no application identity and no long-lived credential: each invocation
  authenticates one verified operator token and opens exactly one restricted
  deployment login, then exits.

The model adapter itself is a Tier 2 boundary integration, not part of the control
plane: it translates the existing bounded protocol to one reviewed upstream call
and owns no database, identity or policy decision.

## Install, upgrade, and uninstall

Installing an application writes, in one transaction after its DDL and Hasura
metadata succeed, the `core.apps` row (`Ready`), its roles in `core.roles`, and
its capabilities in `core.app_capabilities`. Each attempt is recorded in
`core.app_deployments`. A failed upgrade keeps the previous version `Ready` and
records `last_error`; a failed first install leaves the row `Failed`.

An upgrade may add a table, a nullable column, or a state. A table that is no
longer declared loses its Hasura permissions and keeps its rows. A changed type
or nullability, a new required column, a column that is no longer declared, or a
removed state still used by records refuses the upgrade.

Uninstall marks the row `Removed`, drops Hasura permissions, retires packaged
knowledge, releases capabilities, and deletes the application's flows. Domain
tables, data, roles, and role assignments are kept. Gateway routes are static
(`platform/apisix/apisix.yaml`) and are not changed per application. Deleting domain data is a separate operation requiring
an explicit retention policy and authorized scope.

## Kubernetes controller

On Kubernetes, the `dxos-controller` binary reconciles cluster-scoped
`DXApplication` resources with the same planner and applier as the
`dxos-operator` CLI and reports
progress in status conditions. See
[installation](../operations/installation.md#kubernetes-operator-development-cluster).
The CLI keeps working without a cluster.

A resource overrides the controller's drift-repair period with `spec.interval`
and pauses its own reconciliation with `spec.suspend`; deleting a suspended
resource still runs so an uninstall is not delayed. Setting or changing the
`dxos.io/reconcileAt` annotation requests an immediate reconciliation; the
handled value is reported in `status.lastHandledReconcileAt`. One reconciliation
(converge or uninstall) is bounded by `--reconciliation-timeout`, and
transitions are emitted as Kubernetes events. Failed reconciliations retry
quickly a few times, then back off to one attempt per minute while the failure
stays reported in conditions; `--concurrency` allows several resources to be
reconciled at once, and their applies serialize on a database lock.

For the exact lifecycle rules, see [Manifest lifecycle](../reference/manifest/lifecycle.md).
