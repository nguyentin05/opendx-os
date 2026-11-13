# Native RAG P0 execution handoff

## Scope and pinned revision

Native execution on `feat/cap-ai`, base/HEAD `70271c335e58714301d502f0547d9eb76c8b05e8`, 2026-11-13. Deliverables are uncommitted documentation. No runtime code, DDL, deployment, provider call or Git commit was performed.

## Delivered versus gated

- [Security Preflight](rag-security-preflight.md): local static inventory and empty Compose project observation; external exposure unknown, not declared safe.
- [Baseline](../architecture/rag-baseline.md): actual producer/consumer mismatches and target model provenance.
- [ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md): Proposed, recording conversation decisions without superseding ADR 0005/0006 responsibilities.
- [Authorization](../architecture/rag-authorization-proposal.md): distinct source trust, owner-only library, current-DB checks and independent processing policy.
- [Canonical contracts](../contracts/rag-api.proposed.md): nested manifest, Knowledge API, cap_ai ownership, desired/active publication and Gateway/job boundaries.
- [Migration](rag-migration-plan.md): preserve data, block unmapped conversion, explicit rollback boundary after new writes.
- [Acceptance/phase handoff](rag-acceptance-plan.md): R01–R20, threat scenarios, runners and independent phase-plan gates.
- [Versioned source package](../design/native-rag/2026-11-13/STATUS.md): historical proposals, with original Python/YAML bytes retained as reference text and links adjusted.

**P0 is not complete.** Shared-contract convergence with accepted ADR 0005 and full executable P1–P8 plans remain pending. P0-C currently supplies acceptance specifications and gated phase briefs, not runnable backend implementation plans. Do not bypass these gates by inventing FK/identity semantics.

## Decisions received during execution

The user approved the nested `path/mediaType/access` schema, retaining legacy data while blocking unclear mappings, and checking authoritative current DB membership per request and before egress/response, failing closed on errors. These join the previously approved registered personal library, owner-only/no sharing, and no hosted personal-data processing with local FTS fallback.

Still required before dependent execution:

1. ADR 0005 is now accepted on local feat/cap-ai (merge a0f7781), with PR #59 open against feat/db-org-model. Reconcile remaining workspace-era contracts/consumers. Confirm D01–D06 responsible owner; issue owner is proposed, not assumed authenticated identity.
2. Implement the selected DB-authoritative business-role boundary: Keycloak identity verification, current DB user/membership and audited DB changes. Finalize the Hasura/direct-file/session path and service scopes; JWT business-role claims are not target authority. No Keycloak role-sync gate remains.
3. Supply actual legacy dataset mappings, compatible write-fence/cutover and tested reverse conversion or forward repair for post-cutover writes.
4. Choose explicit manifest-extension version carrier in P4; choose measured parser/resource/lease limits in P5; approve non-personal hosted-provider processing/quotas/credentials before P6.
5. Determine actual deployment exposure. If legacy RAG is exposed, execute a separately scoped containment hotfix and verify the deployed boundary.

## Verification ledger

| Check | Result and limits |
| --- | --- |
| Relative links and fenced blocks | PASS: final links/fences on 25 project/archive/plan documents; new-document whitespace clean, historical Markdown hard breaks preserved |
| YAML reference parsing | PASS: two reference YAML files parse with PyYAML |
| Archived input integrity | PASS: YAML/Python reference bytes equal source inputs; Markdown links adapted |
| `git diff --check` | PASS; new-file whitespace checked separately |
| `make check-hygiene` | FAIL: four pre-existing input files under `.scratch/` lack project SPDX/copyright; no new project-file header errors after reference preservation |
| `python3 -m unittest discover -s tests -v` | FAIL: 40 tests run, 39 passed and one hygiene test failed on the same four pre-existing `.scratch` headers; static only, no runtime authorization proof |
| Go/UI/build/real-service migration and security tests | NOT_RUN: documentation-only output, future runtime contracts unimplemented |
| Full Mermaid rendering | NOT_RUN: offline validator checks declarations, fences and links only |

## Rulings and rollback

Documentation stayed in the existing feature checkout; no isolation was created because this scope has no runtime mutations. Prose was verified by parsers/review rather than synthetic TDD tests. Archived unlicensed input code/YAML was retained as `.txt` references to avoid assigning unsupported project copyright. Detailed downstream implementation plans were left gated rather than inventing missing architectural/provider/parser choices. These choices preserve reviewability but require owner decisions before P1–P8 can be finalized.

No data rollback is needed: changes are documentation only. Runtime migration/rollback requirements live in the migration specification. Preserve the ignored local plan/design files when handing off, or use these durable project documents and archived spec as the executor input. No merge/push/commit has been performed.

## Independent review

