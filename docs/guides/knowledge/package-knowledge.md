---
title: Package knowledge
description: Publish knowledge sources as part of an application package.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-16
---

<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright 2026 DX-OS Open-Core Contributors -->
# Declarative package knowledge

An application can declare local Markdown or supported text PDF sources through
Knowledge v1. Its current Core publication governs access immediately; an index
or notification replica never grants access. App `Ready` and knowledge indexing
are separate states. See the [manifest contract](../../reference/rag/knowledge-contract.md),
[personal library](personal-library.md), and [verification](../../contribute/verification.md).

## Declaration and roles

In an installation with cap-ai enabled and qualified predecessors, add cap-ai to
the application's capability list and declare sources. For example, procurement's
included `knowledge/approval-policy.md` can be enabled with:

```yaml
knowledge:
  version: 1
  sources:
    - id: approval-policy
      path: knowledge/approval-policy.md
      mediaType: text/markdown
      indexingProfile: standard-text-v1
      access:
        mode: ROLE
        allowedRoles: [role_procurement_requester, role_procurement_manager, role_procurement_finance]
```

The ordinary procurement manifest does not enable cap-ai automatically. This
keeps its existing installation requirements independent of optional knowledge.
For RESTRICTED, declare `access.grantsManagedBy` referencing a
`knowledge.grantPolicies` entry containing app-owned `allowedRoles`. Never place
named-user grants in YAML. A manager's current role permits grant management,
without document READ. READ additionally requires a currently held allowed source
role and a named, unrevoked grant in the current authorization generation.
Application administrators have no bypass and personal files remain ungrantable.

## Components and configuration

Start the [AI overlay](models.md#compose-installation). The Go Operator scans
package bytes with ClamAV and stages verified immutable artifacts under
`RAG_PACKAGE_ARTIFACT_ROOT`. The Go knowledge dispatcher delivers durable
publication notifications from the Core outbox as its own service subject.
The Python API checks the current app envelope and queues committed version
IDs. The worker reads the same artifact volume read-only.

## Publication, access and UI

Only a current Ready publication can supply new previews, originals or
retrieval evidence. Withdrawing an app or changing its declaration makes
old versions ineligible immediately. A current READ role must be in the
manifest ceiling. RESTRICTED sources also require a named grant from an
authorized manager. Manager views withhold content and titles unless the
same caller independently qualifies for READ.

Application Knowledge shows retrieval and grant actions for the selected
app role. The Assistant resolves an eligible app role independently.
[Source authorization](../../reference/rag/authorization.md).

## Live qualification

Run the focused AI gate with `make test-ai` and the Operator publication
tests from its Go module. Smoke-test a published package and a revoked
role with the deployed service identities.
