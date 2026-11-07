# Proposed backend SQL requirements for B

Status: **PROPOSED; B acceptance and implementation pending**. B owns all platform
SQL and Operator migrations; this document changes no DDL. Inventory reviewed:
`platform/postgres/init/01_core_schema.sql`, `03_app_registry.sql`, and
`platform/postgres/capabilities/cap_process.sql`. Consumers are A1 registry,
A2 command transactions, A3 delivery/effect transactions and A4 files.

| Surface | Existing inventory | Proposed requirement / ownership |
| --- | --- | --- |
| Registry | workspace_id/app_id PK; display_name/version/role_name/domain_table/manifest_spec/ui_schema/state_machine/dependent_capabilities/status/last_error/updated_at | B publishes one coherent Ready active snapshot atomically. A1 reads only Ready. Deployment-attempt state must be separate, never overwrite an old active row with Pending/Applying/Failed. |
| Receipts | Full workspace/actor/app/entity/command/key PK, payload_hash, response_status/body, created_at | Retain these columns; serialize scope before lookup/update. A2 rechecks current permission before returning response. Hash full canonical request (README), not payload alone. No receipt for rollback/failure. |
| Outbox | id, workspace/app/type/entity/version/payload/created_at; unique workspace/id and source version/type | B adds durable delivery fields below; A3 implements claim/retry/ack. |
| Processed steps | event_id FK, step_name, outcome, processed_at; PK event/step | Add workspace_id, work_item_id, result JSONB; composite FKs below. A3 owns atomic effect/result writes. |
| Audit/version | Core bump function; audit hash/immutability triggers | B generates domain BEFORE UPDATE version/time trigger plus domain audit insertion trigger. A2 sets validated transaction-local context before UPDATE; never manually insert duplicate audit. |
| Files | immutable identity metadata, hash, uploader, scan state/time, unique bucket/key | B/A agree app-scoped authorization storage below; A4 owns immutable upload/scan transitions. |

## Durable delivery columns

