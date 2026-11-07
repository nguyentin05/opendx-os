# Backend A1–A6 run and handoff

The backend consumes proposed C0 v1 contracts. Start only after B's SQL, active
registry publication and Hasura grants are reviewed; current compose/platform SQL
is not a sufficient production migration. See [SQL handoff](backend-sql-handoff.md)
and [demo prerequisites](../demo/scenarios.md).

From capabilities/:

```sh
GOCACHE=/tmp/opendx-backend-gocache go build ./cmd/cap-process ./cmd/cap-data ./cmd/cap-ai
GOCACHE=/tmp/opendx-backend-gocache go run ./cmd/cap-process
```

Export DATABASE_URL, OIDC_ISSUER_URL, OIDC_JWKS_URL, OIDC_AUDIENCE,
WORKFLOW_SERVICE_USER_ID, WORKFLOW_SERVICE_ROLE, KESTRA_URL, KESTRA_API_TOKEN,
KESTRA_AUTH_SCHEME, optional KESTRA_ROUTES_FILE,
S3_ENDPOINT, S3_BUCKET, S3_REGION, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY,
CLAMAV_ADDRESS. PORT defaults to 8081; FILES_TIMEOUT defaults to 30s (maximum 5m).
Legacy KESTRA_URL is the complete webhook trigger URL. The integration route-map
adapter and explicit transport auth are defined in the [current contract](../contracts/data-contracts.md#current-workflow-deployment-adapter). Secrets
remain private. S3 requires an existing private bucket and path-style SigV4 support;
ClamAV StreamMaxLength must permit 20 MiB. The process starts publisher/scanner
workers and authenticated command, effect, upload/download routes; /healthz is
liveness, not deployment readiness. Data/AI stubs fail closed and remain deferred.

| Dependency / consumer | B handoff required before live use |
| --- | --- |
| Registry / all process reads | Ready active manifest snapshot, matching state/UI/dependencies; failed deployment preserves prior active row |
| Domain commands | C0 domain DDL/FKs/checks/defaults, version and actor/action audit triggers, restricted trusted transaction context |
| Hasura CRUD | Signed identity/session presets, immutable requester/system fields, editable-state guards, same-workspace filters and restricted columns |
| Receipts/outbox | Full receipt scope/fingerprint, durable lease/delivery columns, transactional audit/event writes and unique constraints |
| Effect steps | Workspace/event/work-item composite FKs, result JSONB, atomic step receipt/work-item insertion; no excessive service grants |
| Files/scanner | App-scoped immutable storage identity, scan retry columns/index, uploader/workspace FK; scanner-only scan updates |
| Runtime DB role | Minimum SELECT/INSERT/UPDATE for owned operations; no arbitrary identity-context authority for ordinary callers; B must review trusted role boundaries |
| External adapters | Real OIDC issuance/JWKS rotation, Kestra plugin/webhook auth, private S3/ClamAV compatibility and restart checks |

Controller verification before A6: complete capabilities
`GOCACHE=/tmp/opendx-backend-gocache go test -race -tags=integration ./... -count=1`
**PASS**, using disposable PostgreSQL and controlled HTTP/TCP/storage adapters.
This is backend transaction/protocol evidence, not live OIDC/Hasura/Kestra/
SeaweedFS/ClamAV acceptance. Actual services and runtime acceptance remain NOT_RUN.

A3 process recovery now has integration/race evidence from
`TestPublisherRecoversAfterProcessCrash`: a helper OS process runs the production
publisher against a disposable PostgreSQL database; the controlled HTTP receiver
reads the event and withholds acknowledgement, then the parent kills and waits
for that process. A new OS process uses the same database without resetting the
fixture, redelivers the identical event ID/body, and records durable delivery on
attempt two. The test expires only the persisted lease deadline to avoid waiting
60 seconds; actual process termination/restart is not simulated. This closes the
module-level publisher recreation evidence gap, not live Kestra acceptance or
an end-to-end cap-process deployment restart check.

Available Operator command is plan, not validate/apply. Running
`GOCACHE=/tmp/opendx-backend-gocache go run ./cmd/dxos-operator plan ../apps/APP/manifest.yaml`
from operator/ on procurement, process-fixture, maintenance and timesheet fails
at decoding AttachmentsDef/CapabilityRefDef (sequence/string versus old structs).
There is no C0 v1 B validator here. Do not interpret hygiene or fixture consistency
as schema/semantic validation PASS, generated plan approval, or Ready publication.
