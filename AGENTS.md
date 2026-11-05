# DX-OS Open-Core — Agent working rules

## Starting a task

1. Determine the output the user requested and its phase: **design**, **implementation**, or **release**. Editing contracts/documents belongs to design; writing code belongs to implementation; checking and packaging the distribution belongs to release. A task may span several parts; assess each part by its own phase.
2. Read the sources in the table below for the affected part. When changing a shared contract, check both the producer and the consumers of that contract.
3. Before and after editing, use [pre-change](.agents/hooks/pre-change.md) and [post-change](.agents/hooks/post-change.md). Before a hand-off or PR, use [pre-pr-gate](.agents/hooks/pre-pr-gate.md).
4. Report the output produced, the verification evidence, the parts not verified, and the open decisions. Missing code/tests during the design phase is a valid state; do not turn that state into a passing test result.

## Sources of truth and when to read them

| When the work involves | Read the source of truth |
| --- | --- |
| Architecture, tier boundaries, ownership | [Rule 01](.agents/rules/01-architecture-and-boundaries.md), [architecture](.agents/memory/architecture.md) |
| Schema, Hasura, manifest, commands, UI using data | [Rule 02](.agents/rules/02-data-contracts-and-permissions.md), [data contracts](.agents/memory/data-contracts.md) |
| Authorization, files, workflows, OT, BI, audit, or tests for these | [Rule 03 and acceptance matrix](.agents/rules/03-anti-patterns-and-security.md) |
| New files, license metadata, packaging | [Rule 04](.agents/rules/04-pof-50-compliance.md) |
| Design review or checking an error-prone approach | [Anti-patterns](.agents/memory/anti-patterns.md) |

`rules/` defines invariants and acceptance criteria; `memory/` defines the detailed contracts; skills/workflows apply those sources. For example, a persona or workflow must not relax an invariant. If sources conflict, name both sources and resolve the conflict according to the agreed requirement; ask for clarification only when a product/architecture decision remains unassigned.

## Routing work

| Scope | Skill |
| --- | --- |
| Operator, DDL/metadata generation, reconcile | [go-operator](.agents/skills/go-operator/SKILL.md) |
| H-P-D-I capability, commands, OT, BI, OCR/RAG | [hpdi-capability](.agents/skills/hpdi-capability/SKILL.md) |
| App Shell, schema-driven rendering, switching app/role | [dynamic-ui-shell](.agents/skills/dynamic-ui-shell/SKILL.md) |
| Creating/editing an application package, manifest, business workflow | [declarative-app](.agents/skills/declarative-app/SKILL.md) |
| Checking licenses, PoF, release artifacts | [pof-compliance](.agents/skills/pof-compliance/SKILL.md) |

- Developing or editing documentation: [develop-and-pr](.agents/workflows/develop-and-pr.md).
- Plugging in an application: [plug-new-app](.agents/workflows/plug-new-app.md).
- Packaging: [release-package](.agents/workflows/release-package.md).
- Role-based review perspectives are in the [index](.agents/config.json); a persona does not instantiate an agent or change execution permissions.

## Scope and execution authority

Follow the instruction precedence of the runtime environment and the scope the user assigned. Project rules do not grant permission to send notifications, publish, or change systems outside that scope. Ownership designates who is responsible and what needs review; it does not require an agent to stop cross-module work it has been assigned.

Write documentation in English, keeping technical names and API identifiers. Put each detailed rule in a single source of truth and point to it. Editing the rule set does not imply a request to change the original architecture document or the learning demo.

`.agents/config.json` is the data index of the guidance set. `hooks/*.md` are checklists for the agent to read; automatic execution requires a separate integration with the runtime environment. The [README](README.md) explains the organization and the design rationale.
