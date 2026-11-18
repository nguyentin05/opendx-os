---
title: Native RAG identity and source boundaries
description: RAG identity, source authorization, and model boundaries.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: proposed
owner: platform
last_reviewed: 2026-11-18
---

# ADR 0009: Native RAG identity and source boundaries

- **Status:** Proposed — conversation decisions recorded; RAG decisions recorded; ADR 0005 is accepted locally, while shared-contract/runtime convergence remains partial.
- **Date:** 2026-11-13
- **Decision owner:** repository/product owner; issue owner `nguyentin05` is the proposed responsible owner, pending identity/assignment confirmation.

The accepted [ADR 0007](0007-keycloak-authenticates-platform-authorizes.md) owns platform identity and authorization. This RAG proposal was renumbered from 0007 to 0009 during integration to avoid colliding with that decision.

## Context and dependencies

Current SQL and runtime consumers remain partially aligned with the accepted organization model; ADR 0005 records the target. Detailed source inventories and implementation progress are user-local. ADR numbers 0005 and 0006 are reserved for issues #9 and #10. This decision does not supersede their responsibilities. ADR 0005 owns the one-organization target and supersession of workspace clauses in ADR 0001/0003 and shared contracts. ADR 0006 owns app business-logic placement; no app-service hook or command-payload feature is selected here.

## Decisions and acceptance register

D01–D06 record conversation approvals from 2026-11-13. The owner-approved RAG-first contract amendment below is dated 2026-11-18. Formal ADR acceptance is distinct from approving an implementation session; these target requirements do not claim completed runtime enforcement.

| ID | Decision / status | Acceptance evidence / remaining gate |
| --- | --- | --- |
| D01 | One organization, no workspaces, independent app_id/org_unit_id; target supplied by owner via issue #9 | [ADR 0005](0005-department-and-application-data-model.md) accepted and merged locally as a0f7781; PR #59 targets feat/db-org-model. Shared-contract/runtime convergence and dataset mapping remain required before migration |
| D02 | Registered personal-library app; each user owns a PRIVATE library across app contexts; no sharing or admin bypass | User explicitly selected personal library, owner-only access and registered-library proposal; P1/P2 must prove original-file routes enforce ownership |
| D03 | Preserve existing data; block migration until mappings are explicit | User approved conservative migration; actual workspace-to-installation/app/department mappings remain deployment-specific and must be reviewed before apply |
| D04 | DB-authoritative business roles; Keycloak identity only; current membership at request entry and before egress/response; failures deny | User selected DB authority; audited DB role-change commit is authoritative. P1 must define Hasura/session boundary, selected-role defaults and scoped service identities; no Keycloak membership mirror |
| D05 | Nested knowledge schema and source-kind-specific trust | User approved nested schema; canonical fields and policy predicates are in linked contracts; parser/CRD/stored-spec implementation remains P4 |
| D06 | Historical blanket PERSONAL hosted restriction; explicitly superseded by D07 on 2026-11-18 | Retained as decision history only; no longer the target processing policy |
| D07 | Hosted PERSONAL EMBED/GENERATE requires audited Platform Admin purpose/provider/profile-revision approval, retention/training-use review, enabled state and valid independent source/query classification; no per-user opt-in | Owner-approved RAG-first amendment; canonical predicates are in authorization; existing Go/SQL restrictions require additive migration and implementation |
| D08 | AGENT sources are selected server-side from all current DB role memberships plus owner-only PERSONAL sources | Preserves ADR 0007 for existing Hasura/capability selected-role APIs; no client/JWT/LLM role authority |
| D09 | Owner-only historical message snapshots survive ordinary source/role revoke; links require current source access and revoked material cannot enter new context | New chat persistence/context consumers must enforce separate transcript ownership and evidence authority |
| D10 | Cross-app Agent model calls use a genuine Ready app configured by AI_AGENT_BILLING_APP_ID for the existing APP budget account | Pragmatic MVP accounting, not source permission; admin provisions budget, every actual source remains independently authorized; dedicated platform billing scope deferred |

## Rationale and alternatives

A registered library app preserves the required generic `core.files.app_id` relation, while resource policy remains in cap_ai. An application-independent file-owner extension is deferred because it broadens Core migration and authorization changes. Never manufacture an unregistered app ID, weaken a FK or hardcode the library app name in shared handlers.

Nested `access` keeps the security declaration together and removes ambiguity between historical flat/nested examples. Runtime named-user grants apply to eligible application documents only, never personal documents. Model-processing permission is independent from READ and from who funds inference.

## Contract amendment and implementation delta (2026-11-18)

