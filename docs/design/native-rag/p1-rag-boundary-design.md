# P1 RAG Authentication and Source Boundary Design

## Scope and approval

This short design records the user's approved scope correction: P1 secures RAG and the original/derived source paths RAG uses. Platform-wide CRUD, Hasura auth mode and subscription migration are a separate project. The earlier platform-wide authorization proposal is deferred; see [platform follow-up](../../development/platform-db-authorization-followup.md).

## Approaches

- **RAG-local boundary (selected):** verified subject plus current DB membership for cap-ai, retirement of unsafe legacy keyword lookup, and a shared source-policy contract applied before file bytes or excerpts can leave the system.
- **Platform-wide auth conversion (deferred):** Hasura hook/live predicates, all app CRUD, subscriptions and shared UI/session migration. Valuable platform work but exceeds this RAG increment.

## Behavior and incremental deliverables

P1-A is an independently testable foundation: legacy keyword RAG is unavailable, cap-ai can verify identity without workspace/JWT business-role claims, and a current-DB membership resolver is exercised against PostgreSQL. Keep the existing legacy auth API for unrelated consumers until their migration; do not weaken their checks.

P1-B aligns File Service/scanner access to core.files and qualifies the original-file boundary used by personal/app documents. It requires its own exact plan against the actual registry/file adapter; the current core.file_objects queries must not be hidden behind an untested compatibility alias. P1-A completion does not imply P1-B or full P1 completion.

The current extract path is mocked and relies on workspace-era SQL. In P1-A it remains unavailable, with authenticated callers receiving a safe 503 instead of fake extraction. Do not claim OCR capability until a parser/extraction increment exists. Personal library registration/upload and retrieval remain P2/P3/P5 work; no new context/document endpoint is added just to demonstrate auth.

## Authority

Keycloak supplies verified subject, issuer/audience/validity, not business roles. Current ACTIVE core.users plus core.user_roles/core.roles determine selected membership. X-Hasura-Role is a selector, never a grant. Require an explicit selected role; a newly granted DB role works without token role refresh. DB errors deny and do not reuse cached allows.

Membership resolution is not source READ. Only source policy may authorize originals/metadata/content. Current role is rechecked before response/model egress when real handlers are added. Principal/context contains no fake workspace and must not be repackaged into the legacy workspace Identity type.

## Source access and release gate

UPLOAD requires source existence, immutable checksum, CLEAN status, app/source scope, document policy and any linked-record guard. Personal originals/metadata/assets/citations are owner-only even for admins. PACKAGE uses verified immutable published source membership and processing safety, not a fabricated upload. The established no-hosted personal-data/query policy remains binding.

Inventory alternative access paths: File Service original route, direct Hasura/relationships/views/functions, object storage URLs and credentials, legacy OCR/RAG and derived asset routes. The release gate is actual denial to an unauthorized caller, not a checklist assertion. It is acceptable for this increment to remove a narrowly scoped personal-file table/relationship permission or disable a source route rather than migrate all Hasura authorization; preserve unrelated app permissions. If safe isolation cannot be demonstrated, personal-library release remains blocked and the specific platform dependency is escalated.

## Acceptance and rollback

- Legacy rag cannot touch the app filesystem or return excerpts for anonymous or authenticated requests, including traversal input.
- Signed token without workspace/role claims verifies identity; malformed/expired/wrong issuer/audience/signature denies.
- Current DB membership admits only selected current roles; new grant, revoke with old token, disabled user and DB outage behave correctly.
- No mock OCR output or source metadata appears through extract while unavailable.
- Before personal-library release, direct original/GraphQL/object/asset paths deny B/admin access to A's file; quarantined/wrong-hash data cannot be read/indexed.

P1-A is code-testable without implementing RAG storage or Hasura conversion. Real PostgreSQL membership tests are required; document/file isolation remains NOT_RUN until P1-B and later routes exist. Rollback keeps unsafe legacy endpoints disabled; no DDL or destructive data migration is introduced by P1-A. Preserve all approved migration mappings/data-retention gates.

## Sources and evidence

This scope refines P1 in the [original Native RAG roadmap](2026-11-13/docs/09-implementation-roadmap.md) under [ADR 0005](../../adr/0005-department-and-application-data-model.md). The user approved the RAG-local scope correction; detailed planning does not assert runtime qualification. P1-A membership is not document ACL enforcement or a completed File Service.
