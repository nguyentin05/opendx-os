---
title: dxos-operator CLI
description: Commands for validating, planning, installing, and reconciling applications.
diataxis: reference
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# dxos-operator CLI

```text
dxos-operator <command> [options]
```

| Command | Purpose |
| --- | --- |
| `validate <manifest.yaml>` | Strictly validate a manifest. No side effects |
| `plan [-format sql\|hasura] <manifest.yaml>` | Print the generated DDL (`sql`, default) or Hasura metadata (`hasura`). No side effects |
| `apply <manifest.yaml>` | Install or upgrade one application |
| `reconcile [--interval 30s] (--dir <apps-dir> \| <apps-dir>)` | Install exactly the applications in a directory, one package per subdirectory, and uninstall the rest. Without `--interval` it runs once |
| `export-crd <app-dir>` | Print a `DXApplication` resource for an application package |
| `version` | Print the operator version |

The Compose `operator` service runs `reconcile` over `apps/` every 30 seconds; see
[installation](../../operations/installation.md).

The Kubernetes `DXApplication` controller is a separate binary, `dxos-controller`;
see [installation](../../operations/installation.md#kubernetes-operator-development-cluster).

## `dxos-controller` flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--metrics-bind-address` | `0` | Metrics endpoint address, or `0` to disable |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint address |
| `--leader-elect` | `false` | Enable leader election so only one manager reconciles |
| `--requeue-interval` | `5m` | Drift-repair period for `Ready` resources that do not set `spec.interval` |
| `--reconciliation-timeout` | `10m` | Maximum duration of one reconciliation of a resource, or `0` to disable |
| `--concurrency` | `1` | Maximum number of concurrent reconciles; applies serialize on a database lock |

The controller reconciles cluster-scoped `DXApplication` resources with the same
planner and applier as the CLI and reports progress in status conditions. Each
resource may override the drift-repair period with `spec.interval` (a positive
duration such as `30s` or `5m`) and pause its reconciliation with
`spec.suspend`; setting or changing the `dxos.io/reconcileAt` annotation
requests an immediate reconciliation, whose value is reported in
`status.lastHandledReconcileAt`. Reconciliation transitions are also emitted as
Kubernetes events (for example `Applied`, `DriftRepaired`, and `ApplyFailed`).
A failed reconciliation retries within seconds a few times, then backs off to
one attempt per minute while the failure stays reported in conditions.
See [installation](../../operations/installation.md#kubernetes-operator-development-cluster).
