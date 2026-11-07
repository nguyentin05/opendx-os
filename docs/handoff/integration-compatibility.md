# Backend/platform integration compatibility

This is the Gate 0 inventory for the isolated `integration/backend-platform`
branch. It records source inspection and executed baseline checks, not runtime
acceptance or frontend readiness. Requirements remain in [architecture](../architecture/README.md),
[data contracts](../contracts/data-contracts.md), [security](../contracts/security-and-acceptance.md),
and [verification](../development/verification.md).

## Pinned baseline and merge

- Main: `3b81e3c0e17e249e4e78b2cbef628b6d544cf854`.
- Platform: `70c29046cfd723d63be36860b9eecce8308f6864`.
- Backend: `d4cc99978725d809b4c1503e0090c6fa55c32654`.
- Platform merge: `1dfa3f7e94383bde109316216b13037ad4b84460`.
- Backend merge / checked combined code: `0a5e3c9b4380276d81e8d4cbafbcc044f6c8d6c7`.

All branch tips matched these pins. Both branches share pinned main as their
merge base. The existing native worktree was clean and detached at pinned main;
the integration branch was created there, platform merged first, backend second.
Both merges completed without conflicts. No source branch, main, or remote was
modified. The approved design and plan were already copied into the ignored
`docs/superpowers/` scratch area; they are not release artifacts.

The two pinned branches have byte-identical `contracts/` trees (`git diff
70c2904 d4cc999 -- contracts` is empty). Semantic inspection found the same
manifest-v1 fields, canonical command request/response, event identity/retry
fixtures, and Ready registry envelope on both sides. Backend app changes and
platform strict parser/generator changes therefore coexist without a textual
contract resolution. This does not prove runtime compatibility.

## Producer/consumer inventory

Disposition prefixes distinguish aligned source contracts, observed defects,
evidence gaps, and product decisions. A downstream task must rerun relevant checks
before closing its rows. Task numbers refer to the approved integration sequence.

