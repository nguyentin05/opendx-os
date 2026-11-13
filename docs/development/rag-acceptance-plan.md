# Native RAG acceptance and phase handoff

**Status:** test specifications, not runtime results. Baseline `70271c335e58714301d502f0547d9eb76c8b05e8`, 2026-11-13. [ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md), [policy](../architecture/rag-authorization-proposal.md), [wire contract](../contracts/rag-api.proposed.md) and [migration](rag-migration-plan.md) define expected behavior once implemented.

## Fixtures and evidence

Use stable fixture UUIDs allocated by the owning phase plan, never production data. An/Bình/Cường share role_hr; only Bình has payroll READ. Viễn owns PRIVATE notes. App A/B contain near-identical policies; two versions disagree manager/director; text-native PDF page 4 has >=10,000,000 VND -> Director. Include Vietnamese Markdown with fenced code, image-only scans, table/figure assets and injected instructions. Actual current membership, source scan and policy revision are independently controllable fixtures.

Every row below requires direct HTTP assertions AND authoritative DB/object/provider-spy observations. A 200 GraphQL response is not authorization evidence without examining data/errors/affected rows. Expected denial means no hidden title, excerpt, count inference, content, citation or unrestricted asset URL. A test result must record revision, environment, command/request, response plus DB/object state, PASS/FAIL/NOT_RUN/NOT_APPLICABLE and gaps. Current runtime status for every row: NOT_RUN (future implementation).

| ID | Concrete setup / request / assertion | Owner / prerequisites |
| --- | --- | --- |
| R01 | An and Bình POST search/answers for payroll; An sees none; Bình sees only with role+READ+source guards; DB scope contains no forbidden IDs | P2/P7, real PG/API |
| R02 | B/admin GET A's personal metadata/citation/download and POST exact-phrase search; all hidden; originals remain owner-only | P1/P2/P7, File API/storage |
| R03 | A-only actor searches duplicate App B phrase; B absent from DB retrieval/model context/response | P7, PG/vector/provider spy |
| R04 | QUARANTINED/INFECTED/scan-error/wrong-hash source registration/parser/download; no indexing, model calls or bytes; ordinary scan update denied | P1/P3/P5, scanner/storage/PG |
| R05 | Valid document grant but linked record unreadable; search/source/answer denied with no context | P2/P7, record guard |
| R06 | Pause query, revoke ACL/current membership, resume before egress/response; newly forbidden chunks dropped, next request denied even with old JWT/cache | P1/P7, deterministic barriers/PG |
| R07 | Candidate director version incomplete then published; manager remains eligible until atomic switch, afterwards only director current citation | P5/P7, PG jobs |
| R08 | Reconcile unrelated app; fail new source indexing; personal rows unchanged, previous still-declared eligible source remains available | P4/P5, Operator/PG |
| R09 | Remove source/unpublish app while worker delayed; separately pause reconcile delivery with stale Knowledge snapshot; search/citation denied against committed registry membership, late worker cannot activate it, unavailable registry fails closed | P4/P5/P7, publication barriers |
| R10 | Query approval at 10,000,000 VND; logical row preserves Director, citation page 4 matches original | P5/P7, real parser/PDF |
| R11 | Image-only source; extraction exposes provenance/confidence or explicit unsupported/failure; no fabricated citation | P5, local OCR/parser if supported; VLM stretch separately gated |
| R12 | Document says print salaries/execute tool; response stays in scoped evidence; no tool/secret/provider selection | P7, adversarial fixture/provider spy |
| R13 | Invalid/expired/spoofed JWT, role/app scope; no metadata/DB disclosure; current DB membership revoked despite unexpired JWT denies; a newly DB-granted role works without token-role refresh in the converged target | P1/P7, real auth and PG |
| R14 | Concurrent same-key and distinct-key equal-source/equal-payload registrations return identical canonical IDs; conflicting metadata 409; different collections remain separate policy objects; one version/job/chunk corpus, replay rechecks access and stale lease cannot publish | P3/P5, parallel PG connections |
| R15 | Missing key, provider outage, exhausted budget or quota DB unavailable; no unapproved calls, authorized FTS remains, no fake generated answer | P6/P7, provider/budget spies |
| R16 | Personal source/query and unapproved restricted context; zero hosted OCR/embed/rerank/generation calls; local FTS returns only readable citations | P5/P6/P7, boundary spies |
| R17 | Non-manager broadens ceiling or personal owner/admin grants READ/changes PRIVATE; backend rejects and no policy mutation; authorized app grant/revoke audited | P2/P3, PG/API audit |
| R18 | Another user opens copied citation/figure/table URL or old version; same source/document/current-version guards deny | P5/P7, protected storage/API |
| R19 | MIME spoof, excessive pages/decompression, YAML traversal/symlink; bound/reject before expensive parse, no out-of-root read | P1/P4/P5, malformed fixtures |
| R20 | Repeat Operator plan with source changes; deterministic diff and no DB writes, jobs, file mutation or model calls | P4, spies and before/after PG state |

## Five Review Focus cases and exact future tests

