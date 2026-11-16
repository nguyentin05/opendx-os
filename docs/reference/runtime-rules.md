---
title: Runtime rules
description: The subset of the manifest and identity contract that services re-check before acting.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Runtime rules

The operator validates the whole manifest before an application becomes `Ready`.
cap-process loads only `Ready` applications whose capabilities are all enabled, and
re-checks what reaches SQL or decides access, failing closed on a corrupted row.

## Rules the services enforce

- Every table is `domain.<identifier>`, declared once; columns are identifiers
  with a supported type and never redefine system columns (`id`, `created_by`,
  `org_unit_id` with cap-hr, `status` on a process table, `version`,
  `created_at`, `updated_at`).
- A command name is declared by exactly one table, and the command runs on that
  table.
- A child table names a parent table of the same application that is not itself a
  child, and has no state machine or attachments; cap-process only reads child
  rows.
- Every access rule and command names a declared role; a scope is `own`,
  `department` (requires cap-hr) or `organization`.
- `recordFilter` uses only `_and`, `_or` and declared columns compared by
  `_eq`/`_neq` with a type-compatible literal, null, or `X-Hasura-User-Id`. Null
  `_eq` means IS NULL, null `_neq` means IS NOT NULL; ordinary SQL NULL
  comparisons do not match.
- Attachments are `uuid` columns with a foreign key to `core.files`; workflow
  effects are `create_work_item` steps on declared commands.
- Role names start with `role_`, so no role can take a name Hasura treats as
  privileged (`admin`); `dxos-authz` never hands one out.

An attachment may point only at a file of the same application that the acting user
uploaded (`core.enforce_attachments`, a trigger on every generated domain table). A
role reads a file's metadata only if its user uploaded it or it is attached to a
record the role may read.

## Identity, roles, and commands

`contracts/fixtures/identity-claims.json` shows the decoded claims of a Keycloak
access token. It is not a signed token. Tokens carry identity only (`sub`, email,
name, audience `dxos-runtime`). The caller names the role they act as in
`X-Hasura-Role`; it is accepted only when `core.user_roles` grants it now, and a
caller holding exactly one role may omit it. Raw `X-Hasura-User-Id` headers never
create authority. Workflow effect calls come from the workflow engine, which
presents the workflow service credential instead of a user token and acts as the
platform's workflow service account ([workflow deployment](api/events.md)).

`dxos-authz` serves two read-only calls for the UI: `GET /api/auth/me` (the
caller's identity and roles) and `GET /api/auth/apps` (the installed applications
the caller holds a role in, with those roles and each application's tables as its
manifest declares them). Neither grants anything. For platform operators
(`role_operator`) it also lists accounts and roles and grants or revokes a role,
each change audited in the `core` chain; see
[integrating the UI](../guides/integrating-the-ui.md).

A command is accepted when:

1. the acting role is allowed by the command;
2. the record is readable by that role, under its scope (own records, the caller's
   department from `cap_hr.staff`, or the whole organization) and its record
   filter;
3. the record satisfies the command's record filter (for example, no one approves
   their own request);
4. its payload holds only the columns the command declares, with every required
   one present and each value fitting its column's type (otherwise 400);
5. version and state match;
6. every attachment the command requires is `CLEAN`.

A record outside the role's scope answers 404, so its existence is not revealed.

Rows of a child table (the lines of a request) are written through GraphQL. Each
change increments the parent record's version, so a command carrying a version read
before the change is a 409; once the parent leaves its editable states the database
refuses further changes.

The status change with the payload's columns, its audit row (actor, acting role and
command), the outbox event and the idempotency receipt commit in one transaction.
A receipt is identified by actor, app, entity, command and `Idempotency-Key`. Its
fingerprint is the SHA-256 of canonical JSON
`{entity_id, expected_version, payload}`. A retry with the same key and request
returns the stored response after re-checking current access; a different request
with the same key is a 409. `command-request.json` and `command-committed.json` are
request and response examples.

`event-retry.json` equals `event-command-committed.json`: transport retries
preserve the event id and payload. The database `outbox_events.id` is the wire
`event_id`. Delivery grants no permission; a workflow effect resolves its source
from the persisted event and the application's declared steps, never from the
caller's body. Each step runs at most once per event
(`cap_process.processed_workflow_steps`). An event goes to the flow the operator
deployed for its application (`core.app_workflows`), never to a destination named
by the event.

## Fixtures and checks

`contracts/fixtures/negative-cases.json` lists cases that must be refused. Those
marked `TESTED` are covered by the cap-process integration tests; the others record
design inputs. Contract tests check that the samples agree:

```bash
python3 -m unittest discover -s tests -p test_machine_contracts.py -v
```

Schema shape alone does not establish a manifest's semantic safety.

Runtime configuration: `DATABASE_URL`, `OIDC_ISSUER_URL`, `OIDC_JWKS_URL`,
`OIDC_AUDIENCE`, `ROLE_CACHE_TTL`, `KESTRA_URL`, `KESTRA_USER`,
`KESTRA_PASSWORD`, `KESTRA_WEBHOOK_KEY`, `WORKFLOW_SERVICE_TOKEN`, `S3_ENDPOINT`,
`S3_BUCKET`, `CLAMAV_ADDRESS`. Database tests require `TEST_DATABASE_URL` and the
`integration` build tag. The UI receives only the public issuer, client, callback
and gateway configuration; service credentials are never exposed.
