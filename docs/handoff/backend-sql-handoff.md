# Backend SQL integration contract and historical requirements

Status: reviewed implementations are integrated for the supported disposable
apps; see [compatibility dispositions](integration-compatibility.md) and
[final evidence](integration-evidence.md). The inventory/proposal descriptions
below retain original design context; they no longer mean all fields are pending.
SQL currently includes durable delivery leases, workspace/result step fields,
composite FKs, file retry/identity guards and trusted audit/version context.
Runtime roles are provisioned separately and are restricted. Existing-database
migration/backfill and retention/fan-out policy remain outside this handoff.

| Surface | Existing inventory | Integrated requirement / ownership |
| --- | --- | --- |
| Registry | workspace_id/app_id PK; display_name/version/role_name/domain_table/manifest_spec/ui_schema/state_machine/dependent_capabilities/status/last_error/updated_at | B publishes one coherent Ready active snapshot atomically. A1 reads only Ready. Deployment-attempt state must be separate, never overwrite an old active row with Pending/Applying/Failed. |
| Receipts | Full workspace/actor/app/entity/command/key PK, payload_hash, response_status/body, created_at | Retain these columns; serialize scope before lookup/update. A2 rechecks current permission before returning response. Hash full canonical request (README), not payload alone. No receipt for rollback/failure. |
| Outbox | id, workspace/app/type/entity/version/payload/created_at; unique workspace/id and source version/type | Durable delivery fields below are implemented; publisher implements claim/retry/ack. |
| Processed steps | event_id FK, step_name, outcome, processed_at; PK event/step | workspace_id, work_item_id, result JSONB and composite FKs below are implemented. A3 owns atomic effect/result writes. |
| Audit/version | Core bump function; audit hash/immutability triggers | B generates domain BEFORE UPDATE version/time trigger plus domain audit insertion trigger. A2 sets validated transaction-local context before UPDATE; never manually insert duplicate audit. |
| Files | immutable identity metadata, hash, uploader, scan state/time, unique bucket/key | B/A agree app-scoped authorization storage below; A4 owns immutable upload/scan transitions. |

## Durable delivery columns

Implemented on the disposable schema in `core.outbox_events`: `delivery_status TEXT NOT NULL DEFAULT
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

`core.processed_workflow_steps` implements NOT NULL workspace_id, work_item_id UUID and
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

## Audit and file access boundaries

Audit insertion must derive actor/action from authenticated Hasura `hasura.user`
or validated transaction-local `dxos.actor_id` / `dxos.action`. Reject missing or
malformed context for domain writes; ordinary DB callers cannot set arbitrary
trusted context. Restricted connection/function privileges are provisioned; session_user gates trusted context. Version
trigger increments once; audit uses resulting version. Recognized retries and
failed commands perform no UPDATE and produce no new audit/event.
Generated domain triggers append update audit records, then the core audit hash trigger hashes inserted rows. External anchoring remains deferred; no new security claim.

File objects carry NOT NULL app_id for new uploads. Authorization resolves the
current Ready app, owner/declared attachment permissions and workspace before
scan-state disclosure. Uploader FK and immutable storage/content metadata are
implemented; only the trusted process/scanner connection updates scan/retry
columns. The retention-safe registry FK policy and migration/backfill of existing
unscoped data remain separate design/deployment work.

## Review and migration boundary

The disposable schema and supported-package publication are qualified in
[final evidence](integration-evidence.md). Deployment attempts use
`core.app_deployments`, separate from active Ready rows. Failed deployment retains
the previous active snapshot. Real PostgreSQL concurrency, Hasura permissions and
service recovery are individually attributed there; no production migration or
backfill is claimed. Do not apply this setup to an existing/shared database.

## A3 implemented consumer requirements

The backend publisher now consumes the implemented delivery column names above. It
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
Disposable grants and immutable-column triggers are implemented and exercised.
Production migration and retention/backfill remain NOT_RUN. Ordinary file metadata
writes and scan-state updates are denied; only the private trusted scanner connection may
change scan_status/scanned_at and retry scheduling fields, with all identity/content
metadata immutable.
The current service uses its private DB connection for upload and scanning; the
restricted dxos_process role owns this service boundary, and its credential must
not be exposed to Hasura/user SQL. No HTTP endpoint changes content, scan state or existing IDs.

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
zero-length terminator. The integration config sets 25 MiB stream/file and 50 MiB
expansion limits and verifies actual SigV4, scanner readiness and 20 MiB content;
production network restrictions
remain deployment work. Startup rejects malformed/missing configuration; worker
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
check covers clean bytes, 20 MiB content, harmless EICAR scanner detection and object
retrieval from a newly constructed client. Actual disposable service outage/restart,
schema immutability/privileges and persisted-object recovery are separately qualified
by the live file suite in
[final evidence](integration-evidence.md). Production migration remains NOT_RUN;
client reconstruction alone is not a server restart claim.
