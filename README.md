# Designing the agent rule set for DX-OS

This document set is the project's shared law during the design phase and the basis for later implementation. Keep it at `docs/olp2026`; there is no requirement to move `.agents/` to the root. Paths such as `operator/` and `apps/` describe the implementation project; they do not confirm that those directories exist where the documentation is stored.

The entry point is [AGENTS.md](AGENTS.md). The environment or the user brings this entry point into context through the mechanism in use; the document set does not register automation for every tool by itself.

## Organizational choice

| Option | Trade-off |
| --- | --- |
| Patch sentences within the old structure | Little initial change, but the contract keeps repeating across AGENTS/rules/memory/skills/personas and drifts easily when edited |
| **Keep the structure, separate the sources of truth — the chosen option** | Align once; afterwards each kind of decision has one defining place, and other documents point to it |
| Build a dedicated runner/adapter for each agent | Enables automation, but adds code and tooling constraints beyond the current need to design rules |

This choice keeps the three-tier architecture, H-P-D-I, two ownership areas, and eight acceptance groups. The mechanism decisions below are design proposals of this update; they must be implemented and verified when the system is developed.

## Responsibilities of each document group

| Component | Responsibility |
| --- | --- |
| `AGENTS.md` | Determines the phase, when to read a document, the order of work, and scope |
| `rules/` | Architecture/data/security/distribution invariants and acceptance criteria |
| `memory/architecture.md` | Components, control plane/runtime, and lifecycle |
| `memory/data-contracts.md` | Logical schema, field ownership, versioning, commands, manifest, and data identity |
| `memory/anti-patterns.md` | Explains common mistakes and points to the correct fix rule |
| `skills/` | Guidance for applying contracts by type of work, with clear triggers and output |
| `agents/` | Review perspectives and responsibilities by role; does not instantiate agents |
| `hooks/` | Pre/post-change and pre-hand-off checklists; do not execute themselves |
| `workflows/` | Work sequences, branching by phase and by assigned scope |
| `config.json` | Path index; does not duplicate ownership, shell commands, or assume a hook runner exists |

## Changes and rationale

| Decision | Rationale and consequence |
| --- | --- |
| Distinguish design, implementation, and release | A design is judged by consistency and acceptance scenarios. Only a phase with a running system requires runtime evidence; a release must still pass every mandatory check |
| Choose the DB trigger as the version owner | Hasura CRUD and the Command API both go through PostgreSQL. The trigger increments the version exactly once per UPDATE, preventing content edits from leaving an old approval request valid; commands do not add a second increment |
| Consider INSERT/UPDATE/upsert/nested/delete separately | Banning `UPDATE state` does not stop a client from creating a record in an approved state. Allowlists and server-managed values close both the create and the update path |
| Composite FK within the same workspace | Row filters protect queries but do not guarantee data relationships. A composite FK blocks invalid links at the DB, even for a service with elevated privileges |
| Separate REST and GraphQL expectations | `HTTP 200` does not prove GraphQL success; a filtered read may return empty. Tests must check data, errors, and write effects |
| Headers by file type | Keep JSON, frontmatter, shebang, and binaries valid; maintain SPDX for self-written code without corrupting third-party metadata |
| Commands with receipts/idempotency and durable events | A committed state must be able to reproduce its result on retry; a crash between commit and workflow dispatch must not lose or duplicate business effects |
| BI uses workspace + entity type + ID + version | Two apps with the same UUID are still two entities. Re-export does not inflate totals, and lineage preserves the exact source set |
| The Role Switcher only selects authenticated permissions | The demo does not become a privilege-escalation path; switching apps does not mean gaining a role |
| Domain/workflow lives in the app manifest/package | Operator/capability/UI use a shared mechanism; a fourth app proves pluggability without modifying the core |
| Verification commands carry a working directory and a result status | Subshells keep each test group in the right directory; PASS, FAIL, NOT_RUN, and NOT_APPLICABLE distinguish evidence from intent |
| Package first, test installation from the archive, then publish only when assigned | Verify the artifact that will be handed off, without depending on files that exist only in the checkout or accidentally publishing because of a checklist |

## Phases and evidence

[Pre-PR gate](.agents/hooks/pre-pr-gate.md) is the source of truth for how to choose and report checks. During the design phase, the absence of a future runner is neither a defect nor a passing result. There is no need to create source code or start services to edit a contract.

[Rule 03](.agents/rules/03-anti-patterns-and-security.md) keeps the eight groups S01–S08 together with their negative cases. Required results include refusing cross-workspace links, blocking state/version inserts, making CRUD invalidate an old version, and distinguishing two entity types that share a UUID.

## Items still to settle during implementation

- A versioned manifest schema, validator, and shared examples; mapping of OIDC/Hasura claims, actor fields, and each app's specific permissions.
- Full DDL for the registry, idempotency receipts, batch/lineage, and the durable event dispatch mechanism.
- Runner, CI, dependency versions, and integration test environment; executable adapters/hooks are added only when the project needs automation.
- The official PoF rules and the version that applies. The rule set sets a verification goal; it does not by itself guarantee a 50/50 score.

These items are work needed to implement the contracts, not defects caused by the rule set being stored in a documentation folder. This update does not modify the original architecture proposal or the learning demo to force them to match the rule set.
