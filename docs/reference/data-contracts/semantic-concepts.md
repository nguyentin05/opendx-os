---
title: Semantic concept ownership
description: How shared domain concepts, write owners, and replicas are governed.
diataxis: reference
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

## 8. Semantic concept ownership

[ADR 0002](../../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md#3-three-scenario-governance-for-domain-attributes-tier-3)
defines three cases: independent domain attributes, an explicitly shared concept
identified by a `semanticType` URI, and conflicting declarations rejected during
planning/reconcile. A shared concept has one authoritative Write Owner and
Multi-Reader replicas; owner updates produce transactional outbox events for
workflow fan-out. Matching column names alone never establishes shared meaning.

Incompatible types or multiple Write Owners for the same concept must fail before
configuration is published. The manifest serialization, ownership registry,
replica delivery contract, and enforcing validator still need to be specified and
implemented; the current parser does not define `semanticType` fields. This section
records the accepted requirement without claiming that synchronization works.
