---
title: Architecture overview
description: Tiers, components, the control plane, and how decisions change contracts.
diataxis: explanation
audience: [evaluator, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Architecture overview

OpenDX-OS is a composable platform for business applications. An application
declares its data, permissions, workflows, and UI configuration in a package; the
platform generates and serves it without a per-application backend.

This section describes the architecture and says, where it matters, which parts
are implemented and which are planned. The [roadmap](https://github.com/nguyentin05/opendx-os/blob/main/ROADMAP.md)
tracks the planned parts.

| Page | Covers |
| --- | --- |
| [Three tiers](tiers.md) | Tier boundaries and the end-to-end diagram |
| [Control plane](control-plane.md) | The Operator, reconcile, upgrades, and the Kubernetes controller |
| [Runtime](runtime.md) | Request-path services, capabilities, storage, and events |
| [Security model](security-model.md) | Identity, authorization, scopes, and audit |
| [Anti-patterns](anti-patterns.md) | Recurring design mistakes |

## Architecture boundaries

- **Tier 1** provides shared infrastructure in `platform/`. PostgreSQL, Hasura,
  Keycloak, `dxos-authz`, APISIX, Kestra, object storage and scanning are shared
  by every application.
- **Tier 2** provides reusable H-P-D-I capabilities in `capabilities/` and the
  schema-driven App Shell in `workspace-ui/`.
- **Tier 3** packages domain declarations in `apps/<app>/manifest.yaml`,
  `workflows/*.yaml`, and `knowledge/*.md`; there is no per-app backend.
- The Go `operator/` is the control plane outside the synchronous request path:
  the `dxos-operator` CLI (and the Compose reconcile loop) and the
  `dxos-controller` Kubernetes controller. Running applications continue serving
  when it stops.
- Adding an app using supported capabilities changes its package and published
  configuration. A new capability may require Tier 2 extensions with an explicit
  contract change.
- Shared runtime code, configuration, and bootstrap DDL consume manifest
  identifiers rather than branching on app names. Domain names in examples,
  fixtures, and tests must not become a runtime app list.
- The Operator generates domain-table DDL from manifests. Bootstrap creates
  shared structures and enabled capability storage. OT will publish standard
  events, and declared application workflows supply thresholds and business
  responses (cap-data, v1.1.0).

## Decisions and contract changes

The [ADRs](../adr/index.md) record accepted decisions and rationale; the
[data contracts](../reference/data-contracts/index.md) and
[manifest reference](../reference/manifest/index.md) describe their current
application. ADR 0002 refines domain-attribute ownership; ADR 0003 refines
optional capability storage while retaining the three tiers. ADR 0004
introduces the `DXApplication` CRD controller. ADR 0005 defines one organization
per installation with application and department isolation. ADR 0006 keeps
application logic in declarations. ADR 0007 keeps authentication in Keycloak and
authorization in the platform. ADR 0008 lets an application declare several
tables. ADR 0009 sets identity and source boundaries for native RAG.

For a proposed change, identify relevant accepted decisions and affected
contracts, producers, consumers, examples, and data migrations. Record a
replacement decision in an ADR with explicit supersession when changing an
accepted decision. Update the current contracts and affected consumers within
scope; record pending work when implementation is separate. A newer date or
divergent code alone does not override an accepted requirement. Name conflicting
sources and resolve them against the agreed requirement; ask only when a
product/architecture choice is still unassigned.

## Architecture verification

Verify plugging in an app beyond the three examples without core edits, runtime
operation while the Operator is stopped, capability disablement, and reconcile
across apps sharing resources. Check for circular dependencies and domain logic
leaking into shared code. Report a finding's location, failure scenario, impact,
and fix direction using the [verification guidance](../contribute/verification.md).
