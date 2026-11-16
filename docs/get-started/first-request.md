---
title: Complete a first request
description: Create, submit, and approve a purchase request through the runtime API.
diataxis: tutorial
audience: [evaluator, app-author]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Complete a first request

This walkthrough uses the demo organization and the procurement sample
application. It exercises real sign-in, file upload, scanning, GraphQL writes, and
versioned commands through the gateway.

## Seed the demo organization

After the stack is healthy and the Operator has installed the sample apps:

```bash
scripts/seed-demo.py seed
```

This creates three departments and six Keycloak accounts with procurement,
maintenance, and timesheet roles. The demo password is `DEMO_PASSWORD` (default
`Demo@2026`). The command is safe to re-run.

## Run the approval flow

The fastest path runs one request through the whole chain and prints each step:

```bash
scripts/seed-demo.py flow
```

The script:

1. files a purchase request with a quote uploaded to cap-process;
2. submits it with a versioned command;
3. shows a stale-version refusal and a self-approval refusal;
4. approves it as the department manager, then as the budget manager;
5. shows the workflow review that the committed event opens in Kestra.

Other demo scenarios:

```bash
scripts/seed-demo.py scopes   # what each person sees through the gateway
scripts/seed-demo.py admin    # grant and revoke a role
scripts/seed-demo.py org      # add a department and move a person
scripts/seed-demo.py lines    # vendor list and request line items
```

## Inspect the result

Open the App Shell at <http://localhost:5173> and sign in as a demo user. The
record's state, version, and audit row should match the script output. In Hasura
(<http://localhost:8080>) you can inspect `core.audit_logs` and the generated
`domain.procurement_requests` row.

## What you just used

| Step | Contract |
| --- | --- |
| Sign-in and role selection | [Identity, roles, and scope](../reference/data-contracts/identity-and-roles.md) |
| Record write and version | [Writes, versions, and audit](../reference/data-contracts/writes-and-versions.md) |
| Command and state transition | [Manifest schema](../reference/manifest/schema.md) |
| Workflow event | [Workflow deployment and events](../reference/api/events.md) |
| File quarantine | [Files, OT, and BI](../reference/data-contracts/files-ot-bi.md) |

Continue with the [guides](../guides/index.md) to add an application, or the
[architecture](../architecture/index.md) to understand the tiers.
