---
title: RAG authorization
description: Source authorization and identity model for native RAG.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-18
---

# RAG authorization and source trust

**Status:** canonical policy for [ADR 0009](../../adr/0009-native-rag-identity-and-source-boundaries.md). The opt-in runtime enforces the implemented source and model boundaries; deployment qualification and remaining implementation limits are described in the ADR and [model guide](../../guides/knowledge/models.md).

## Document access

Every source-content operation requires a verified actor, current ACTIVE DB user and authoritative role memberships, enabled application/collection and authorized current version. Existing APP APIs validate the selected role against current DB grants. The new `AGENT` chat path selects sources server-side using all current database-authoritative role memberships; it does not change [ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)'s selected-role behavior for existing Hasura/capability APIs. Scope parameters narrow authority; they never grant it. A linked domain record adds its authoritative READ predicate.

| Mode | Additional READ predicate |
| --- | --- |
| PRIVATE | authenticated user equals document owner |
| ROLE | a current DB-granted role applicable to the source app belongs to its allowed role ceiling |
| RESTRICTED | the same role-ceiling predicate AND named-user READ grant |

For existing APP APIs, the qualifying role above is the validated selected role. For `AGENT`, the server resolves a qualifying role separately for each source app from the actor's current DB memberships; a role from one app cannot satisfy another app's ceiling. PERSONAL sources still require the owner predicate, regardless of any role.

All rows intersect SourceAccessPolicy below. Unknown source/mode/state denies. Personal collections are always PRIVATE; reject attempts to broaden visibility or add grants, including by admins. MANAGE requires explicit authority, never automatically confers READ to private content, and cannot expand an app's role ceiling. Personal lifecycle mutations require owner. No global operator/HR/model-administrator bypass exists.

## SourceAccessPolicy

| Source | Required trust and access predicates |
| --- | --- |
| UPLOAD | immutable File Service ID exists in correct app/owner scope, caller can read original, current scan status CLEAN, bytes match recorded checksum; scanner alone may confirm CLEAN |
| PACKAGE | app publication Ready, source ID still declared in current authorized source set, immutable package provenance/checksum verified, source approved for processing, retained version eligible under that source's current ceiling |

PACKAGE has no fabricated user upload, owner or core.files row. Before parser execution, package PDF/Markdown bytes still undergo applicable malware/format/size/resource checks. “Published package” is not permission to execute embedded code or ignore parser safety. A previous indexed package version may remain active on update failure only while its source ID remains declared and current policy permits it; source removal immediately revokes all its versions. Reads and activation verify the synchronously committed authoritative registry generation/source membership even when reconcile delivery is delayed; inability to verify current publication denies affected package sources.

Apply these predicates to metadata, search, OCR, preview, original download, citation, derived images/tables and future tool calls. File Service original routes must enforce owner-only personal access; document ACL alone cannot protect a broadly readable original.

## Current membership, revision and revocation

For RAG source access, at each request and immediately before external processing/response, query authoritative current DB user/membership and current document/source/policy revision. Keycloak verifies token identity (signature, issuer, audience, validity and subject); current ACTIVE `core.users` and `core.user_roles` determine business membership. JWT business-role claims cannot grant or revive a role. A new DB-granted role does not require refreshed JWT role claims. For existing selected-role RAG APIs, the role/header is only a request selector validated against current DB membership and app/capability scope. AGENT instead resolves qualifying roles per source from all current DB grants. Fail closed on DB/policy errors; do not cache a RAG allow decision across requests. These fresh RAG checks do not replace ADR 0007's existing Hasura cache/subscription semantics. JWKS freshness is unrelated to business-role revocation.

A revoke committed before the final authorization check denies that new source response/egress; later revocation cannot recall already-sent bytes. MVP answers are non-streaming. Document grants/policies update under revision checks or row lock, append audit and bump policy revision in the same transaction. Cache invalidation follows commit; canonical checks block access even if invalidation/index purge fails. The authoritative role-change commit is the authorized, audited DB transaction, not a Keycloak role update. No Keycloak role synchronization is required for business-role revocation. Identity/session revocation at Keycloak is a separate concern: valid token expiry and DB user disablement must not be misrepresented as instantaneous Keycloak logout propagation. Hasura, original-file routes and registry/UI context must use the same DB authority; capability middleware alone does not secure GraphQL.

## AGENT source selection and historical transcripts

`AGENT` is a server-side multi-source retrieval scope, not a role or app identity. The server derives eligible APP sources and the actor's owner-only PERSONAL sources from current DB authority. The owner conversation API accepts a question and derives the source set server-side. Client role lists, JWT business-role claims and LLM-suggested app IDs never supply authority. Every candidate, lexical/vector result, cited source and uncited context item must satisfy its own current source-app policy. No all-app query may bypass these predicates before ranking or leak denied titles/counts through results.

