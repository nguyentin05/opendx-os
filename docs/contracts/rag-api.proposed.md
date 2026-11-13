# Native RAG canonical contracts (proposed)

**Status:** contract specification, not implemented API/schema. [ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md) records decisions; [authorization](../architecture/rag-authorization-proposal.md) alone defines access predicates. [Historical source](../design/native-rag/2026-11-13/STATUS.md) is non-authoritative. Formal ADR 0005 alignment is required before identity/FK-dependent implementation.

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
| profiles / usage / reservations | allowlisted model purpose/classifications/dimension, credential_ref only, actor/app/installation budgets; idempotent reservation/settlement |

Canonical logical source identity is scoped, never a global checksum: UPLOAD document identity is `(collection_id, owner_user_id, initial_file_id)` and its version identity is `(document_id, immutable_file_id)`. A registration into a different collection deliberately creates a separate policy object and still requires source readability; it never inherits another collection's grant. PACKAGE document identity is `(app_id, source_id)` and version identity is `(document_id, immutable_package_revision, sha256)`; changing bytes within the same immutable revision is an integrity error. Processing-profile changes create new derived jobs/index revisions, not duplicate canonical originals. Concurrent registrations with distinct request keys but equal scoped source identity and normalized registration payload return the same canonical document/version IDs; conflicting registration metadata returns 409 without mutating the original. DB uniqueness and transactional conflict handling enforce this independently of receipt keys.

An active_version_id must reference a complete eligible version of the same document. Source kind/reference checks, same-document references and job uniqueness require database constraints, not UI conventions. Identical bytes across owners do not merge authority. Derived embedding indexes carry profile/model/dimension revision and never mix incompatible dimensions; rebuild does not mutate canonical original bytes.

## Public Knowledge API

All paths are under `/api/cap/ai`. Identity derives from verified context and current DB authority. Resource denial is concealed 404 without title/excerpt/version. Unauthenticated is 401; explicit policy mutation/egress denial is 403; malformed input 400; conflicting replay/revision 409; oversized request 413; unsupported media 415; unclean source 422; quota 429. Unexpected internals return safe 5xx without SQL, paths or secrets.

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
| POST /search | app_id/collection_ids/query; scope requests only; authorized active-version lexical or permitted hybrid hits |
| POST /answers | app_id/collection_ids/query/max_citations; grounded answer only from readable, processing-approved sources |
| GET /documents/{id}/citations/{chunkId} | reauthorized active-version source/asset preview; never raw object keys |
| GET /ingestions/{jobId} | source owner or authorized manager sees safe progress |
| GET /admin/model-usage | explicit operations permission, aggregated metering without private content |

POST registration is CLEAN-only: do not create WAITING_SCAN documents in this MVP contract. Upload itself may remain quarantined until scanner completes. A successful registration commits document/version/job/audit and idempotency receipt together; 202 is never emitted before commit. Receipt uniqueness is actor+app+action+key with normalized payload fingerprint; replay rechecks current access, returns original result for equal payload, 409 for mismatch. Request body cannot set owner, allowed-role claims or scan state. Changed content is a new immutable file/version.

Search hit/citation fields: document_id, version_id, chunk_id, page (nullable for Markdown), section_path, element_ids, bbox (nullable), source_url at the protected citation route; search adds authorized excerpt/score. `max_citations` must be bounded by the P7 configuration contract. No unbounded request/parse/context limits may be left to caller control; numerical limits must be fixed in owning phase plan before coding.

Answer response: answer, grounded, citations, model_profile, usage with prompt_tokens/completion_tokens when generation occurred. If no accessible grounding, answer is empty and grounded false. Search-only fallback returns readable citations with answer empty, grounded false, model_profile null and no fabricated usage. Personal queries use local FTS and this fallback when no approved local model exists; no hosted query embedding. Never suggest a generated answer occurred when it did not.

## App publication and indexing

`POST /internal/cap/ai/package-sources/reconcile` consumes an authenticated, scoped Operator snapshot: schema_version=1, app_id, immutable app_revision, monotonic publication_generation and complete desired source declarations/checksums. P4 must implement its authenticated trust envelope before exposure. Stale/replayed generations cannot restore removed sources. App apply durably commits desired-source work with Ready app publication; delivery retries survive Operator downtime. Reads and worker activation validate against the synchronously committed authoritative registry publication generation and source membership, not merely the most recently delivered Knowledge snapshot. If current publication state is unavailable or a snapshot cannot be matched to it, fail closed for affected package sources. Delayed reconcile delivery therefore cannot keep removed sources visible or authorize late activation; retrieval does not synchronously call the Operator.

**App Ready** means app configuration was published successfully. **Knowledge Source Ready** means the source has complete eligible lexical content and protected provenance. Embedding readiness is a separate derived-index state. Operator never waits on parser/model calls within apply and is not a request-time dependency.

Changed source content indexes asynchronously. Keep the previous eligible version while the same source ID remains declared, intersecting its current policy ceiling. A removed source ID is denied at publication commit even if old chunks remain. Worker publication transaction compares current publication generation/source declaration, policy/source state, lease token and complete candidate state; publish all-or-nothing. Late workers cannot resurrect removed sources. Unrelated reconcile never resets uploaded documents/grants.

## Ingestion and DocIR

Durable job state: QUEUED -> CLAIMED -> PARSING -> CHUNKING -> READY_CANDIDATE -> ACTIVE; transient failure enters RETRY_WAIT, fatal/retry-exhausted enters FAILED, revoked source enters BLOCKED. Expired leases may be reclaimed; old lease tokens cannot write/publish. Active previous content remains until atomic replacement. Retry policy/concurrency/lease duration require explicit P5 values.

DocIR preserves heading hierarchy, code fences, logical table rows with headers/units, row/column spans where supported, page/bbox/element IDs, figure provenance and confidence. Synthetic model captions are labelled and never source truth. Chunking must preserve the >=10,000,000 VND -> Director relation with page-4 provenance. P5 chooses a parser adapter only after representative Vietnamese/PDF/table/scan evaluation; no Docling dependency is selected here.

Event envelope: event_id, event_type `ai.document.version.registered`, schema_version 1, app_id, document_id, version_id, source_kind, source_ref, occurred_at. No document bytes, private text, credentials or client role claims. DB job poller is sufficient for MVP; effects idempotent under duplicate delivery.

## Model Gateway

Configured generation/embedding profiles only; no caller keys, provider URLs or credential refs. Gateway independently verifies processing policy, current authority and user/app/installation quotas before every external operation. Reserve estimated spend before dispatch, settle idempotently with actual/estimated usage after response. Unknown provider outcome retains conservative reservation for bounded reconciliation; retries cannot release all reserved spend or double-settle one successful request. Budget/policy outage fails closed. Kill switch keeps authorized FTS available.

Usage stores operation/model/actor/app/tokens/estimated cost/outcome/latency, never prompt bodies/source bytes/keys. Provider/data-class approvals, maximum cost/token limits, retry bounds and uncertainty reconciliation schedule are mandatory P6 plan inputs. They are not yet approved. Personal sources remain excluded from hosted profiles regardless of spend budget.
