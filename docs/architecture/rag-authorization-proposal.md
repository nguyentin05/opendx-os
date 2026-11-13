# RAG authorization and source trust

**Status:** canonical proposed policy for [ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md); approved conversation decisions are recorded there. No runtime enforcement claimed.

## Document access

Every operation requires verified actor, current DB user status and role membership, selected role among authoritative current DB grants, enabled application/collection and authorized current version. Scope parameters narrow authority; they never grant it. A linked domain record adds its authoritative READ predicate.

| Mode | Additional READ predicate |
| --- | --- |
| PRIVATE | authenticated user equals document owner |
| ROLE | current acting role belongs to allowed role ceiling |
| RESTRICTED | current acting role in ceiling AND named-user READ grant |

All rows intersect SourceAccessPolicy below. Unknown source/mode/state denies. Personal collections are always PRIVATE; reject attempts to broaden visibility or add grants, including by admins. MANAGE requires explicit authority, never automatically confers READ to private content, and cannot expand an app's role ceiling. Personal lifecycle mutations require owner. No global operator/HR/model-administrator bypass exists.

## SourceAccessPolicy

| Source | Required trust and access predicates |
| --- | --- |
| UPLOAD | immutable File Service ID exists in correct app/owner scope, caller can read original, current scan status CLEAN, bytes match recorded checksum; scanner alone may confirm CLEAN |
| PACKAGE | app publication Ready, source ID still declared in current authorized source set, immutable package provenance/checksum verified, source approved for processing, retained version eligible under that source's current ceiling |

PACKAGE has no fabricated user upload, owner or core.files row. Before parser execution, package PDF/Markdown bytes still undergo applicable malware/format/size/resource checks. “Published package” is not permission to execute embedded code or ignore parser safety. A previous indexed package version may remain active on update failure only while its source ID remains declared and current policy permits it; source removal immediately revokes all its versions. Reads and activation verify the synchronously committed authoritative registry generation/source membership even when reconcile delivery is delayed; inability to verify current publication denies affected package sources.

Apply these predicates to metadata, search, OCR, preview, original download, citation, derived images/tables and future tool calls. File Service original routes must enforce owner-only personal access; document ACL alone cannot protect a broadly readable original.

## Current membership, revision and revocation

At each request and immediately before external processing/response, query authoritative current DB user/membership and current document/source/policy revision. Keycloak verifies token identity (signature, issuer, audience, validity and subject); current ACTIVE `core.users` and `core.user_roles` determine business membership. JWT business-role claims cannot grant or revive a role. In the converged target, a new DB-granted role can be selected without refreshing token role claims. The selected role/header is only a request selector and must be validated against current DB membership and app/capability scope. Existing JWT/DB intersection may temporarily narrow access during cutover, but is not the completed target. Fail closed on DB/policy errors. Do not cache an allow decision across requests. JWKS freshness is unrelated to business-role revocation.

A revoke committed before the final authorization check denies that response/egress; later revocation cannot recall already-sent bytes. MVP answers are non-streaming. Document grants/policies update under revision checks or row lock, append audit and bump policy revision in the same transaction. Cache invalidation follows commit; canonical checks block access even if invalidation/index purge fails. The authoritative role-change commit is the authorized, audited DB transaction, not a Keycloak role update. No Keycloak role synchronization is required for business-role revocation. Identity/session revocation at Keycloak is a separate concern: valid token expiry and DB user disablement must not be misrepresented as instantaneous Keycloak logout propagation. Hasura, original-file routes and registry/UI context must use the same DB authority; capability middleware alone does not secure GraphQL.

## Service and database authority

Operator identity may register immutable package snapshots only for managed app revisions; it cannot read PRIVATE documents. Worker identity is constrained to assigned job/version and eligible source, checks CLEAN/integrity again, and cannot grant access or scan-clean arbitrary files. User APIs cannot submit a service identity. Credentials belong server-side.

P2 must create separate least-privilege API/worker authority with narrowly listed cap_ai grants and guarded source access. No blanket grants on other capability schemas, all Core tables, original object buckets or future tables; no superuser/BYPASSRLS. Operator package publication uses a narrow internal API or reviewed write boundary, not document-content SELECT. Denial must be tested with actual connection roles.

## Model-processing policy

READ is necessary but insufficient for any external processing. Personal originals/text/chunks/assets/queries and embeddings are excluded from hosted OCR/VLM, embedding, rerank and generation. Use local parser + PostgreSQL FTS with protected citations. No local model is promised; no automatic hosted fallback exists. Other data classes need explicitly accepted profile/classification/provider policy; otherwise deny egress. Product-funded credentials do not expand permission. Documents/model responses are untrusted data and cannot grant tools, authority or provider URLs.
