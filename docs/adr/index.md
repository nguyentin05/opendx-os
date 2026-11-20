---
title: Architecture decision records
description: Accepted and proposed decisions, with status and date.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-20
---

# Architecture decision records

An ADR records why a decision was made. When a decision changes, add a new ADR
with explicit supersession instead of rewriting the old one.

| ADR | Title | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-three-tier-composable-architecture.md) | Three-tier composable architecture | Accepted; one-table rule superseded in part by ADR 0008 | 2026-11-05 |
| [0002](0002-semantic-schema-governance-and-cross-app-data-sync.md) | Core schema purity and semantic cross-app data synchronization | Accepted; workspace examples superseded by ADR 0005; not implemented | 2026-11-05 |
| [0003](0003-modular-capability-schema-separation-for-iot-data.md) | Separation of core kernel schema and capability data schemas | Accepted; workspace examples superseded by ADR 0005 | 2026-11-05 |
| [0004](0004-kubernetes-crd-operator.md) | Kubernetes CRD operator with a cluster-scoped application definition | Accepted; workspace clauses superseded by ADR 0005 | 2026-11-11 |
| [0005](0005-department-and-application-data-model.md) | Department and application data model | Accepted | 2026-11-13 |
| [0006](0006-where-application-business-logic-lives.md) | Where application business logic lives | Accepted | 2026-11-15 |
| [0007](0007-keycloak-authenticates-platform-authorizes.md) | Keycloak authenticates, the platform authorizes | Accepted | 2026-11-13 |
| [0008](0008-an-application-declares-several-tables.md) | An application declares several tables | Accepted | 2026-11-14 |
| [0009](0009-native-rag-identity-and-source-boundaries.md) | Native RAG identity and source boundaries | Accepted; implemented in part | 2026-11-13 |

## Reading the old decisions

ADR 0001–0004 were written while the platform still used workspace tenancy.
Their core reasoning still applies, but table names and examples may use the
older model. The current model is defined by ADR 0005 and the
[data contracts](../reference/data-contracts/index.md).
