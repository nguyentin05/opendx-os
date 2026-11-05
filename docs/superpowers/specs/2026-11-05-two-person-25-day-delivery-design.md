# Two-person, 25-day module-first delivery design

Status: Draft for user approval. This is a delivery design, not an implementation plan or evidence of a working system.

## Confirmed requirements

- The repository is an initial scaffold. Existing files and passing unit tests do not mean the system is implemented.
- Two contributors are available: A is the user, with a backend focus; B has a DevOps focus.
- The delivery window is 25 calendar days, with day 1 being 2026-11-05 and day 25 being 2026-11-29, inclusive.
- The user selected module-first development: contributors build their assigned modules in parallel, then integrate the business flows at a scheduled integration stage.

## Proposed target and capacity assumptions

The proposed day-25 target is a reproducible development/demo build with a secure process foundation and declarative applications. A complete production deployment or competition-ready release is not assumed.

For sizing only, assume each contributor can provide approximately six focused hours per calendar day: 150 hours each. Days 1–3 reserve 18 hours each for shared contracts and setup; days 4–20 provide 102 hours each for module work; days 21–23 reserve 18 hours each for integration; days 24–25 reserve 12 hours each for contingency and hand-off. These are capacity allocations, not guaranteed task estimates. Reviews and tests consume the module allocation. Reduced availability requires reduced scope rather than removing verification time.

Only the deadline, contributor strengths, and module-first approach are confirmed. The target, workload assumption, milestones, and scope below are proposals awaiting approval.

## Approaches

| Approach | Benefit | Cost | Decision |
| --- | --- | --- | --- |
| Complete one small business flow end-to-end, then expand | Earlier business feedback and fewer late integration surprises | More switching between modules; initially narrow coverage | Considered; not selected |
| Build modules against agreed contracts, then integrate business flows | Each contributor can focus on their specialty and develop independently | Contract mistakes can accumulate until integration | Selected by the user |

Module-first does not mean untested until the final week. Each module must have its own checks, fixtures, and documented interface. Shared services, authentication configuration, and generated SQL can be checked before full business-flow integration. The main end-to-end integration window starts on day 21.

## Sources and architecture constraints

- [Architecture boundaries and current ownership](../../../.agents/rules/01-architecture-and-boundaries.md).
- [Architecture inventory](../../../.agents/memory/architecture.md).
- [Data and manifest contracts](../../../.agents/memory/data-contracts.md).
- [Security requirements and S01–S08](../../../.agents/rules/03-anti-patterns-and-security.md).
- [Verification gate](../../../.agents/hooks/pre-pr-gate.md).
- [License and packaging requirements](../../../.agents/rules/04-pof-50-compliance.md).
- [Core purity ADR](../../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md) and [capability schema separation ADR](../../adr/0003-modular-capability-schema-separation-for-iot-data.md).

This document allocates work; it does not redefine those contracts. Conflicts between the older memory inventory, later ADRs, and scaffold types are day-1–3 alignment work. Keep detailed decisions in their canonical source and update consumers together. Treat existing code as reusable scaffolding after checking compatibility, not as completed functionality.

The current Rule 01 groups Platform + Operator under one owner and Capabilities + UI + Apps under another. The allocation below is a proposed change for this team. After approval, update the canonical ownership table before implementation; do not silently keep two competing ownership definitions. Tier boundaries and security invariants remain applicable.

## Proposed delivery scope

### Core delivery target

- A reproducible Docker Compose development environment, with pinned service versions, explicit prerequisites, and reset/seed scripts.
- Real login and authenticated workspace/role context through Keycloak, APISIX, Hasura, and Go services. Client headers and mock tokens must not grant permissions in the delivered environment.
- A versioned manifest schema shared by the validator, Operator, app examples, and UI consumers.
- Operator `plan`, `apply`, and a bounded `reconcile` for supported resources. The supported resource set is fixed on day 3; it must include generated domain schema/version triggers and Hasura permissions. Repeated runs are safe; reconcile revokes excess managed permissions while preserving grants required by other active apps. It does not promise arbitrary destructive schema migration.
- Process commands with manifest-based state/role rules, stale-version handling, persistent idempotency, audit, and durable event publication.
- File upload, quarantine, scanning, and permission-checked clean-file usage through supported paths.
- A minimal generic UI: login, app selection, list/form, commands, capability availability, and useful conflict/error states. No custom per-app frontend.
- Procurement is the working business demonstration. The other existing app packages are validated design examples until their complete policies and dependencies work; they are not advertised as working applications merely to increase the app count. A small additional process-only fixture proves independent state and adding an app without editing core runtime code; it is the fourth package alongside the three existing examples.
- Executable evidence for the relevant core groups S01, S03, S04, S05, S06; runtime behavior independent of a stopped Operator; basic backup/restore and documented installation.

