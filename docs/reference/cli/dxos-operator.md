---
title: dxos-operator CLI
description: Commands for planning, installing, reconciling, and verifying applications.
diataxis: reference
audience: [operator, platform-developer]
status: draft
owner: platform
last_reviewed: 2026-11-16
---

# dxos-operator CLI

The intended Operator interface includes `plan`, `apply`, `reconcile`, `uninstall`,
and `verify-audit`. `plan` has no side effects. Configuration progresses through
`Pending -> Applying -> Ready | Failed`, with manifest versions and structured
errors.

Check the actual command set before use; it is not fully implemented.

```bash
dxos-operator --help
dxos-operator plan --help
dxos-operator apply --help
dxos-operator reconcile --help
```

On Kubernetes, `dxos-operator controller` reconciles cluster-scoped
`DXApplication` resources with the same planner and applier as the CLI and reports
progress in status conditions. See
[installation](../../operations/installation.md#kubernetes-operator-development-cluster).
