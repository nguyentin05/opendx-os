# Frontend API integration handoff

> Superseded by [integrating the UI](../development/ui-integration.md). The text
> below describes the integration branch of early November and no longer matches
> the manifest shape, the token contents or the routes.

Status: **partial handoff; App Shell discovery is blocked**. The integrated known-app
APIs are qualified separately in [final evidence](integration-evidence.md). There
is no authenticated frontend registry discovery API on this revision: the real
`core_app_registry` GraphQL query returns HTTP 200 with `validation-failed` because
the field is absent from `query_root`. Do not supply a Hasura admin secret to a
browser. Platform/Operator and App Shell owners must agree and implement a
workspace/role-filtered discovery interface before dynamic navigation is ready.
The existing UI is scaffolding with empty apps and obsolete command bodies;
UI implementation, tests and build are outside this gate.

The authoritative interfaces are [runtime OpenAPI](../../contracts/runtime-v1.openapi.yaml),
[manifest schema](../../contracts/manifest-v1.schema.json),
[event schema](../../contracts/events-v1.schema.json), and
[data contracts](../contracts/data-contracts.md). Examples below describe the
supported disposable deployment; they introduce no replacement schema.

## Endpoints and context

| Purpose | Verified local URL | Browser use |
| --- | --- | --- |
| Gateway GraphQL | `http://localhost:19080/v1/graphql` | Intended API route |
| Gateway process/files | `http://localhost:19080/api/cap/process/` | Intended API route |
| Keycloak realm/issuer | `http://localhost:18085/realms/dxos` | Canonical login/token issuer |
| Direct Hasura | `http://localhost:18080/v1/graphql` | Operational/debug HTTP; browser GraphQL also observed |
| Direct process | `http://localhost:18091/api/cap/process/` | CLI/service only; no direct browser CORS support |
| Browser origin | `http://localhost:15173` | Allowed explicitly by gateway CORS |
| Process liveness | `http://localhost:18091/healthz` | Does not establish dependency readiness |

APISIX permits Authorization, Content-Type, X-Hasura-Role and Idempotency-Key
preflights from that exact browser origin. Different ports/origins require an
explicit deployment configuration change. Actual Chromium evidence includes both
preflighted API calls and an authorized CLEAN download with byte/hash comparison.
SeaweedFS, database and Kestra administration are not browser API surfaces.

Use the actor's signed workspace/employee/subject claims and select only a granted
`X-Hasura-Role`. Procurement uses app ID `procurement`, requester role
`role_procurement` and manager role `role_manager_procurement`; process-fixture uses app ID
`process_fixture` and `role_review`. These two actual packages are published in
alpha and beta. Changing app/role/workspace must clear old selections, records,
versions and pending request/cache results. A workspace header cannot override a
signed token's workspace. Gateway strips forged identity/admin headers.

Follow [setup](integration-setup.md) and [identity](identity-runtime.md). Private
`.env.integration` contains local port/configuration values and generated secrets;
`build/integration/actor-credentials.json`, `tokens.json`, `token-expiry.json` and
`connections.json` remain ignored mode 0600. For local fixtures acquire/renew:

```sh
python3 scripts/integration/identity.py provision
python3 scripts/integration/identity.py tokens
```

Load only the chosen `TEST_*_TOKEN` into a caller; never log token files or pass
bootstrap/admin credentials to frontend code. Renew before `renew_before` and
reload the caller. Browser login uses public client `dxos-portal`, runtime audience
`dxos-runtime`, and the canonical issuer above. Fixture password issuance is a
local test setup, not a production login implementation.

## Known-app CRUD and commands

These payloads were exercised through actual authenticated Hasura/process calls.
Obtain `work_item_id` from authorized shared master data; bootstrap creation of
master data belongs to setup, not an unrestricted browser mutation.

```graphql
mutation Draft($object: domain_procurement_requests_insert_input!) {
  insert_domain_procurement_requests_one(object: $object) {
    id status version
  }
}
```

```json
{"object":{"work_item_id":"AUTHORIZED_WORK_ITEM_UUID","vendor_name":"Integration supplier","item_description":"Integration item","quoted_amount":42}}
```