### Extensions, separately gated

OT deduplication and workflow effects (S02), real Parquet/DuckDB export and lineage (S07–S08), OCR/RAG, and externally anchored audit verification are additional deliverables. They are not silently counted as complete because a stub, table, or health endpoint exists.

At the day-14 checkpoint, identify at most one bounded extension candidate and its prerequisites, including the minimum infrastructure it needs. Reserve no more than the day-18–20 capacity for it. Start it only if the day-17 core module gates pass and the remaining capacity covers its implementation and tests. Otherwise use that time for core completion. The proposed first candidate is OT ingestion plus one declared workflow; it still requires complete S02 evidence to be advertised as delivered. Any uncompleted capability is absent or explicitly unavailable to users, never represented by a success mock.

Kubernetes, Argo CD, HA, autoscaling, custom UI views, general-purpose workflow editing, full semantic synchronization, and destructive schema evolution are outside the proposed 25-day commitment. Optional or deferred work remains visible in the backlog.

This is a scoped demo target. A release claim still requires the complete project release gate, including all S01–S08 and artifact evidence. Deferring audit anchoring means not claiming externally anchored integrity. If competition rules require all components by day 25, this scope and capacity must be revisited explicitly.

### Explicit demonstration boundaries proposed for approval

The minimum procurement flow is authenticated requester creation of a draft with a positive amount, optional quote attachment, draft editing, submit, manager approve/reject and permitted cancel. File checks, self-approval prohibition, workspace/role rules, initial state, version and retry behavior are required. A missing attachment is distinct from an attached file that is not clean. Declare its supported dependency set and hide or disable AI/BI features; do not register an unavailable service as enabled to satisfy the scaffold's current broad dependency list. Align the package's capability declarations and knowledge text in the contract stage.

The concrete core workflow effect is one persistent review work item per submitted procurement record version, associated with the originating record. Kestra invokes a generic, least-privilege process effect API implemented by A; the declared workflow provides the field mappings. Creation of the work item and the processed-step receipt must commit atomically and be repeatable by event identity plus step. B implements the workflow and retry tests. This does not require a separate inbox schema or an app-specific backend, and the minimal UI need only display the resulting review work item/link. A log line is not the effect or evidence of S03.

Maintenance is not accepted as working without its device prerequisites and declared alert policy. Timesheet is not accepted as working without the aggregate per-employee/day hours rule, including concurrent requests. This design does not remove those policies or replace them with weaker checks. Their live operation is outside the core commitment; the fourth process-only fixture supplies S05/pluggability evidence without pretending to validate those business rules. Fully implementing either existing secondary app is a separate extension estimate.

Semantic concept bindings, writer registration, ownership/type conflict handling and asynchronous semantic replication are all deferred. A core manifest must fail validation for unsupported semantic declarations instead of silently ignoring them. This prevents the scoped Operator from accepting a configuration whose ADR 0002 guarantees it cannot provide. Enabling semantic declarations later requires implementing the accepted governance contract first.

## Responsibility and hand-off model

A owns application semantics and Go/React implementation. B owns operating the services, integration configuration, workflow definitions, automation, and system test execution. B is not assumed to implement substantial Go domain logic or build a React application. A therefore has an explicit, limited UI allocation and does not absorb unlimited frontend work.

| Area | A: backend lead | B: DevOps lead | Hand-off artifact |
| --- | --- | --- | --- |
| Shared contracts | Manifest/API/event/schema definitions and examples | Claims, endpoint, deployment and service-identity requirements | Both approve one versioned contract set |
| Platform SQL | Author shared schema, constraints, triggers and domain planner behavior | Initialization order, migrations runner, volumes and DB operational checks | SQL plus isolated DB checks |
| Operator | Go validator, planner, apply/reconcile and deployment status | Credentials, environment wiring, workflow/route deployment adapters or scripts | Repeatable apply/reconcile procedure and resource ownership inventory |
| Identity and gateway | Service authorization, Hasura permission generation and negative tests | Keycloak realm, trusted claims, APISIX, Hasura connectivity and token setup | Real token fixtures and permission matrix |
| Process and audit | Command engine, receipts, audit transactions and outbox publisher | Kestra setup, event delivery configuration, workflow task definitions and retries | Event fixtures and replay/recovery test |
| Files | Upload/download APIs, metadata state machine, permission checks, scanner worker logic | SeaweedFS, ClamAV, storage credentials, scanner connectivity and failure fixtures | Quarantine and clean-file lifecycle checks |
| UI | Minimal schema-driven UI and API integration | UI image/build configuration, routing, deployment and browser smoke checks | Browser actions against the agreed API |
| Apps | Manifest semantics, validator fixtures and business acceptance assertions | Workflow YAML, knowledge/demo content and seed/reset scripts | Validated app packages; no per-app backend |
| Tests | Unit tests and behavioral assertions for owned modules | Integration harness, service fixtures, CI execution and artifact collection | Per-scenario evidence; both investigate failures |
| Delivery | API docs, known limitations and application troubleshooting | Container images, operational docs, backup/restore and archive installation | Repeatable demo build with explicit result status |

