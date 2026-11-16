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
| `controller [flags]` | Run the Kubernetes `DXApplication` controller |
| `version` | Print the operator version |

The Compose `operator` service runs `reconcile` over `apps/` every 30 seconds; see
[installation](../../operations/installation.md).

## `controller` flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--metrics-bind-address` | `0` | Metrics endpoint address, or `0` to disable |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint address |
| `--leader-elect` | `false` | Enable leader election so only one manager reconciles |
| `--requeue-interval` | `5m` | Drift-repair period for `Ready` resources |

The controller reconciles cluster-scoped `DXApplication` resources with the same
planner and applier as the CLI and reports progress in status conditions. See
[installation](../../operations/installation.md#kubernetes-operator-development-cluster).
