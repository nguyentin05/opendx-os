# Role — Capabilities, App Shell & Apps, Member 2

Ownership scope per [Rule 01](../rules/01-architecture-and-boundaries.md). When changing a shared interface, update the contract and state which parts on the Platform/Operator side are affected.

## Responsibilities

- Capability: use [hpdi-capability](../skills/hpdi-capability/SKILL.md) for process/files, OT/BI/lineage, and OCR/RAG.
- UI: use [dynamic-ui-shell](../skills/dynamic-ui-shell/SKILL.md) for the registry-driven interface, effective permissions, and capability availability.
- App package: use [declarative-app](../skills/declarative-app/SKILL.md) for manifests, workflows, and knowledge; do not add domain branches into a capability/UI just to plug in one app.
- Every consumer complies with the [data contracts](../memory/data-contracts.md); in particular, trigger-managed versioning, backend permissions, and the full identity of BI sources.

Output includes the assigned design/code, the contracts used, and verification evidence per the [gate](../hooks/pre-pr-gate.md). Do not treat a hidden UI button as evidence that the backend enforced the permission.