| Surface | Producer | Consumer | Mismatch | Owner | Fix task | Check | Disposition |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Active versus attempt registry | `platform/postgres/init/03_app_registry.sql`, `05_operator_deployments.sql`, `operator/internal/apply/{apply,registry}.go` | `capabilities/internal/registry/store.go` | None found: attempts use `app_deployments`; publication sets Ready in one transaction; consumers filter Ready and enabled dependencies. Legacy registry permits other statuses but current publisher does not publish attempts there. | Operator/backend | 4 | Apply actual packages, decode published rows, fail update and preserve active snapshot | Closed for actual supported packages: live failed-update recovery and Ready snapshots PASS (Task 4) |
| Registry serialization | Operator marshaled AppSpec/UI/state machine | strict backend `registry.Decode` | Fixture roundtrips pass, but actual Operator publication has not been decoded by backend against a real DB. | Operator/backend | 4 | Actual app apply then backend load, capability disablement | Closed for procurement/process_fixture: backend strict load and disabled cap-process fail-closed PASS (Task 4) |
| Command receipts | `platform/postgres/capabilities/cap_process.sql` | `process/command.go` | Full workspace/actor/app/entity/command/key PK and response/hash columns agree; advisory lock, permission recheck and receipt writes present. | Backend | 5 | Same-key canonical retry/conflict; reject paths leave no receipt | Aligned in source; live evidence gap |
| Outbox fields and checks | `01_core_schema.sql` | `events/publisher.go`, command producer | Delivery status, attempt, lease, retry, result timestamps match SQL usage; status/lease coherence CHECK exists. Due index is only `created_at` filtered by status, rather than due-time/expired-lease indexes requested in historical SQL handoff. | Platform/backend | 2 and 6 | Add/assess due-time indexes; real lease expiry/restart/redelivery and stale ack rejection | Observed index discrepancy; correctness/recovery evidence gap |
| Processed effects | `cap_process.sql` | `process/effects.go` | NOT NULL workspace/work item/result and both composite FKs already exist, contrary to historical pending handoff; event/step PK matches consumer lock/lookup. | Platform/backend | 6 | Duplicate real workflow delivery, rollback and one persisted result/effect | Aligned in source; live evidence gap |
| File identity and retries | `01_core_schema.sql` | `files/{service,scanner,access}.go` | App ID, MIME/size, uploader, hash, unique bucket/key, retry columns/index and immutability trigger exist. No DB FK to registry app identity; authorization resolves active registry. Retention-safe app FK policy remains historical proposal, not an approved new requirement. | Backend/platform | 7 | Upload, blocked quarantine, clean/infected scans, content replacement and cross-app/workspace checks | Aligned fields; recovery evidence gap; app-retention policy deferred |
| Version/audit context | core bump/audit functions and generated DDL | Hasura CRUD, `process/command.go` | Actual generated procurement SQL includes BEFORE version and AFTER audit triggers; command sets transaction-local actor/action and does not manually duplicate audit. Missing runtime privilege boundaries leave trusted settings exposed to runtime DB credential holders. | Platform/backend | 3 and 5 | Restricted role CRUD/command, exactly one bump/audit/event, stale/concurrent rejection | Aligned trigger wiring; observed privilege defect |
| Runtime DB privileges | `compose.yaml`, bootstrap SQL | Hasura and cap-process | Both DSNs default to POSTGRES_USER bootstrap administrator; no restricted runtime role/grant provisioning found. This violates handoff constraint; do not run live service acceptance with these defaults. | Platform/backend | 3 | Assert current_user lacks superuser/BYPASSRLS and exercise required grants plus denied writes | Observed blocker |
| Actor provisioning | `platform/keycloak/realm-export.json` | `tests/fixtures/actors.json`, JWT verifier, Hasura | Realm has mappers/audience but no actor users or actor fixture business roles. Distinct subject/employee/workspace claims and repeatable tokens not provisioned. | Platform/backend | 3 | Real signed tokens for all six actor fixtures, forged/foreign role/workspace rejection | Observed blocker |
| Workflow principal | Compose defaults and Keycloak `role_workflow_service`, user `99999999-9999-4999-8999-999999999999` | actors and workflows use `role_workflow`, actor `22222222-2222-4222-8222-000000000005` | Role and principal differ; configured engine rejects fixture caller. | Platform/backend | 3 and 6 | One configured signed service principal; reject ordinary caller; renewable effect token | Observed blocker |
| Manifest parser and app packages | strict `operator/internal/manifest` | actual procurement/process-fixture/maintenance/timesheet packages | Procurement and process_fixture validate. Maintenance and timesheet both fail on `spec.sharedAccess.relationships` unknown field. Fixture-based Operator tests do not establish compatibility for all app packages; runtime reconcile parses actual files without fallback. Preserve business semantics when adapting legacy apps. | Operator/platform | 4 | Validate actual packages and reconcile intended package set; no fixture substitution | Procurement/process_fixture actual validation/publication/staged reconcile PASS; maintenance/timesheet intentionally unchanged and unsupported |
| Generated SQL/Hasura | `operator/internal/planner/{ddl,hasura}.go` | actual procurement CRUD and backend transactions | Generated procurement SQL and JSON inspected: same-workspace FKs, DRAFT/default version, triggers, allowlists, workspace guards, insert presets and editable-state update guards present. No real Hasura application yet. | Operator/backend | 4 and 5 | S01/S04 CRUD, protected/nested/upsert/relationship paths, command stale version | Task 4 CRUD/presets/protected paths/version/composite FK PASS; generated restricted domain/shared-source grants repaired. Command concurrency remains Task 5 |
| Workflow endpoint and auth | Compose `KESTRA_URL` uses namespace `dxos`; Kestra config basic auth | procurement flow namespace `dxos.apps.procurement`; publisher always sends Bearer token | Namespace differs; configured dummy bearer is not provisioned into basic auth. Flow secret values and plugins are not deployed/verified. | Platform/backend | 6 | Import declared flow on pinned Kestra; actual supported webhook/auth; separate publisher/effect credentials | Observed configuration blockers plus plugin evidence gap |
| Workflow destination routing | Publisher uses one global endpoint | procurement and process_fixture declare separate flows | All app events go to a single procurement endpoint; its condition ignores process_fixture. Independent subscriber delivery is not established. Do not claim second-app automatic effects from manual API tests. | Platform/backend | 6 | Deliver both declared app flows; document one-destination scope or implement declared dispatch without weakening dedup | Observed blocker for declared second flow |
| Kestra persistence | `platform/kestra/config.yaml` memory repository/queue, local storage | flow deployment and restart/recovery | Local storage volume does not persist memory repository/queue. Restart loses deployed in-memory configuration/queued work unless explicitly restored. | Platform | 6 | Actual restart with uncertain delivery; retain/redeploy flows and prove one effect | Observed recovery risk; live evidence gap |
| Storage/scanner setup | SeaweedFS credentials, Compose services | S3 adapter, file scanner | Credentials exist; private bucket creation, SigV4 operation, ClamAV limits/readiness and durable scan retry have not been demonstrated. Configured bucket is not itself provisioning. | Platform/backend | 2 and 7 | Real bucket setup and anonymous denial, scanner/storage interruption and restart persistence | Evidence gap |
| Service probes | `scripts/test-supporting-services.sh`, cap-process healthz | setup and handoff evidence | Script accepts any S3/Kestra HTTP status except 200, including missing endpoint/server errors; CLAMAV_ADDRESS is required but never probed. healthz always reports OK without dependency checks. | Platform/backend | 7 | Require expected auth failures and successful authenticated operations, actual ClamAV ping/readiness | Observed misleading verification defect |
| Safe environment/seed | root Compose fixed project name and ports; fixture seed | integration tests/manual recovery | No guarded disposable integration harness or reset targeting is established by source merge. | Integrator/platform | 2 | Verify project/database/volume target before seed/reset; restricted runtime credentials | Manual/files real API+scanner seeding and guarded reset qualified in Task 4; automatic seed refuses pending Task 6 flow/routing |
| Historical handoff wording | `backend-sql-handoff.md`, `backend-handoff.md` | frontend/integrators | Proposed/pending statements are stale for fields now implemented on platform. Must distinguish source implementation from live qualification. | Integrator | 8 after 2–7 | Link final revision and per-flow evidence; update historical status | Observed documentation drift |

