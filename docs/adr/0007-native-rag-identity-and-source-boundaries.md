# ADR 0007: Native RAG identity and source boundaries

- **Status:** Proposed — conversation decisions recorded; RAG decisions recorded; ADR 0005 is accepted locally, while shared-contract/runtime convergence remains partial.
- **Date:** 2026-11-13
- **Decision owner:** repository/product owner; issue owner `nguyentin05` is the proposed responsible owner, pending identity/assignment confirmation.

## Context and dependencies

[Baseline](../architecture/rag-baseline.md) identifies conflicting SQL, accepted workspace ADRs and runtime consumers. ADR numbers 0005 and 0006 are reserved for issues #9 and #10. This decision does not supersede their responsibilities. ADR 0005 owns the one-organization target and supersession of workspace clauses in ADR 0001/0003 and shared contracts. ADR 0006 owns app business-logic placement; no app-service hook or command-payload feature is selected here.

## Decisions and acceptance register

All conversation approvals below were received 2026-11-13. Formal ADR acceptance is distinct from approving an implementation session.

| ID | Decision / status | Acceptance evidence / remaining gate |
| --- | --- | --- |
| D01 | One organization, no workspaces, independent app_id/org_unit_id; target supplied by owner via issue #9 | [ADR 0005](0005-department-and-application-data-model.md) accepted and merged locally as a0f7781; PR #59 targets feat/db-org-model. Shared-contract/runtime convergence and dataset mapping remain required before migration |
| D02 | Registered personal-library app; each user owns a PRIVATE library across app contexts; no sharing or admin bypass | User explicitly selected personal library, owner-only access and registered-library proposal; P1/P2 must prove original-file routes enforce ownership |
| D03 | Preserve existing data; block migration until mappings are explicit | User approved conservative migration; actual workspace-to-installation/app/department mappings remain deployment-specific and must be reviewed before apply |
| D04 | DB-authoritative business roles; Keycloak identity only; current membership at request entry and before egress/response; failures deny | User selected DB authority; audited DB role-change commit is authoritative. P1 must define Hasura/session boundary, selected-role defaults and scoped service identities; no Keycloak membership mirror |
| D05 | Nested knowledge schema and source-kind-specific trust | User approved nested schema; canonical fields and policy predicates are in linked contracts; parser/CRD/stored-spec implementation remains P4 |
| D06 | Personal documents and personal queries never hosted; local FTS when no local model | User approved default restriction; external processing for other classes/providers remains unapproved and blocks those P6 profiles |

## Rationale and alternatives

A registered library app preserves the required generic `core.files.app_id` relation, while resource policy remains in cap_ai. An application-independent file-owner extension is deferred because it broadens Core migration and authorization changes. Never manufacture an unregistered app ID, weaken a FK or hardcode the library app name in shared handlers.

Nested `access` keeps the security declaration together and removes ambiguity between historical flat/nested examples. Runtime named-user grants apply to eligible application documents only, never personal documents. Model-processing permission is independent from READ and from who funds inference.

## Consequences and canonical sources

- [Authorization](../architecture/rag-authorization-proposal.md) is the single source for access/source predicates and revocation behavior.
- [RAG contracts](../contracts/rag-api.proposed.md) own serialization, metadata/version ownership, job/publication and Gateway boundaries.
- [Migration](../development/rag-migration-plan.md) owns conversion/cutover/rollback. Capability disablement never implies data deletion.
- [Acceptance](../development/rag-acceptance-plan.md) owns R01–R20 and counterexample evidence.

The personal library can ship local full-text search before hosted RAG. App Ready and Knowledge Source Ready are independent. Revocation denies through canonical policy even if derived indexes remain stored. Implementation must preserve previous eligible source versions on failed indexing, but a late worker must never reactivate a removed source.

No new Core-to-capability FK, superuser/BYPASSRLS access, per-app backend or direct model access to originals/database/secrets is permitted. This ADR creates no schema, permissions or runtime enforcement by itself.
