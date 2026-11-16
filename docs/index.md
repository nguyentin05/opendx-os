---
title: OpenDX-OS documentation
description: Entry point for operators, application authors, platform developers, and security reviewers.
diataxis: explanation
audience: [evaluator, operator, app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# OpenDX-OS documentation

OpenDX-OS is a composable three-tier platform for business applications. Tier 3
applications declare their data, permissions, workflows, and UI in a package;
Tier 2 capabilities provide shared H-P-D-I services; Tier 1 provides the
database, authentication, gateway, workflow engine, and object storage.

Start from the path that matches your task.

| I want to... | Start here |
| --- | --- |
| Run the platform and see an application | [Get started](get-started/index.md) |
| Add or change a declarative application | [Guides](guides/index.md) |
| Look up a contract, manifest field, or endpoint | [Reference](reference/index.md) |
| Understand the architecture and accepted decisions | [Architecture](architecture/README.md) |
| Read a decision record | [ADR index](adr/index.md) |
| Install, upgrade, or recover a deployment | [Operate](operations/installation.md) |
| Contribute code, tests, or releases | [Contribute](contribute/index.md) |

!!! info "Documentation migration in progress"
    The repository is moving to a single published documentation set. Files under
    `docs/_internal/` are retained for history and are excluded from the site.

## The model in one screen

- One installation serves one organization. Isolation is by application and
  department, not by a per-workspace tenant key.
- Keycloak authenticates identity; the platform authorizes through
  `core.user_roles`.
- Each application owns roles named with the `role_` prefix and one or more
  `domain.*` tables generated from its manifest.
- Reads and edits go through Hasura; state transitions go through cap-process
  commands with a version check.
- The Operator installs packages out of band. Runtime services do not call it on
  each request.