No newly unassigned product decision blocks Task 1. Fan-out storage beyond declared
handoff flows, retention-safe registry/file FK policy, existing-database migration,
and external audit anchoring need separate design/scope if pursued. Current work
uses clean disposable databases; it does not qualify production migration.
Data/AI success, frontend implementation/tests, release acceptance, and S02/S07/S08
remain outside this baseline. Maintenance/timesheet incompatibility must be reported
and repaired without replacing business behavior with process-fixture examples.

## Executed baseline evidence

All commands ran 2026-11-07 in the isolated worktree on combined code
`0a5e3c9b4380276d81e8d4cbafbcc044f6c8d6c7`, before this documentation-only commit.
No services or runtime database connections were started. Go tests used the real
module runners, without integration build tags. Logs are local ephemeral evidence.

| Command | Working directory | Result | Evidence / limits |
| --- | --- | --- | --- |
| `make check-hygiene` | root | PASS | Repository metadata, headers, boundaries and manifests; `/tmp/integration-task1-hygiene.log` |
| `python3 -m unittest discover -s tests -v` | root | PASS | 15 tests; `/tmp/integration-task1-python.log`; static contracts/helper tests, not explicit live acceptance modules |
| `go test ./... -count=1` | operator | PASS | Five packages; `/tmp/integration-task1-operator.log`; integration-tag suites not run |
| `go test ./... -count=1` | capabilities | PASS | Seven tested packages; three packages report no tests; `/tmp/integration-task1-capabilities.log`; integration/serviceintegration suites not run |
| `go run ./cmd/dxos-operator validate ../apps/procurement/manifest.yaml` | operator | PASS | Valid manifest procurement v0.1.0 |
| `go run ./cmd/dxos-operator validate ../apps/process-fixture/manifest.yaml` | operator | PASS | Valid manifest process_fixture v0.1.0 |
| `go run ./cmd/dxos-operator plan ../apps/procurement/manifest.yaml` | operator | PASS | Generated SQL inspected; `/tmp/integration-task1-procurement.sql`; not applied |
| `go run ./cmd/dxos-operator plan -format hasura ../apps/procurement/manifest.yaml` | operator | PASS | Generated metadata inspected; `/tmp/integration-task1-procurement-hasura.json`; not applied |
| `go run ./cmd/dxos-operator validate ../apps/maintenance/manifest.yaml` | operator | FAIL | Unknown field `spec.sharedAccess.relationships` |
| `go run ./cmd/dxos-operator validate ../apps/timesheet/manifest.yaml` | operator | FAIL | Unknown field `spec.sharedAccess.relationships` |
| Live identity/CRUD/commands/workflows/files and recovery | disposable service environment | NOT_RUN | Tasks 2–7; baseline unit/static success establishes none of these flows |
| UI tests/build | workspace-ui | NOT_APPLICABLE | No frontend work or verification claim in this task |

