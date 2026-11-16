---
title: Three tiers
description: The Tier 1 infrastructure, Tier 2 capabilities, and Tier 3 application packages.
diataxis: explanation
audience: [evaluator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Three tiers

OpenDX-OS separates shared infrastructure, reusable capabilities, and declared
applications. A change should land in the lowest tier that can own it.

```mermaid
flowchart TB
    subgraph T3["Tier 3: Declarative domain applications (apps/*)"]
        direction LR
        MAINT["apps/maintenance<br/>manifest.yaml + workflows + knowledge"]
        PROC["apps/procurement<br/>manifest.yaml + workflows + knowledge"]
        TIME["apps/timesheet<br/>manifest.yaml + workflows + knowledge"]
    end

    subgraph T2["Tier 2: Composable H-P-D-I capabilities and App Shell"]
        UI["workspace-ui (H and App Shell)<br/>schema-driven Kanban, forms, inbox"]
        CPROC["cap-process (P)<br/>versioned commands + ClamAV quarantine"]
        CDATA["cap-data (D)<br/>OT ingestion + reconciled Parquet + DuckDB BI"]
        CAI["cap-ai (I)<br/>schema OCR + scoped policy RAG"]
    end

    subgraph T1["Tier 1: Headless open-core PaaS (platform/)"]
        GW["Apache APISIX gateway + Keycloak OIDC"]
        HASURA["Hasura GraphQL Engine<br/>row, column, relationship permissions"]
        KESTRA["Kestra workflow engine<br/>durable event-driven flows"]
        PG[("PostgreSQL 17 + pgvector<br/>core.* shared + domain.* isolated")]
        S3[("SeaweedFS + ClamAV<br/>quarantine files + versioned Parquet")]
        BAO["OpenBao<br/>signed audit checkpoints"]
    end

    subgraph CP["Out-of-band control plane (operator/)"]
        OP["dxos-operator (Go)<br/>plan | apply | reconcile | uninstall | verify-audit"]
    end

    T3 -. "declarative manifest.yaml" .-> OP
    OP -. "DDL, version trigger, Hasura metadata, flows" .-> T1
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

## Tier 1: shared infrastructure

Tier 1 is the headless platform. It provides the database, authentication,
gateway, workflow engine, object storage, scanning, and audit anchoring. Tier 1
never references Tier 2 or Tier 3 tables.

## Tier 2: capabilities and App Shell

Tier 2 provides reusable H-P-D-I services:

| Capability | Responsibility |
| --- | --- |
| `cap-hr` (H) | Departments and staff: who works where, which the `department` scope reads |
| `cap-process` (P) | Commands, business validation, and file lifecycle |
| `cap-data` (D) | OT validation/deduplication, reconciled export, DuckDB BI, and lineage |
| `cap-ai` (I) | Schema-driven OCR and scoped RAG with source citations |
| `workspace-ui` | Registry-driven navigation, Kanban/Calendar, forms, inbox, OT/BI, and OCR/RAG |

Capabilities own their schemas, register themselves in `core.capabilities`, and
are enabled per installation. Disabling a capability does not delete its stored
data.

## Tier 3: application packages

A Tier 3 package declares:

- `manifest.yaml`: capabilities, roles, shared access, domain tables, workflows,
  and optional knowledge sources;
- `workflows/*.yaml`: Kestra flows limited to declared, reviewable task types;
- `knowledge/*`: optional versioned knowledge sources.

The Operator installs packages out of band. Runtime services read published
configuration and never call the Operator per request.