Path ownership is specific: A primarily edits `operator/`, `capabilities/`, `workspace-ui/src/`, app manifests, and semantic SQL. B primarily edits `compose.yaml`, service configurations under `platform/`, Docker/build files, scripts, CI, app workflows and operational documentation. App fixtures, contracts, SQL and test files may require both reviewers; coordinate edits before touching the same file. CI infrastructure belongs to B; CI failures in A's code remain A's responsibility.

## Twenty-five-day allocation

The rows are work windows, not instructions to wait for another person to finish. If an input is delayed, use the frozen fixtures to continue module work and record the blocker. Fixtures are test aids, not acceptance evidence for real integrations.

| Days | A deliverables | B deliverables | Required exit or hand-off |
| --- | --- | --- | --- |
| 1–3 | Align manifest/types/schema; specify command API, errors, event payloads and frontend data shapes; identify incompatible scaffold code | Establish deployment prerequisites and service inventory; specify JWT claims, network routes, environment variables and storage interfaces; scaffold CI | Contract freeze v1, two workspaces and test personas specified, named supported Operator resources, agreed scope and capacity |
| 4–7 | Operator schema validation, pure plan output, bounded apply and initial reconcile; version trigger and Hasura permission generation; module tests | Compose foundation: PostgreSQL, Hasura, Keycloak and APISIX; Go/UI build images; real token and endpoint fixtures; CI and DB checks | Operator output checked against a disposable database/Hasura; identity fixtures usable; no business flow required yet |
| 8–11 | Manifest-governed process commands; role/workspace checks; concurrency/version handling; receipts, audit and outbox publisher; generic idempotent workflow-effect API; module tests | Kestra and event delivery environment; SeaweedFS and ClamAV; trusted service routing and credentials; integration fixtures | Stable process/effect API and event envelope; event replay/recovery procedure; prerequisite services ready |
| 12–14 | File API and scanner worker; clean-file checks in commands; file/version/idempotency module checks | App workflows and step deduplication integration using A's event contract; service failure fixtures; API/system test harness; draft app packages | Process/file modules independently testable; extension candidate and prerequisites identified; unresolved backend work listed |
| 15–17 | Minimal registry/schema-driven React UI, real login, command retries and conflict handling; app manifest validation; remaining core module fixes | Demo users/data, fourth process-only fixture app, CI module matrix, UI delivery configuration; backup/restore automation and workflow checks | Core module hand-offs accepted, UI works with fixtures, all three example manifests validate structurally without falsely promoting unavailable apps, no new core API surface after this gate |
| 18–20 | Finish/review bounded reconcile and core fixes; only start the preselected extension if core gate is met | Independently run core component tests, fix configuration, collect module evidence; build packaging/install scripts and runbook; extension integration only if admitted | Feature freeze at end of day 20; complete hand-off inventory and explicit blockers; no extension may consume days 21–25 |
| 21–23 | Resolve cross-module logic and UI defects; verify user and manager actions, stale versions, retries, files and app discovery | Deploy clean environment and run end-to-end negative/recovery checks; test fourth app, Operator shutdown and backup restore; gather logs/results | Core acceptance evidence on one revision; no unresolved critical defect on demonstrated paths |
| 24–25 | Regression checks and contingency; finalize API/limitations notes and demo actions | Rebuild delivery artifact, verify installation from the extracted archive, complete operational docs and rehearse demo | Reproducible hand-off, evidence ledger and honest incomplete-work list; no unverified release claim |

## Dependency contracts and coordination

The critical dependencies are contract v1 → Operator and real claims → process/files → UI and business-flow integration. B's workflow development depends on A's event envelope, not on the entire UI. A's module tests can use disposable services or controlled fixtures while B completes deployment.

Each module hand-off includes its interface version, runnable setup, configuration needs, sample request/event, success/error behavior, checks already run, and known limitations. B can reject a hand-off that only works against undocumented local settings; A can reject deployment wiring that requires trusting client-supplied identities.