Both APP and PERSONAL retrieval support bounded FTS + Embedding fusion when the corresponding profiles and approvals are active. Missing, unavailable or unqualified semantic models preserve authorized FTS. Neither an all-role source set nor an embedding match grants processing permission.

Conversation/message snapshots belong only to their authenticated conversation owner; Platform Admin/model management does not grant transcript READ. Ordinary source-document or role revocation does not delete or hide the owner's historical messages and answer snapshots. Linked originals, citation previews, downloads and newly fetched excerpts always require current source authorization. A retained snapshot grants no source access: before any new turn, regeneration, summary or other model call, reauthorize all source-derived historical material and its processing policy. Omit revoked evidence and derived summaries from new context, even when their stored message remains readable. Never send the full transcript as an unchecked source of evidence. Snapshot retention/deletion is distinct from ordinary source revocation.

## Service and database authority

Operator identity may register immutable package snapshots only for managed app revisions; it cannot read PRIVATE documents. Worker identity is constrained to assigned job/version and eligible source, checks CLEAN/integrity again, and cannot grant access or scan-clean arbitrary files. User APIs cannot submit a service identity. Credentials belong server-side.

P2 must create separate least-privilege API/worker authority with narrowly listed cap_ai grants and guarded source access. No blanket grants on other capability schemas, all Core tables, original object buckets or future tables; no superuser/BYPASSRLS. Operator package publication uses a narrow internal API or reviewed write boundary, not document-content SELECT. Denial must be tested with actual connection roles.

## Model-processing policy

READ is necessary but insufficient for any external processing. Hosted PERSONAL `EMBED` and `GENERATE` are permitted only under an explicit audited Platform Admin approval. An existing enabled model slot, a qualified adapter, product-funded credentials or a spend budget never implies PERSONAL approval. There is no per-user hosted opt-in. PERSONAL remains an owner-only access class even when processing is approved.

Approval binds the processing purpose (`EMBED` or `GENERATE`), provider identity, exact immutable profile revision, permitted source and query classifications, retention and training-use review, approver/time and audit record. The approval, policy and profile must currently be enabled. The server independently validates source classification and query classification; client declarations cannot classify a personal query as non-personal. Mixed APP/PERSONAL context retains PERSONAL handling requirements. A query-only embedding call needs its own applicable query approval, even before any source has been retrieved. Each source must independently match its current processing policy and the chosen provider/profile/purpose; one approved source never approves the rest of a request. Unknown classification, missing authority or disabled/mismatched approval denies egress. Source READ and processing permission are checked immediately before every send and again, for every supplied evidence item, immediately before the non-streaming response.

Treat every global-chat question as potentially PERSONAL during hosted processing, even when searching APP scopes or generating from APP-only evidence. For query-only EMBED, require the exact current PERSONAL query review for the app being searched in addition to ordinary scope and model policy. For grounded AGENT GENERATE, require a separate current PERSONAL query review for the configured billing consumer. Source READ and processing predicates apply independently under each source's actual current app and role.

Only Platform Admin manages installation-wide model slots and processing approvals; applications and chat requests cannot choose models, providers, endpoints or credentials. The Gateway verifies the exact active slot and revision before dispatching a global query or answer. A slot change requires matching approval for the new profile revision and leaves existing vectors tied to their original profile. With no qualifying approval/model, use authorized PostgreSQL FTS and the documented search-only outcome rather than an unapproved provider. Hosted RERANK and OCR/VLM, and local-model serving, are outside this increment. Documents and model responses remain untrusted data and cannot grant tools, authority or provider URLs.

## Model-operation billing attribution

Every model operation keeps the existing atomic INSTALLATION/APP/USER reservation and accounting invariants. Ordinary APP calls charge their actual app; PERSONAL-only calls charge the genuine registered personal-library app, independent of UI context. Background ingestion retains its genuine worker Core identity for the USER account.

Cross-app `AGENT` model operations charge the requesting actor's USER account, the installation account and one server-configured APP account identified by `AI_AGENT_BILLING_APP_ID`. It must be a genuine registered app currently `Ready`, with an admin-provisioned budget and the profile's pricing revision. Missing/invalid configuration or budget denies the model operation. Do not synthesize an unregistered billing app or select it from request JSON. This reuses the existing APP ledger for MVP; a dedicated platform billing scope is deferred. The billing app is accounting attribution only: model policy must independently authorize every actual source app and each PERSONAL source/query. Charging that app never grants access or substitutes its classification for source/query classifications. Operation identity/fingerprint binds the actual evidence and policy revisions as well as the billed app. Preserve conservative uncertain-charge handling and idempotent settlement.