Gate 0 has a combined tree and named downstream owners/checks for discrepancies.
It permits the next integration tasks, not frontend readiness or release claims.

## Task 4 closure scope

On the integrated Task 4 code, both actual packages validate, publish in both
fixture workspaces, preserve each other across sequential apply and supported-set
reconcile, and load through the backend registry. Disabling cap-process causes
commands to return 503 before entity lookup. Hasura enforces identity/workspace
presets, protected state/version/requester fields, editable-state grants, denied
delete/protected upsert/nested master-data mutations, and composite foreign keys.
A bounded forwarding proxy injects a dependency failure after real Hasura
permission removal; Operator restores metadata and retains the previous Ready
snapshot. This exercises the actual Hasura/PostgreSQL dependencies, not an
in-memory registry replacement.

Producer repairs are confined to Operator: single-package `apply` builds the
active desired set before planning; explicit `reconcile` retains its full-set
removal behavior. Generated DDL grants the declared domain tables to the trusted
process/Hasura connections, read-only access to dxos_reader, and declared shared
source reads to Hasura. Hasura remains the business row/column boundary; ordinary
DB callers gain no domain grants. No normative C0 or audit session_user gate was
changed. Two existing Operator SQL integration fixtures were repaired to execute
bound statements separately rather than passing multiple commands to lib/pq.

The real manual/files seed and reset interfaces are documented in
[integration setup](integration-setup.md#actual-package-publication-and-phase-data).
They use actual uploads/scans and authorized CRUD/transitions; CLEAN is observed,
never forged. Manual data intentionally freezes the scanner and stops Kestra.
Automatic seed completion remains deferred to Task 6. These results do not
qualify file recovery, workflow delivery, unsupported app packages or release
acceptance.

Task 6 adapter reconciliation: publisher routing is now a deployment-owned
workspace/app/version/workflow route map derived from Ready manifests and actual
workflow YAML. Explicit Basic transport authentication supports pinned Kestra
0.20.7; legacy full-endpoint Bearer mode remains available. Repository/queue use
Kestra's supported PostgreSQL backend with a dedicated restricted owner/database.
Effect service identity remains separate from transport authentication.

Automatic workflow qualification is limited to alpha for both actual apps.
Beta has no configured authorized workflow route; events remain retryable and
alpha-principal effects on beta events are denied. Beta automatic workflows are
NOT_READY pending provisioned per-workspace service identity/credential delivery.
No multi-destination/fan-out delivery contract was introduced. See the setup guide
for explicit signed-token renewal/reload and the final qualification evidence.