Propose adding to `core.outbox_events`: `delivery_status TEXT NOT NULL DEFAULT
'pending' CHECK IN ('pending','in_flight','delivered')`, `attempt_count INTEGER
NOT NULL DEFAULT 0 CHECK >= 0`, `next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
`lease_token UUID NULL`, `lease_until TIMESTAMPTZ NULL`, `delivered_at TIMESTAMPTZ
NULL`, `last_error TEXT NULL`. Enforce paired lease token/time for in_flight;
delivered requires delivered_at and no active lease. Index pending due time and
expired leases. Preserve identity, payload and all original event source columns.

A3 claims due/expired rows with locking (e.g. FOR UPDATE SKIP LOCKED) in a short
transaction, increments attempts and persists a new token/expiry before network
I/O. Acknowledge/reschedule only with matching lease token; stale workers cannot
ack a newer claim. Failed/lost acknowledgement schedules bounded backoff without
changing event ID; a crash reclaims after lease expiry. No network call holds a
command transaction open. Event retention must preserve processed-step FKs.
The one-destination core contract is insufficient for independent subscribers;
B/A must agree a per-destination delivery table before adding fan-out.

## Work-item effect result and source association

`core.processed_workflow_steps` gains NOT NULL workspace_id, work_item_id UUID and
result JSONB for successful create_work_item results (stored
`{"work_item_id":"UUID"}`). Retain event_id/step_name uniqueness, processed_at and
outcome; only a successful atomic effect is stored. Require
`(workspace_id,event_id) REFERENCES core.outbox_events(workspace_id,id)` and
`(workspace_id,work_item_id) REFERENCES core.work_items(workspace_id,id)`; both
columns NOT NULL. Source app/entity/version is recovered through the persisted
event, avoiding a polymorphic unvalidated client association. Lock/serialize the
event/step scope; create work item and processed-step result in one transaction.
A rollback leaves neither; duplicate invocation returns the stored result after
service/workspace authorization. No partial `processing` result counts as success.

## Audit and file access gaps

Audit insertion must derive actor/action from authenticated Hasura `hasura.user`
or validated transaction-local `dxos.actor_id` / `dxos.action`. Reject missing or
malformed context for domain writes; ordinary DB callers cannot set arbitrary
trusted context. B must define restricted connection/function privileges. Version
trigger increments once; audit uses resulting version. Recognized retries and
failed commands perform no UPDATE and produce no new audit/event.
The existing audit hash trigger hashes inserted rows but does not create a domain
update audit record. External anchoring remains deferred; no new security claim.

Existing file_objects has no app_id. Propose NOT NULL app_id for new uploads and
a same-workspace/app reference to active app identity (retention-safe registry key
policy needs B review). Authorization must cover both newly uploaded owner files
and readable declared attachments, before scan-state disclosure. Preserve uploader
same-workspace FK and immutable bucket/key/hash/content; only trusted scanner
writes scan status. B must decide migration/backfill for existing unscoped files;
A must not infer cross-app read permission from ownership alone. MIME/size may be
stored for consistency but are validated against bytes by A4, not client metadata.

## Review and migration boundary

Do not mark these names frozen until B accepts column/check/index names, migration
strategy, deployment-attempt storage (proposed separate core.app_deployment_attempts
with attempt UUID, workspace/app, requested version, status, last_error and times),
file app scope and runtime DB privileges. Active publication occurs after all
DDL/Hasura/workflow validation succeeds; failed attempt preserves the prior active
snapshot. Real PostgreSQL concurrency, Hasura permission and crash/restart tests
remain NOT_RUN until B's service/schema handoff. Fixture agreement is static only.

## A3 implemented consumer requirements

The backend publisher now consumes the proposed delivery column names above. It
claims one event atomically using `FOR UPDATE SKIP LOCKED`, uses 60-second leases,
20-second delivery timeouts, a maximum batch of 1000 and bounded exponential retry
(2 to 1024 seconds). Token **and unexpired lease** gate both acknowledgement and
rescheduling. Payloads above 1 MiB fail delivery; responses are drained up to
64 KiB and oversized responses fail acknowledgement. Errors persisted/logged are
generic and exclude endpoints, response bodies and credentials.

Effects require `core.employees(workspace_id,id)` for configured assignment lookup
and the existing work-item same-workspace employee FK. The work-item insert uses
`id,workspace_id,title,description,priority,assigned_employee_id`; B must ensure
other required columns have defaults. Successful receipts use outcome `success`.
Production migrations remain B-owned; integration fixtures install receipt
composite FKs but do not establish production migration readiness.

`cap-process` requires explicit `WORKFLOW_SERVICE_USER_ID` (UUID) and
`WORKFLOW_SERVICE_ROLE` (role identifier), in addition to existing signed OIDC
identity verification. No role is implicitly trusted. The signed caller must match
both, include the role in its signed grants, and match the persisted event's
workspace. The active Ready app, enabled dependencies, subscribed event and
literal effect command mapping are checked again before cached results.

Legacy endpoint mode uses a complete `KESTRA_URL` and Bearer credential.
Integration additionally supports deployment-owned single-destination route maps
and explicit Basic transport authentication; see the current
[workflow adapter contract](../contracts/data-contracts.md#current-workflow-deployment-adapter)
and [deployment/renewal setup](integration-setup.md). Publisher payload identity,
`Idempotency-Key`, redirect rejection and persisted retries remain unchanged.
Transport configuration is private and separate from the signed effect token.

A workflow HTTP task must invoke the effect API with a signed token for that
configured principal, for example (verified against Kestra 0.20.7 in the alpha integration phase):

```yaml
- id: create_review
  type: io.kestra.plugin.core.http.Request
  uri: "{{ secret('PROCESS_BASE_URL') }}/api/cap/process/effects/work-item"
  method: POST
  contentType: application/json
  headers:
    Authorization: "Bearer {{ secret('WORKFLOW_SERVICE_ACCESS_TOKEN') }}"
  body: |
    {"event_id":"{{ trigger.body.event_id }}","step_name":"create_review"}
