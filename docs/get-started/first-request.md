---
title: Complete a first request
description: Create, submit, and approve a purchase request through the runtime API.
diataxis: tutorial
audience: [evaluator, app-author]
status: active
owner: platform
last_reviewed: 2026-11-23
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

This creates three departments and six Keycloak accounts. The demo password is
`DEMO_PASSWORD` (default `Demo@2026`), and the command is safe to re-run.

| Account | Department | Roles |
| --- | --- | --- |
| `an.nguyen` | Kinh doanh | procurement requester, timesheet employee |
| `cuong.le` | Kinh doanh (head) | procurement requester and manager, timesheet manager |
| `binh.tran` | Kỹ thuật | procurement requester, maintenance technician, timesheet employee |
| `dung.pham` | Kỹ thuật (head) | procurement manager, maintenance manager, timesheet manager |
| `giang.vo` | Tài chính | procurement finance, timesheet employee |
| `quan.tri` | Tài chính | platform operator, auditor, HR administrator |

When cap-ai is enabled, the five business accounts also get the personal library
role.

## Run the approval flow

The fastest path runs one request through the whole chain and prints each step:

```bash
scripts/seed-demo.py flow
```

The script:

1. files a purchase request as `an.nguyen`, with a quote uploaded to
   cap-process and scanned by ClamAV;
2. shows that the manager cannot act on a draft (`404`);
3. submits it with a versioned command, and shows the review work item that the
   workflow opens in Kestra;
4. shows the refusals: a manager of another department (`404`) and the
   requester approving their own request (`403`);
5. approves it as the department manager (`BUDGET_REVIEW`), then as finance
   (`APPROVED`);
6. on a second request, refuses a rejection without a reason (`400`), rejects
   it with one, and shows the requester reading the reason.

Other demo scenarios:

```bash
scripts/seed-demo.py scopes   # what each person sees through the gateway
scripts/seed-demo.py admin    # grant and revoke a role
scripts/seed-demo.py org      # add a department and move a person
scripts/seed-demo.py lines    # vendor list and request line items
```

## Inspect the result

Open the Workspace UI at <http://localhost:5173> and sign in as a demo user, for
example `an.nguyen`. Under **Mua sắm vật tư** the approved request shows its
state, version and **Lịch sử** (history): created, submitted, approved by the
department and approved by finance. `cuong.le` sees requests waiting for them
in **Chờ tôi xử lý** and their review items in **Việc của tôi**.

The rows themselves are in PostgreSQL:

```bash
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -c "SELECT status, version FROM domain.procurement_requests"'
```

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
