# OpenDX-OS System Architecture Overview

This directory contains the architectural documentation for **OpenDX-OS (DX-OS Open-Core)**.

---

## 1. Three-Tier Composable Architecture

```mermaid
flowchart TB
    subgraph T3["Tier 3: Declarative Domain Applications (apps/*)"]
        direction LR
        MAINT["apps/maintenance<br/>manifest.yaml + workflows + knowledge"]
        PROC["apps/procurement<br/>manifest.yaml + workflows + knowledge"]
        TIME["apps/timesheet<br/>manifest.yaml + workflows + knowledge"]
    end

    subgraph T2["Tier 2: Composable H-P-D-I Capabilities & Dynamic App Shell"]
        UI["workspace-ui (H & App Shell)<br/>Schema-Driven Kanban, Forms & Inbox"]
        CPROC["cap-process (P)<br/>Versioned Commands + ClamAV Quarantine"]
        CDATA["cap-data (D)<br/>OT Ingestion + Reconciled Parquet + DuckDB BI"]
        CAI["cap-ai (I)<br/>Schema OCR + Scoped Policy RAG"]
    end

    subgraph T1["Tier 1: Headless Open-Core PaaS (platform/)"]
        GW["Apache APISIX Gateway + Keycloak OIDC"]
        HASURA["Hasura GraphQL Engine<br/>(Row / Column / Relationship Permissions)"]
        KESTRA["Kestra Workflow Engine<br/>(Durable Event-Driven Flows)"]
        PG[("PostgreSQL 17 + pgvector<br/>core.* (shared) + domain.* (isolated)")]
        S3[("SeaweedFS + ClamAV<br/>Quarantine Files & Versioned Parquet")]
        BAO["OpenBao<br/>(Signed Audit Checkpoints)"]
    end

    subgraph CP["Out-of-Band Control Plane (operator/)"]
        OP["dxos-operator (Go)<br/>plan | apply | reconcile | uninstall | verify-audit"]
    end

    T3 -. "Declarative manifest.yaml" .-> OP
    OP -. "DDL, Version Trigger, Hasura Metadata & Flows" .-> T1
    T3 === UI
    UI ==> GW
    GW ==> HASURA & CPROC & CDATA & CAI
    HASURA ==> PG
    CPROC ==> PG & S3
    CDATA ==> PG & S3
    CAI ==> PG & S3
    PG ==> KESTRA
    KESTRA ==> CPROC & BAO
```

---

## 2. Architecture Decision Records (ADRs)

- [ADR 0001: Three-Tier Composable Architecture with Shared Core and Isolated Domain Tables](../adr/0001-three-tier-composable-architecture.md)
- [ADR 0002: Core Schema Purity and Semantic Cross-App Data Synchronization](../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md)
- [ADR 0003: Separation of Core Kernel Schema and Pluggable Capability Data Schemas (cap-data)](../adr/0003-modular-capability-schema-separation-for-iot-data.md)

---

## 3. Key References

- **Architecture Contract:** [`../../.agents/memory/architecture.md`](../../.agents/memory/architecture.md)
- **Data & Manifest Contract:** [`../../.agents/memory/data-contracts.md`](../../.agents/memory/data-contracts.md)
- **Security & Acceptance Scenarios (`S01`–`S08`):** [`../../.agents/rules/03-anti-patterns-and-security.md`](../../.agents/rules/03-anti-patterns-and-security.md)