D07 explicitly replaces D06 and the previous blanket hosted-PERSONAL clauses in the RAG authorization/API/answer contracts and model setup guide. It does not supersede any accepted ADR. In particular, ADR 0007 retains Keycloak authentication, DB-granted business roles and selected-role authorization for existing Hasura/capability APIs, including its documented cache/subscription behavior. The new AGENT path resolves and freshly checks its complete DB membership set separately; it must not silently widen those existing APIs.

[Authorization](../reference/rag/authorization.md) owns the detailed source, processing, transcript and billing predicates. The amendment recorded the following gaps in the pre-harness implementation.

| Producer/consumer at amendment time | Observed implementation and required change |
| --- | --- |
| `capabilities/internal/knowledge/models/policy.go` and `models/authority.go` | Accept only APP/PERSONAL and reject hosted PERSONAL outright; add AGENT authority and explicit approved PERSONAL decisions from DB, without trusting request classifications |
| `capabilities/internal/knowledge/embedding.go` | `semanticCandidates` only accepts APP; add owner-only PERSONAL and per-source AGENT hybrid retrieval, independent source policy checks, pinned revision/dimension and authorized FTS fallback |
| `capabilities/internal/knowledge/answers.go` | Accepts APP/PERSONAL, requires LOCAL for PERSONAL generation and one selected-role reader; integrate approved hosted PERSONAL and per-source AGENT authority with final rechecks of all supplied evidence/policy |
| `cap-ai/0001_metadata_jobs.sql` | `enforce_identity` and initial `read_document` require LOCAL_ONLY for personal uploads; preserve owner/original-file guards while allowing explicitly approved processing policies through a new migration |
| `cap-ai/0004_models_vectors.sql` | `model_policy`, `worker_model_policy` and `queue_embeddings` prohibit hosted private sources; query approval is app-bound NON_PERSONAL. Add independent PERSONAL/query approvals, purpose and retention/training-use review, AGENT per-source authorization and matching vector/worker eligibility |
| `cap-ai/0005_model_registry.sql` and runtime resolver | Active slots identify profiles, not source/query approvals. Preserve immutable bindings and digests; only Platform Admin manages installation-wide lifecycle, with no approval inherited from activation |
| New chat persistence/context and API/App Shell consumers | Add owner-only conversations/message snapshots with source provenance; preserve historical READ after revoke but filter source-derived history from every new context using fresh authority |
| Model operation/accounting producers | Keep Gateway reservations/settlement, actual library-app attribution and worker identity; bind AGENT operations to server-configured `AI_AGENT_BILLING_APP_ID` and independently validate every actual source |

Required migrations are additive, versioned **after 0005**, under `cap_ai`; never rewrite applied 0001/0004/0005 checksums. A base chat migration must support the FTS-only deployment without assuming optional model tables exist. It must add conversation/message ownership, immutable historical snapshots and source provenance plus narrowly granted multi-role retrieval/read helpers. A separate model-dependent migration must explicitly require 0004/0005 and replace conflicting guards/functions, add reviewed approval metadata/constraints and narrow grants for PERSONAL/AGENT operations. Existing local-only policy rows remain local-only: do not silently reclassify them, auto-approve providers or seed enabled approvals. Apply migrations through the existing checksummed runner with explicit optional prerequisites. The API/worker/Gateway/UI and migration/runtime-role tests must converge on these contracts in later implementation work.

The harness uses in-process cap-ai modules, no separate agent service, MCP, tool execution, autonomous actions, streaming or per-app backend. The table above records the implementation delta at amendment time. Migrations 0006–0010 and the [optional Compose deployment](../guides/knowledge/models.md#compose-installation) now implement the harness. Installation qualification remains a separate operational gate.

## Consequences and canonical sources

- [Authorization](../reference/rag/authorization.md) is the single source for access/source predicates and revocation behavior.
- [RAG contracts](../reference/rag/api.md) own serialization, metadata/version ownership, job/publication and Gateway boundaries.
- Migration preserves retained data and blocks unclear identity/source mappings; capability disablement never implies deletion. Detailed migration/run evidence is user-local.
- [Security and acceptance](../reference/security/index.md) defines shared security requirements; [verification](../contribute/verification.md) defines evidence standards. Detailed RAG test plans and handoffs are user-local.

The personal library can ship local full-text search before hosted RAG. App Ready and Knowledge Source Ready are independent. Revocation denies through canonical policy even if derived indexes remain stored. Implementation must preserve previous eligible source versions on failed indexing, but a late worker must never reactivate a removed source.

No new Core-to-capability FK, superuser/BYPASSRLS access, per-app backend or direct model access to originals/database/secrets is permitted. This ADR creates no schema, permissions or runtime enforcement by itself.
