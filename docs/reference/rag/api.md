---
title: Knowledge API
description: HTTP contract for scoped knowledge ingestion and retrieval.
diataxis: reference
audience: [app-author, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Native RAG canonical contracts

**Status:** canonical contract for [ADR 0009](../../adr/0009-native-rag-identity-and-source-boundaries.md); [authorization](authorization.md) alone defines access predicates. `cap-ai` serves these routes under `/api/cap/ai`: `GET /collections`, `POST /documents`, `GET /documents/{id}`, `POST /documents/{id}/versions`, `POST /documents/{id}/grants`, `DELETE /documents/{id}/grants/{userId}`, `POST /search`, `POST /answers`, `GET /documents/{id}/citations/{chunkId}`, and `GET /ingestions/{jobId}`. `POST /collections`, `PUT /documents/{id}/policy`, and `GET /admin/model-usage` are not implemented yet. Owner conversations are described in [capability routes](../api/capability-routes.md).

## Knowledge manifest serialization

Canonical version-1 extension under `DXApplication.spec`:

```yaml
knowledge:
  sources:
    - id: hr_payroll
      path: knowledge/payroll_policy.pdf
      mediaType: application/pdf
      access:
        mode: RESTRICTED
        allowedRoles: [role_hr]
        grantsManagedBy: authorized_hr_admin
      indexingProfile: complex-pdf-v1
```

This fragment is a contract example, not accepted by today's parser. `id` is stable within an app; `path` is package-relative, resolves inside its immutable package root and cannot escape through traversal/symlinks. `mediaType` must match verified bytes. `allowedRoles` must be declared/valid roles; `grantsManagedBy` names an approved authority policy, not a user ID or a self-authorizing free string. Reject unknown policy/profile names. ROLE and RESTRICTED are package modes; personal PRIVATE sources come through authenticated runtime registration, not YAML. RESTRICTED uses runtime named-user grants within its ceiling.

The extension schema version is 1; P4 must select and test its explicit carrier in the current manifest/CRD API version before implementation. No silent flat aliases (`file`, `contentType`, `visibility`, top-level `allowedRoles`, `grantManagement`). Existing manifests omitting knowledge remain valid. YAML, Go JSON tags, validation, planner, CRD/OpenAPI, stored app spec and runtime registry consume the same names. Example profile names are declarations requiring real configured profiles, not selected parser dependencies.

## Capability data ownership and constraints

All objects below belong to `cap_ai`. IDs are UUID unless indicated; app/role references use ADR 0005 identifiers. No workspace columns are added to new RAG contracts. Exact physical DDL is gated by ADR 0005, migration framework and P2 plan.

| Object | Required identity/invariants |
| --- | --- |
| collections | id, app_id, scope APP/PERSONAL, owner_user_id for PERSONAL, status; private owner cannot be reassigned by caller |
| documents | id, collection_id, owner_user_id, title, access_mode, policy_revision, active_version_id, revoked_at; personal mode fixed PRIVATE |
| versions | id, document_id, source_kind UPLOAD/PACKAGE, sha256, parser/chunker profile version, state; exactly one file_id or immutable package_ref according to source kind |
| policies / ACL | per-document role ceiling and unique (document_id,user_id,action); READ/MANAGE authority separate; personal grants forbidden |
| bindings | app/entity_type/entity_id; linked-record READ is an additional intersection |
| elements / assets | immutable version ID, parent/sequence/type/page/bbox/text/structured content/confidence; opaque protected asset reference and checksum |
| chunks / chunk_elements | version/ordinal/text/content hash/element links; unique version+ordinal; lexical index independent of embedding readiness |
| jobs | unique version+processing-profile identity, lease token/deadline, attempts, checkpoint, safe error and durable state |
| profiles / operations | allowlisted model purpose/classifications/dimension, credential_ref only, immutable profile ID/revision; one durable operation row per operation key with state, provider ID and token counters, no monetary account |
| processing approvals | audited Platform Admin approval binding exact provider/profile revision, purpose, source/query classifications, retention/training-use review and enabled state; see authorization |
| conversations / messages | owner-only snapshots and source provenance; historical READ survives ordinary source revoke, but retained evidence is reauthorized before reuse |

Canonical logical source identity is scoped, never a global checksum: UPLOAD document identity is `(collection_id, owner_user_id, initial_file_id)` and its version identity is `(document_id, immutable_file_id)`. A registration into a different collection deliberately creates a separate policy object and still requires source readability; it never inherits another collection's grant. PACKAGE document identity is `(app_id, source_id)` and version identity is `(document_id, immutable_package_revision, sha256)`; changing bytes within the same immutable revision is an integrity error. Processing-profile changes create new derived jobs/index revisions, not duplicate canonical originals. Concurrent registrations with distinct request keys but equal scoped source identity and normalized registration payload return the same canonical document/version IDs; conflicting registration metadata returns 409 without mutating the original. DB uniqueness and transactional conflict handling enforce this independently of receipt keys.

An active_version_id must reference a complete eligible version of the same document. Source kind/reference checks, same-document references and job uniqueness require database constraints, not UI conventions. Identical bytes across owners do not merge authority. Derived embedding indexes carry profile/model/dimension revision and never mix incompatible dimensions; rebuild does not mutate canonical original bytes.

## Public Knowledge API

All paths are under `/api/cap/ai`. Identity derives from verified context and current DB authority. Resource denial is concealed 404 without title/excerpt/version. Unauthenticated is 401; explicit policy mutation/egress denial is 403; malformed input 400; conflicting replay/revision 409; oversized request 413; unsupported media 415; unclean source 422. There is no quota response: the platform enforces no monetary quota. Unexpected internals return safe 5xx without SQL, paths or secrets. A model call that may have been sent returns 200 `SEARCH_ONLY` with reason `MODEL_CALL_UNCERTAIN`, or 503 `MODEL_CALL_UNCERTAIN` when generation is required, distinct from 503 `MODEL_UNAVAILABLE`.

| Method/path | Input and output |
| --- | --- |
| POST /collections | requested app/personal scope and title; server-owned identity; returns collection ID after authorized create |
| GET /collections | scoped visible collection list only |
| POST /documents | `Idempotency-Key`; file_id, collection_id, title, visibility; personal visibility only PRIVATE; 202 document_id/version_id/state QUEUED after durable commit |
| GET /documents/{id} | readable metadata/status, otherwise 404 |
| POST /documents/{id}/versions | new CLEAN file_id + idempotency key, MANAGE; 202 immutable version/job IDs |
| PUT /documents/{id}/policy | expected policy_revision and allowed mode/roles; MANAGE and ceiling; personal broadening rejected |
| POST /documents/{id}/grants | target user_id/action READ, MANAGE and ceiling; personal always rejected |
| DELETE /documents/{id}/grants/{userId} | authorized revoke with policy revision, personal grants unsupported |
| POST /search | scope/query with optional collection filters; authorized active-version lexical or permitted hybrid hits |
| POST /answers | scope/query/max_citations/require_generation; see the answer contract for implemented response fields and target extensions |
| GET /documents/{id}/citations/{chunkId} | reauthorized active-version source/asset preview; never raw object keys |
| GET /ingestions/{jobId} | source owner or authorized manager sees safe progress |
| GET /admin/model-usage | explicit operations permission, aggregated operation/token observations without private content |

POST registration is CLEAN-only: do not create WAITING_SCAN documents in this MVP contract. Upload itself may remain quarantined until scanner completes. A successful registration commits document/version/job/audit and idempotency receipt together; 202 is never emitted before commit. Receipt uniqueness is actor+app+action+key with normalized payload fingerprint; replay rechecks current access, returns original result for equal payload, 409 for mismatch. Request body cannot set owner, allowed-role claims or scan state. Changed content is a new immutable file/version.

Search hit/citation fields: document_id, version_id, chunk_id, page (nullable for Markdown), section_path, element_ids, bbox (nullable), source_url at the protected citation route; search adds authorized excerpt/score. `max_citations` must be bounded by the P7 configuration contract. No unbounded request/parse/context limits may be left to caller control; numerical limits must be fixed in owning phase plan before coding.

The [answer contract](answers.md) owns answer serialization and abstention outcomes; never imply generation occurred for search-only results. Existing APP/PERSONAL endpoints retain their selected-role/owner boundaries. The new chat path adds `AGENT` multi-source retrieval: the server resolves all current DB memberships and owner-only PERSONAL sources, and accepts only the question in the turn request. Neither clients nor models can supply role authority or choose a model/provider/profile. APP and PERSONAL support bounded FTS + Embedding fusion when authorized and qualified; otherwise preserve authorized FTS. Hosted PERSONAL source/query processing requires the independent audited approval in [authorization](authorization.md#model-processing-policy), not merely an active slot.

Chat persistence stores owner-only historical message snapshots with source provenance. Ordinary source revocation leaves those snapshots readable only by their conversation owner; current original/citation routes still enforce source READ, and every new generation context reauthorizes source-derived history. See [historical transcript policy](authorization.md#agent-source-selection-and-historical-transcripts). The [owner conversation API](../api/capability-routes.md#owner-conversations) persists turns. Selected-scope answers remain stateless.

## App publication and indexing

`POST /internal/v1/package-sources/reconcile` accepts `{schema_version: 1,
app_id, publication_generation, spec_digest}` through the separate internal
listener and the deployment's narrow dispatcher OIDC subject. It accepts no
caller-supplied source snapshot, bytes, artifact address or grant ceiling.
cap-ai rereads current Core authority and independently verifies immutable
artifacts before reconciling. Stale/replayed generations cannot restore removed
sources. Ready publication and durable notification commit together; delivery
retries survive Operator downtime. Reads and activation validate current Core,
source incarnation and authorization generation independently of delivery.
See [package knowledge integration](../../guides/knowledge/package-knowledge.md).

**App Ready** means app configuration was published successfully. **Knowledge Source Ready** means the source has complete eligible lexical content and protected provenance. Embedding readiness is a separate derived-index state. Operator never waits on parser/model calls within apply and is not a request-time dependency.

Changed source content indexes asynchronously. Keep the previous eligible version while the same source ID remains declared, intersecting its current policy ceiling. A removed source ID is denied at publication commit even if old chunks remain. Worker publication transaction compares current publication generation/source declaration, policy/source state, lease token and complete candidate state; publish all-or-nothing. Late workers cannot resurrect removed sources. Unrelated reconcile never resets uploaded documents/grants.

## Ingestion and DocIR

Durable job state: QUEUED -> CLAIMED -> PARSING -> CHUNKING -> READY_CANDIDATE -> ACTIVE; transient failure enters RETRY_WAIT, fatal/retry-exhausted enters FAILED, revoked source enters BLOCKED. Expired leases may be reclaimed; old lease tokens cannot write/publish. Active previous content remains until atomic replacement. Retry policy/concurrency/lease duration require explicit P5 values.

DocIR preserves heading hierarchy, code fences, logical table rows with headers/units, row/column spans where supported, page/bbox/element IDs, figure provenance and confidence. Synthetic model captions are labelled and never source truth. Chunking must preserve the >=10,000,000 VND -> Director relation with page-4 provenance. P5 chooses a parser adapter only after representative Vietnamese/PDF/table/scan evaluation; no Docling dependency is selected here.

Event envelope: event_id, event_type `ai.document.version.registered`, schema_version 1, app_id, document_id, version_id, source_kind, source_ref, occurred_at. No document bytes, private text, credentials or client role claims. DB job poller is sufficient for MVP; effects idempotent under duplicate delivery.

## Global model registry foundation (R1)

This is the canonical contract for the implemented optional registry foundation;
implementation does not establish production qualification. `RAG_GLOBAL_MODEL_REGISTRY_ENABLED`
defaults to `false`. It does not replace the existing model/source qualification
or policy gates. While OFF, preserve the current Plan05 deployment-file behavior:
`embed_profile` and `generate_profile` may select exact existing profile revisions
under the existing gates. They are legacy deployment configuration, never app or
user overrides. Authorized lexical search remains available without models or
pgvector.

While ON, the database is the sole authority for installation-wide `ACTIVE`
slot bindings. There is exactly one slot for each of `EMBEDDING`, `LLM`, and
`VLM`; a slot may be unbound. `RERANKING` is reserved and disabled until a
separate increment. No model choice belongs in an app manifest, workflow, UI
request or user preference. Capability names map to existing database profile
`purpose` values as follows:

| Capability | Existing profile purpose | R1 binding |
| --- | --- | --- |
| `EMBEDDING` | `EMBED` | Exact profile ID and revision |
| `LLM` | `GENERATE` | Exact profile ID and revision |
| `VLM` | None | Reserved, unbound; never alias to text generation |
| `RERANKING` | None | Reserved, disabled |

An absent binding, disabled/ineligible profile or unavailable configured adapter
returns `MODEL_UNAVAILABLE` for the required model operation. Never silently
fall back to `embed_profile`/`generate_profile`, another revision, provider or
capability while ON. Search degrades to authorized FTS when embedding is
unavailable. Answers retain the existing scoped SEARCH_ONLY fallback when
generation is optional; with accessible evidence and `require_generation=true`,
unavailable generation returns 503 `MODEL_UNAVAILABLE`. No evidence remains
NO_EVIDENCE without a model call. Policy denial remains distinct from model
unavailability. Slot activation never approves PERSONAL egress: source and query
processing independently require the [model-processing policy](authorization.md#model-processing-policy).
Optional migrations 0008–0010 implement audited PERSONAL and per-source AGENT processing. Enable them through the [deployment guide](../../guides/knowledge/models.md).

Startup configuration may declare candidates and adapter/secret references only;
it cannot override an `ACTIVE` database pointer or auto-promote candidates. Raw
credentials never enter the database, browser, logs or app packages. Endpoints
and adapters are deployment-allowlisted, never request-selected. Hosted APIs are
the first supported adapters; local serving in Tier 1 is future work, with no
bundled local model.

Retain `cap_ai.model_profiles`, policy allowlists, exact profile revision identity
and the durable `cap_ai.model_operations` rows. Embedding
query/index generations must agree on that identity and dimensions; a slot change
does not relabel or silently mix existing vectors. Registry migrations are
additive after optional `0004_models_vectors.sql`; applied migration checksums
must never change, except for the one-time coordinated rebase recorded in
[ADR 0010](../../adr/0010-model-operations-without-monetary-admission.md), which
requires the checksum preflight and a named database rebuild. `dxos_ai_gateway`
retains its scoped policy rights and the gateway-only operation interfaces,
and gains only read-only slot-resolution and fenced embedding lease-profile
execution. Both reads return the stored immutable declaration digest. Runtime
checks it against normalized profile, adapter and validated provider metadata
before accepting a slot or lease pin. Endpoint or loopback drift fails unavailable
even with an empty candidate list; enabled state is excluded from declaration
identity. No endpoint or raw secret is persisted. Candidate imports use a
distinct narrow deployment identity; neither identity gets blanket `cap_ai`
table writes. Future qualification, promotion and rollback require Platform
Admin authority, never application-admin authority. This foundation exposes no
production mutation route and grants no such administration permission.

### Consumer impact and contract review

| Consumer | Required integration when registry is enabled |
| --- | --- |
| cap-ai HTTP and worker model setup | Respect the default-off flag; resolve DB slots while ON without file-reference fallback |
| Embedding query and index generation | Resolve EMBEDDING, pin exact revision/dimensions, preserve independent lexical readiness |
| Answer service | Resolve LLM and distinguish unavailable models from policy denial; preserve scoped fallback and final authorization |
| Future evaluation worker | Consume the same slot/revision identity and existing policy/accounting gates; no separate model override |
| Future admin route | Separate Platform Admin authorization and audited lifecycle; no production mutation in R1 |
| Model provisioning role | Narrow candidate import only, no activation or blanket table writes |
| App Shell/UI | No model selector or credentials; display existing unavailable/search-only outcomes and discard stale context |

Review the contract with the registry OFF and ON: OFF preserves legacy exact
profile references; ON uses a single DB slot authority even when file references
are present. Confirm manifests and requests cannot choose models, absent slots
preserve authorized FTS, and required generation returns the unavailable outcome.
These are review/implementation acceptance cases, not runtime authorization test
results. This foundation refines optional capability storage and the existing
platform authorization boundary; it changes no accepted ADR. Production provider
approval and future lifecycle routes remain separate work.

## Model Gateway

Configured generation/embedding profiles only; no caller keys, provider URLs or credential refs. The Gateway independently verifies processing policy and current authority before every external operation, then claims a durable operation row, rechecks after the database wait, records dispatch intent and checks authority once more immediately before invoking the bound adapter. The adapter is invoked at most once per operation key. An unknown provider outcome records `UNCERTAIN`; the stored intent prevents any later resend. Policy/authority outage fails closed. Kill switch keeps authorized FTS available.

The operation row stores the actor, attributed app, profile ID/revision, purpose, fingerprint, lifecycle state, optional bounded provider request ID, input/output token counters, a sanitized bounded error code and latency. It never stores prompt bodies, source bytes, responses or keys. Token counts are observational evidence, not a charge; they are recorded only for `SUCCEEDED`. Each real provider/data-class/purpose approval and operational qualification must be provisioned explicitly; this contract approves no provider. [Attribution and durable state](authorization.md#model-operation-attribution-and-durable-state) assigns PERSONAL-only operations to the library app and cross-app AGENT operations to the genuine Ready app configured by `AI_AGENT_PROCESSING_APP_ID`. That app never substitutes for source authorization or classification.
