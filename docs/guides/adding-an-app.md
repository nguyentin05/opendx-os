---
title: Adding an application
description: Declare a Tier 3 application package and register its data and roles.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Adding a declarative application

An application is a package of declarations: no backend code and no change to
Tier 1 or Tier 2. The [manifest schema](../reference/manifest/schema.md) is the
contract; `apps/procurement` is the fullest sample (a process table, a plain
table, a child table, attachments, a workflow and knowledge), and
`apps/process-fixture` the smallest one with a workflow.

## 1. Lay out the package

```text
apps/<app>/
├── manifest.yaml          # required
├── workflows/<flow>.yaml  # optional Kestra flows named in spec.workflows
└── knowledge/*.md|*.pdf   # optional sources named in spec.knowledge (needs cap-ai)
```

## 2. Declare the manifest

1. `metadata`: `name` becomes the `app_id` (lowercase letters, digits and `_`),
   plus `displayName` and `version`.
2. `spec.capabilities`: always `cap-process`. Add `cap-hr` for departments and
   the `department` scope, `cap-work` for work items, and `cap-ai` for knowledge.
3. `spec.roles`: every role the app uses, named `role_<app>_<role>` by
   convention, with a Vietnamese `description` that the Workspace UI shows.
4. `spec.tables`, for each table:
   - its business columns and foreign keys;
   - per-role `access` rules with a scope (`own`, `department`, `organization`)
     and optional `recordFilter`;
   - `ui` (columns, form fields, labels).
   A process table adds a `stateMachine` (and `attachments` for files that must
   be scanned clean). Rows that are part of another record, such as the lines of
   a request, go in a child table with `belongsTo`.
5. `spec.sharedAccess`: the shared columns a role reads, such as
   `core.users.display_name`.
6. `spec.workflows`, if commands should open or close work items: see
   [running workflows](running-workflows.md).

Keep thresholds and business handling in declarations and workflows. Adding an
app must not add an app-name branch to shared capabilities, the UI or bootstrap
DDL (`make check-architecture` refuses it).

## 3. Validate and review the plan

```bash
go -C operator run ./cmd/dxos-operator validate ../apps/<app>/manifest.yaml
go -C operator run ./cmd/dxos-operator plan ../apps/<app>/manifest.yaml
go -C operator run ./cmd/dxos-operator plan -format hasura ../apps/<app>/manifest.yaml
```

`validate` and `plan` change nothing. Review the generated DDL and the Hasura
permissions of each role before installing.

## 4. Install

- **Compose:** copy the package into `apps/` (or `OPERATOR_APPS_DIR`). The
  `operator` service installs it on its next pass, at most
  `OPERATOR_RECONCILE_INTERVAL` (default `30s`) later. Follow it with
  `docker compose logs -f operator`. Editing the package upgrades the app, and
  removing it uninstalls the app.
- **Without Compose:** `dxos-operator apply apps/<app>/manifest.yaml` with the
  [operator environment](../reference/cli/dxos-operator.md#environment).
- **Kubernetes:** `dxos-operator export-crd apps/<app> | kubectl apply -f -`
  ([installation](../operations/installation.md#kubernetes-operator-development-cluster)).

The app is installed when its `core.apps` row is `Ready`. A refusal is recorded
in `last_error`, and an upgrade that cannot be applied leaves the previous
version serving ([lifecycle](../reference/manifest/lifecycle.md)).

## 5. Grant roles and verify

1. A platform operator grants the app's roles to users
   ([administration](integrating-the-ui.md#8-administration)), and an HR
   administrator places users in departments when the app uses cap-hr.
2. The Workspace UI shows the app from the registry, with no rebuild.
3. Verify each role's scope, the initial state, the commands and their
   refusals, a stale version (`409`), attachment checks, retry-safe workflow
   effects, and that disabling a capability the app needs takes it out of
   service. Reconciling the app must not change other apps' grants.

To test pluggability, write an app beyond the samples: combine user reads, file
attachments, the department scope and a workflow, and check that a repeated
event does not repeat business work. Record evidence with
[verification](../contribute/verification.md).