```

The service access token must carry the event workspace and configured role. The
HTTP request owns no app/entity/title/assignment fields. The app's deployed effect
mapping owns those literal fields. Token issuance/refresh and real workflow YAML
publication are B4/A6 handoffs; a static example token is not authentication.

## A4 implemented immutable file consumer

A4 now requires these exact `core.file_objects` columns: existing `id UUID`,
`workspace_id UUID`, `bucket TEXT`, `object_key TEXT`, `file_name TEXT`,
`sha256_hash TEXT`, `uploaded_by UUID`, `scan_status TEXT`, `scanned_at TIMESTAMPTZ`,
`created_at TIMESTAMPTZ`; plus proposed `app_id TEXT NOT NULL`,
`mime_type TEXT NOT NULL CHECK IN ('application/pdf','image/png','image/jpeg')`,
`size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520)`.
Retain unique `(bucket,object_key)`, same-workspace uploader FK and attachment FK.
Add `scan_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (scan_attempt_count>=0)`
and `scan_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()`. A partial index on
`(bucket,scan_next_attempt_at,created_at,id)` where scan_status='QUARANTINED' supports
fair due selection and retries.
Production migrations, retention/backfill, grants and immutable-column triggers
remain B-owned and **PROPOSED / NOT_RUN**. B must deny ordinary file metadata
writes and scan-state updates; only the private trusted scanner connection may
change scan_status/scanned_at and retry scheduling fields, with all identity/content
metadata immutable.
The current service uses its private DB connection for upload and scanning; B must
agree the corresponding least-privilege deployment role rather than exposing it
to Hasura/user SQL. No HTTP endpoint changes content, scan state or existing IDs.

Upload authorizes the signed workspace, selected Ready app, enabled dependencies
and a current deployed domain grant for the signed selected role. Each upload
creates a fresh UUID/key `<workspace>/<app>/<UUID>`, stores only sniffed PDF/PNG/JPEG
bytes at most 20 MiB, and hashes the exact bounded content. Client extension and
MIME never grant authority. The success body always returns QUARANTINED, even if a
worker races after insertion. SQL insertion failure can leave an unreachable
orphan object; B should define retention/garbage collection rather than reusing it.
The storage gateway/bucket and credentials remain private. No filesystem fallback
is implemented. Storage retention/credentials must prevent external content
mutation. Downloads additionally verify the whole bounded object's SHA-256 before
releasing any bytes, so post-scan replacement cannot release altered content.

Download first authorizes workspace/app and either uploader ownership with a
current app grant, or a declared attachment column included in a matching current
SELECT grant whose recordFilter matches the attached row. Status is queried only
after authorization. Only CLEAN bytes download; QUARANTINED/INFECTED return 422
for authorized users, while unauthorized requests return the same generic 403.
A2 retains its locked same-workspace/app CLEAN attachment command checks.
The shared strict filter evaluator preserves SQL null, typed UUID and lossless
numeric comparison semantics across commands and file access.

The scanner retries QUARANTINED rows, reads verified immutable bytes and sends
those same bytes through ClamAV INSTREAM. Scanner errors/timeouts, malformed
responses and hash mismatch remain unclean. A result updates status only with
matching ID/bucket/key/hash and the complete immutable metadata snapshot (excluding
scan status/time and retry scheduling), while the row remains QUARANTINED. A short
atomic `FOR UPDATE SKIP LOCKED` claim orders due rows by next-attempt time, creation
time and ID; before network I/O it increments attempts and schedules bounded
exponential retry at 2, 4, 8, ... 256 seconds. Failed/hash-mismatched objects stay
unclean and move behind other due uploads, so a permanently failing prefix cannot
starve later uploads. Cancellation or process crash leaves the persisted deadline
reclaimable when due. No scan claim holds a database transaction open during network
I/O; concurrent workers may duplicate slow scans after the retry deadline, with
only a still-matching quarantined row eligible for an update.

Private startup configuration now additionally requires `S3_REGION`,
`S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, alongside `S3_ENDPOINT` (HTTP(S) root
endpoint), `S3_BUCKET` (existing private bucket), and `CLAMAV_ADDRESS` (`host:port`).
`FILES_TIMEOUT` is an optional Go duration, default 30s, maximum 5m; storage requests
and scanner operations have bounded deadlines and cancellation. S3 uses path-style
AWS Signature V4 without redirects; no public URLs or credentials appear in API
responses. ClamAV uses `zINSTREAM` with network-byte-order chunk lengths and a
zero-length terminator. B must configure StreamMaxLength >= 20 MiB and verify
actual gateway signature compatibility, scanner definitions/readiness and private
network restrictions. Startup rejects malformed/missing configuration; worker
failures log only generic messages. Upload/download routes use the existing OIDC
middleware; scanner and outbox loops shut down with the process context.

Protocol references: [AWS Signature V4 header authentication](https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html)
and [ClamAV INSTREAM protocol](https://docs.clamav.net/manual/Usage/ClamdProtocol.html).
Local HTTP/TCP fixtures establish framing and signed request shape, not live
SeaweedFS or ClamAV compatibility. PostgreSQL lifecycle tests use isolated test
schemas and controlled adapters, not production migrations or live scanners.

Real services are opt-in through
`go test -tags=serviceintegration ./internal/files -run TestRealSeaweedFSClamAV -count=1`
with all private S3/ClamAV variables above. Missing prerequisites fail explicitly;
these external-service tests are excluded from `-tags=integration`. The opt-in
check covers clean bytes, harmless EICAR scanner detection and object retrieval
from a newly constructed client. Actual service outage/restart, production schema
immutability/privileges, and real persisted-object restart acceptance require B4
and remain **NOT_RUN**; client reconstruction is not a server restart claim.