A fresh reviewer found two Important contract gaps and no Critical/Minor findings: delayed reconcile delivery could leave stale access, and source version uniqueness did not cover distinct idempotency keys. The fix pass specifies synchronous authoritative registry-generation/source checks, fail-closed behavior and distinct-key scoped canonical source identities, with dedicated acceptance scenarios. This is documentation review, not runtime verification.

The reviewer declined runtime/model/migration/deployed-exposure judgments and unaccepted ADR/provider/parser/phase-interface decisions; these remain explicit prerequisites rather than accepted behavior or test results. Archive integrity/link evidence is supplied by executed local checks.

## Contract convergence after ADR 0005

The shared data contract now states the one-organization kernel/capability ownership and DB-authoritative role target, and the shared security contract defines app/department/owner S01 cases. Unrelated sections 5–9 explicitly retain legacy consumer serialization pending module-specific migration; they are not permission to introduce new workspace claims. RAG policies no longer treat JWT role lists or Keycloak role synchronization as business authority. Platform-wide Hasura/subscription conversion is deferred outside RAG P1; alternative source-access paths remain a private-library release gate, not implemented controls. PR #59 remains open; local integration is distinct from remote merge.

## RAG P1 scope correction

The user approved RAG-local auth/source isolation, with platform-wide Hasura/CRUD/subscription conversion deferred. See [platform follow-up](platform-db-authorization-followup.md). P1-A foundation and P1-B source isolation are separate deliverables; do not treat membership primitives or unavailable legacy routes as a completed file lifecycle or a shipped personal library.

## P1-A implementation handoff — 2026-11-13

P1-A now adds subject-only verification alongside the unchanged legacy role/workspace contract, a cap-ai-local current-DB membership resolver and testable HTTP wiring. Legacy `/rag` is unavailable without filesystem lookup; `/extract` is authenticated but unavailable while real extraction/source policy is absent. No successful RAG/OCR feature is claimed. Startup requires explicit DB/OIDC configuration, bounds its DB ping to three seconds, and no longer uses a fallback DSN.

Validation: capabilities unit suite passed; PostgreSQL 17 membership lifecycle/outage/bounded-query integration passed. Race/static/final-review results are recorded in the execution evidence below after completion. The DB fixture uses only disposable identity/role tables and does not prove production runtime grants. No DDL, Hasura conversion, source-store access, migration, provider call or deployment was performed.

### P1-B entry and release gates

P1-B must align the original File Service/scanner and narrow registry adapters from core.file_objects/workspace to core.files/app under ADR 0005, with explicit current app authorization and generic owner/source policy. Preserve immutable file identity, owner/hash/scan metadata and bound upload/scan processing; no broad compatibility aliases, app-name branches or privilege escalation. Scope app/record READ as well as owner-only personal access.

Inventory and test alternate routes before release: process File API originals, direct Hasura selects/relationships/views/functions, object/storage credentials or URLs, and later derived asset/citation routes. A personal source must be unreadable by B/admin via every available route. Where an alternate path is not ready, narrowly revoke/disable it or block release; do not expand this increment into whole-platform subscription migration. Real ClamAV/storage, least-privilege production roles and these direct-source denials are NOT_RUN in P1-A. P2/P3 document lifecycle and P5 parsing/retrieval remain future work.

### P1-A verification evidence

Code commits: 4d7b31d (subject), 42cdf8f (membership), f892a7d (HTTP boundary). Checks run from capabilities/: `go test ./... -count=1` PASS; `go test -race ./internal/auth ./internal/ai/access ./cmd/cap-ai -count=1` PASS; `go test -race -tags=integration ./internal/ai/access -count=1 -v` PASS with a disposable PostgreSQL 17 container and isolated test databases. Integration cases cover current grant/revoke, disabled/unknown user, expired/cancelled subject, no cached allow after DB close and a bounded query blocked by a table lock.

Root hygiene in the user's checkout still reports four pre-existing `.scratch` input-header errors. A clean detached verification worktree at f892a7d has no such inputs: `make check-hygiene` PASS. This distinction preserves the original inputs rather than silently excluding failing checks. Static suite result and independent review are appended below after final completion.

Final clean-worktree static verification: `python3 -m unittest discover -s tests -v` PASS, 40/40 at f892a7d. Fresh independent review found no Critical/Important issues. Evidence documentation is now populated; one nonblocking regression-test gap is deferred: cancellation/expiry during membership resolution is guarded in production code but lacks a dedicated mid-resolution test (already-cancelled/expired cases are covered).

Reviewer exclusions remain explicit: deployed gateway containment, production primary routing/grants, original File/Hasura/object isolation and real OCR/RAG were not qualified. The reviewer inspected code and supplied evidence; executed test results above come from this implementation session, not independent reviewer reproduction. P1-A is complete as an authentication/unavailability foundation; full P1 and private-library release remain blocked on P1-B/source qualification. No push, PR or deployment was performed for P1-A.