The server presets workspace/requester and DRAFT/version. Do not insert protected
state, requester, workspace, version or audit fields. Attach an uploaded CLEAN
quote to an editable draft using the current GraphQL input schema:

```graphql
mutation Quote($id: uuid!, $file: uuid!) {
  update_domain_procurement_requests_by_pk(pk_columns: {id: $id},
    _set: {quote_file_id: $file}) { id status version }
}
query Status($id: uuid!) {
  domain_procurement_requests_by_pk(id: $id) { id status version quote_file_id }
}
```

Always consume returned version after every edit. GraphQL HTTP 200 can contain
`errors`; filtered reads can return null/empty data and writes can affect zero
rows. Check errors, data and affected rows before presenting success.

Upload multipart `POST /api/cap/process/files/upload` with fields `app_id` and
`file`, the bearer token and selected role. Do not manually set a multipart boundary
when using browser FormData. The result includes a fresh `file_id` and initial
`scan_status`; every upload receives a new immutable identity. Files allow only
supported MIME types and at most 20 MiB. `GET /api/cap/process/files/{file_id}`
returns original bytes only after authorized CLEAN scanning; quarantine/infection
returns 422, denied context 403 and storage unavailability 503. There is **no
separate public scan-status polling endpoint** on this revision. Seed/test tooling
observes scan status via its private read-only assertion connection; frontend may
retry authorized download with bounded backoff and must not infer CLEAN from
upload success. A richer status UI requires an agreed API, not DB credentials.
Replacing content requires a new upload and a new scan.

Submit (requester) and approve (different manager) use:

```http
POST /api/cap/process/commands/procurement/submit
Authorization: Bearer <chosen caller token>
X-Hasura-Role: role_procurement
Idempotency-Key: <fresh UUID saved for this logical attempt>
Content-Type: application/json

{"entity_id":"RECORD_UUID","expected_version":2,"payload":{}}
```

Approval changes the final path to `/approve`, uses the manager's token and
`role_manager_procurement`, and sends the latest returned version. A successful response
contains `entity_id`, `status`, `version`, and `executed_at`. Preserve the exact key
and canonical body for uncertain-response retry; a committed retry returns its
recorded response after a current permission check. Reusing a key with changed
request contents conflicts. A new logical operation needs a new key and current
version. Concurrent equal-version commands permit one transition only.

| Response | Caller behavior |
| --- | --- |
| 401 | Acquire valid signed token; do not retry indefinitely |
| 403 | Permission, workspace or requester-separation denial; no disclosure/retry escalation |
| 404 | Missing/inaccessible entity or attachment context; do not assume version information |
| 409 | Stale version or conflicting key/body; refresh record and decide a new operation |
| 422 | Missing, quarantined or infected required attachment; fix attachment before submit |
| 503 | Capability/dependency unavailable; preserve uncertain command key/body and retry boundedly |

## Workflows and unsupported surfaces

Manual acceptance freezes Kestra and ClamAV for isolated counts/quarantine checks.
It does not demonstrate automatic effects. The separate automatic phase deploys
both real manifest workflows on Kestra and verifies persisted delivery/retry and
one work effect. Automatic workflows are **alpha-only**. Beta CRUD/commands work,
but beta automatic events stay pending/retryable without an authorized route;
beta automatic availability is NOT_READY. Workflow credentials are not frontend
credentials. Explicit local renewal is:

```sh
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.workflows renew
```

That reloads fresh signed effect credentials and routes into Kestra/publisher.
Kestra webhook keys authenticate actual webhook delivery; Basic authenticates
its admin API. Do not mistake Basic admin access for the webhook boundary.

Maintenance and timesheet remain unsupported because their existing relationship
contracts fail the strict parser; their business behavior has not been replaced
with fixtures. Data/OT/BI/export/AI/OCR/RAG success is unavailable/deferred and must
not be shown as working. S02/S07/S08, production migration, clean-machine install,
release artifacts and backup/restore are outside this handoff. See
[compatibility dispositions](integration-compatibility.md) and
[final assertion evidence](integration-evidence.md) for precise limits and owners.