Use a brief daily coordination check for interface changes, blockers and the next hand-off, not for joint feature development. After day 3, contract changes require both contributors to review the producer and consumers and update the shared fixture set. A broken contract takes priority over optional work. Do not defer schema/identity compatibility questions to day 21.

Work on small feature branches with one primary owner per change; do not keep each person's entire 25-day work on a single long-lived branch. Independently verified module changes can merge before business-flow integration. No tools are authorized to contact people, publish images, push branches, or deploy outside the agreed environment merely because this document describes those activities.

## Acceptance and evidence responsibilities

The canonical requirements remain in Rule 03; this table allocates evidence rather than redefining them. Every row starts NOT_RUN for this project delivery window, regardless of any earlier scaffold unit-test result.

| Requirement | A provides | B executes/collects | Proposed scope |
| --- | --- | --- | --- |
| S01 isolation | Permission generation and backend checks; forbidden-column and cross-workspace test assertions | Real JWT/Hasura/service tests across workspaces and roles, including disallowed relationship/write paths | Core |
| S03 idempotency | Durable receipt/payload conflict behavior, event publisher and deduplication support | Duplicate command/event delivery and interrupted workflow retry checks | Core |
| S04 version/concurrency | Trigger and command logic; concurrent-request assertions | CRUD edit then stale approval, concurrent commands, failed-command audit/event inspection | Core |
| S05 independent app state | Separate app tables and state machines | Shared employee/work item with independent transitions | Core |
| S06 files | Immutable identity, scan state, download/command/OCR guards wherever supported | Quarantine, infected, scanner failure, unauthorized use and replacement cases | Core; OCR-specific execution only if OCR is delivered |
| S02 OT | Generic ingestion/deduplication and standard event logic if admitted | Duplicate telemetry and duplicate workflow delivery produce one work effect | Extension |
| S07 export | Real batch publication/reconciliation and source key logic if admitted | Failed batch exclusion, retries and cross-entity/workspace cases | Deferred unless separately admitted and resourced |
| S08 lineage | Full source-set capture and query behavior if admitted | Multi-record aggregates and version traceback | Deferred unless separately admitted and resourced |
| Pluggability/availability | Generic app/capability behavior, runtime independent of Operator | Fourth app install, disabled capability, stopping Operator without stopping requests | Core |
| Delivery artifact | Current dependency/application behavior documentation | Rebuild, checksum, archive extraction, out-of-tree install and backup restoration | Scoped hand-off; full release gate separate |

Unit tests, fixture tests, database behavior, real authentication and end-to-end results must be reported separately. A green hygiene script or an HTTP 200 alone is insufficient proof of business or GraphQL authorization behavior. If a required core check fails or is not run, the corresponding functionality is not accepted.

## Scope pressure and anti-goals

- Do not allocate all work to A while B only runs containers. B owns workflow configuration, fixtures, automation, failure testing and delivery work throughout the period.
- Do not assume either person has unconfirmed frontend or Go expertise beyond the stated strengths. Keep the UI small and reserve A's time for it.
- Do not make a mock login, fake OCR response, log-only workflow or scaffold table stand in for delivered behavior.
- Do not grant broad permissions, skip file scans, bypass manifest state rules or remove negative tests to meet the date.
- Do not turn a delayed module into an unannounced dependency for the other contributor. Track it at the next hand-off.
- At days 7, 14 and 17, compare remaining work to capacity. Drop extensions and UI polish first. Reducing a core invariant or changing the day-25 objective requires a user decision, recorded with remaining gaps.
- Production readiness, official PoF scoring and full S01–S08 completion are not inferred from this design or a successful demo.

## Decisions left to the implementation planner

Within the approved scope, select service/library versions after verifying compatibility, the manifest schema tooling, supported file-size/type limits, small demo datasets, exact package boundaries, CI provider details, and the UI's existing component approach. Use the current Go `cmd/` + `internal/` layout. Do not redesign the platform or add an extra infrastructure layer to satisfy this schedule.

The executable task plan should give each task an owner, bounded change, prerequisite, acceptance check and effort estimate. If that estimate exceeds the capacity envelope, bring the scope conflict back before implementation. It must start by aligning the canonical contracts/ownership and their consumers rather than accepting today's scaffold mismatches.

## Approval and verification status

Pending user approval: proposed demo target, capacity assumption, core versus extension scope, ownership allocation and schedule.

Design verification checks: local links and UTF-8/LF validity; contract/source alignment review; reviewer assessment of unclear dependencies and unallocated work. No runtime checks, service installation, implementation changes or release verification are performed as part of writing this document.