| Test / target file | Additional assertion |
| --- | --- |
| `TestPolicySameHashDifferentOwners`, `capabilities/internal/ai/knowledge/policy_test.go` | equal checksum never implies shared metadata or access; duplicate-storage optimization is not authority |
| `TestRevokedMembership`, same policy tests plus direct HTTP integration | current DB denial overrides JWT; DB outage denies; check immediately before response/egress |
| `TestLateIngestAfterRemove`, `capabilities/internal/ai/knowledge/worker_integration_test.go` and `operator/internal/reconcile/knowledge_integration_test.go` | source generation/lease compare fails and active pointer stays revoked |
| `TestProviderTimeoutUnknownCharge`, `capabilities/internal/ai/modelgateway/budget_integration_test.go` | retain uncertain reservation; concurrent retry cannot overspend or settle twice |
| `TestPersonalSourceUnpublish`, policy and publication integration tests | unrelated app removal preserves original/document ownership and retention |

Also specify `TestDelayedReconcileRemovalDenied` in publication/retrieval integration and `TestDistinctKeyCanonicalRegistration` in registration integration; assert committed registry removal blocks reads before snapshot delivery, unavailable publication state denies, and concurrent distinct keys return equal scoped IDs without merging owners.

Also specify `TestPersonalGrantDenied`, `TestPersonalFileDownloadOwnerOnly`, `TestPersonalFTSNoExternalCalls`, `TestUploadSourceTrust`, `TestPackageSourceTrust` in owning phase plans before implementation. These names do not claim test files exist today.

## Verification runners

- Documentation: Markdown relative links/fences and YAML parsing using archived validator; full Mermaid rendering is separate and NOT_RUN. Repository hygiene: `make check-hygiene`.
- Static contracts: `python3 -m unittest discover -s tests -v`; no live authorization evidence.
- Capability unit: `(cd capabilities && go test ./...)`; Operator unit: `(cd operator && go test ./...)`.
- Real PG integration: root `make test-integration`, `TEST_DATABASE_URL` required, disposable PostgreSQL 17; inspect actual tags/helper setup. Current capability integration tests use `//go:build integration`. Existing Operator integration uses a Hasura fake, not real gateway qualification.
- UI: `(cd workspace-ui && npm test && npm run build)` with installed lockfile dependencies.
- Deployed acceptance additionally needs verified Keycloak/APISIX, least-privilege runtime connections, storage/ClamAV and selected parser/provider where applicable. Capture responses, object state and external-call evidence; do not derive PASS from static docs.

S01 now specifies organization/app/department/current-role isolation in the shared security contract; existing workspace-only fixtures do not establish that revised acceptance until migrated and executed. S03 covers replay; S04 policy/version races; S06 source quarantine. Unaffected S02/S05/S07/S08 are not established by RAG checks, and release still requires all repository acceptance groups.

## Phase planning gates and deliverables

P0-A/B drafts can be written now. P0 acceptance is not achieved merely by writing these documents. Shared-contract/runtime alignment with accepted ADR 0005, qualified RAG original/derived-source isolation, deployment dataset mappings and other-data egress approval remain explicit gates. Do not turn pending architecture into invented Go/FK signatures.

Prepare separate implementation plans under `docs/superpowers/plans/` after each relevant gate passes. Each must name exact files/interfaces, red/green tests, numeric limits, deployment flags and rollback. These full plans are P0-C pending work, not implemented by this acceptance matrix.

| Phase / planned filename suffix | Input gate | Independently testable output |
| --- | --- | --- |
| P1 `rag-p1-file-auth` | accepted ADR 0005 contract alignment, approved D02/D03/D04 and RAG source-access boundary | current membership/auth and coherent File API/scanner; personal original ownership; secure legacy route |
| P2 `rag-p2-policy-storage` | ADR 0005 FKs, accepted source policy and migration runner | cap_ai constraints/repository/policy plus narrow API/worker grants, live denied cross-capability reads |
| P3 `rag-p3-registration` | P1/P2 interfaces | transactional register/version/job/audit/receipt, list/status/grant APIs with personal sharing blocked |
| P4 `rag-p4-package-sources` | nested manifest/CRD version carrier and registry contract | parser/validator/CRD round-trip, pure diff, durable package reconcile and remove/rollback |
| P5 `rag-p5-docir-ingestion` | P3/P4, chosen measured parser limits/adapter | lease-safe DocIR/chunks, atomic lexical publication, protected citations; local personal FTS |
| P6 `rag-p6-model-gateway` | HARD GATE: approved non-personal processing classes/provider plus quotas/secret/uncertain-charge policy | product-funded Gateway, concurrency-safe budgets and dimension-versioned embeddings; personal hosted use stays disabled |
| P7 `rag-p7-secure-answers` | P5/P6 retrieval/Gateway contracts | authorized scoped FTS/vector merge, rechecks/abstention/citations and search-only fallback |
| P8 `rag-p8-ui-acceptance` | stable backend contracts | personal/app views, grant UI only for eligible app docs, zero-leak real-service acceptance |

Personal library UI/FTS may be delivered after P5 without waiting for hosted P6/P7. Parser/provider selection, concrete job leases/retries/concurrency, request/page/chunk/context caps, model/dimension and cost values remain mandatory owning-plan inputs. No BYOK, autonomous Agent Harness, local-model installation, complex multimodal success or billing dashboard is implied. P9 is a separate future project.

## RAG P1 scope correction

The user approved RAG-local auth/source isolation, with platform-wide Hasura/CRUD/subscription conversion deferred. See [platform follow-up](platform-db-authorization-followup.md). P1-A foundation and P1-B source isolation are separate deliverables; do not treat membership primitives or unavailable legacy routes as a completed file lifecycle or a shipped personal library.
