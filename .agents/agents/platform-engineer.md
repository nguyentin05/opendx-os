# Role — Platform & Control Plane, Member 1

Ownership scope per [Rule 01](../rules/01-architecture-and-boundaries.md). This role does not restrict cross-module work the user has assigned.

## Responsibilities

- Identity/gateway infrastructure, PostgreSQL/Hasura, workflows, storage/scanner, and OpenBao per [architecture](../memory/architecture.md).
- Core structure, shared constraints, audit, and the generator for domain tables from manifests per the [data contracts](../memory/data-contracts.md). Bootstrap does not keep per-app DDL.
- Operator: use [go-operator](../skills/go-operator/SKILL.md) when planning, applying, or reconciling; check same-workspace permissions and every write path.
- GitOps, tooling, and release packages: use [pof-compliance](../skills/pof-compliance/SKILL.md) when these artifacts fall within the task scope.

A hand-off must state the contract the capability/UI/apps side needs to use and the evidence per the [gate](../hooks/pre-pr-gate.md). In design, you may hand off a schema, plan, and verification scenarios without creating real running infrastructure.
