---
title: OpenDX-OS documentation
description: Entry point for evaluators, operators, application authors, and security reviewers.
diataxis: explanation
audience: [evaluator, operator, app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# OpenDX-OS documentation

OpenDX-OS is a composable three-tier platform for business applications. Tier 3
applications declare their data, permissions, workflows, and UI in a package;
Tier 2 capabilities provide shared H-P-D-I services; Tier 1 provides the database,
authentication, gateway, workflow engine, and object storage.

## Start from your task

| I want to... | Start here |
| --- | --- |
| Evaluate the platform and see an application work | [Run the local stack](get-started/local-stack.md) |
| Add or change a declarative application | [Adding an application](guides/adding-an-app.md) |
| Look up a contract, manifest field, or endpoint | [Reference](reference/index.md) |
| Understand the architecture and accepted decisions | [Architecture](architecture/index.md) and [ADRs](adr/index.md) |
| Install, upgrade, or recover a deployment | [Operate](operations/index.md) |
| Contribute code, tests, documentation, or releases | [Contribute](contribute/index.md) |
| Review security properties and acceptance evidence | [Security and acceptance](reference/security/index.md) |

## The model in one screen

- One installation serves one organization. Isolation is by application and
  department, not by a per-workspace tenant key.
- Keycloak authenticates identity; the platform authorizes through
  `core.user_roles` on every request.
- Each application owns roles named with the `role_` prefix and one or more
  `domain.*` tables generated from its manifest.
- Reads and edits go through Hasura; state transitions go through cap-process
  commands with an expected version.
- The Operator installs packages out of band. Runtime services do not call it on
  each request.
- Workflows are declared Kestra flows deployed by the Operator and fed by durable
  events.

## Documentation map

| Section | Purpose |
| --- | --- |
| [Get started](get-started/index.md) | Tutorial: run the stack and complete a first flow |
| [Guides](guides/index.md) | Task guides for application authors and platform developers |
| [Reference](reference/index.md) | Canonical contracts: manifest, data, security, API, CLI, RAG |
| [Architecture](architecture/index.md) | Tiers, control plane, runtime, security model, anti-patterns |
| [ADRs](adr/index.md) | Accepted and proposed architecture decisions |
| [Operate](operations/index.md) | Installation, backup, restore, and recovery |
| [Contribute](contribute/index.md) | Verification, hygiene, releases, and writing docs |

The documentation is written in English and uses the
[Diataxis](https://diataxis.fr/) page types: tutorial, how-to, reference, and
explanation. See [Writing docs](contribute/writing-docs.md) before adding a page.
