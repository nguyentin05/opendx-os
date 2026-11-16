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

Apply Core migration 0003 and cap_ai migration 0003 through the checksum-verified
migration runners before deploying the package consumers. Fresh bootstrap and
Kubernetes ConfigMap projection include the same files. Legacy data, source
restrictions and audit remain retained when features are disabled.

The Operator requires `RAG_PACKAGE_ARTIFACT_ROOT` and `CLAMAV_ADDRESS` for a
package containing knowledge sources. The artifact root is deployment-owned and
private; source reads are confined to the package root, bounded and scanned.
Approved copies remain available after the mutable package directory or Operator
process disappears. Mount that same artifact root read-only into cap-ai; the
parser receives bytes only and keeps its qualified sandbox restrictions.

cap-ai requires `RAG_PACKAGE_ENABLED=true` plus source, metadata and parser
qualification flags, `RAG_PACKAGE_ARTIFACT_ROOT`, `AI_DISPATCHER_DATABASE_URL` and
`RAG_DISPATCHER_SERVICE_SUBJECT`. The latter is a third distinct deployment-owned
OIDC service subject, separate from the worker and File Service subjects. Internal
listeners remain off the public gateway. The dispatcher database role can only
claim/acknowledge knowledge notifications and invoke scoped package reconciliation;
it cannot read arbitrary chunks or change grants. The user API database role
cannot reconcile packages.

Build and run `operator/cmd/dxos-knowledge-dispatcher` with `DATABASE_URL` for the
narrow dispatcher role, `RAG_INTERNAL_AI_URL`, `RAG_DISPATCHER_BEARER_TOKEN` and
the predecessor qualification flags. It leases notifications durably and retries
with bounded delay. `--once` processes at most one notification for verification.
The workflow publisher leaves knowledge notifications to this consumer. Neither
service logs credentials or response bodies.

Give the worker `RAG_INTERNAL_AI_URL` for its package-source delegation endpoint,
in addition to its existing private-upload File Service endpoint. Provision the
actual verified OIDC worker subject as an ACTIVE Core identity for package
activation audit. A package has no uploader; no fake file or owner is inserted.
Service tokens expire and must be renewed by deployment-owned credential handling.

## Publication, access and UI

A successful changed publication commits generation, source incarnation,
authorization generation and a durable notification together. Reconciliation is
idempotent. Changing mode, role ceiling or manager policy retires grants even if
the dispatcher misses intermediate A → B → A publications. Removal immediately
blocks original, metadata, citation, search and delegated worker access; readding
the same declaration cannot restore retired grants. Content changes keep an old
eligible active version until the replacement completes. Parser failure does not
erase an eligible active version. Returning to historical content queues a fresh
fenced job when another version is active, retaining canonical version identity,
terminal job history and immutable COMPLETE runs.

The shell discovers App knowledge through the current registry. It displays
indexing/failure separately from App Ready, searches the current app and role,
and loads citations through protected APIs. Switching roles/apps/users discards
old requests and grant state. Grant management displays role ceilings, revisions
and named-user grant state without title, excerpts or artifact references. When
Core has advanced ahead of reconciliation, management reports a pending
publication and rejects a new write until the manager refreshes after delivery.
A stale ETag returns 409 and requires a fresh decision; it is not retried as a
new grant automatically.

Public additions are `GET /api/cap/ai/app-knowledge?app_id=...`,
`GET /documents/{id}/grant-management`, `POST /documents/{id}/grants` and
`DELETE /documents/{id}/grants/{user_id}` under `/api/cap/ai`. Mutations require
`If-Match` and `Idempotency-Key`. Search accepts `scope: {kind: APP, app_id}`;
personal scope remains separate. No hosted generation, embedding or reranking
is enabled by this slice.

## Live qualification

Run `scripts/test-rag.sh package` on a disposable PostgreSQL 17 installation. It
runs the source gate, then real Operator publication, scanner/artifact admission,
authenticated dispatcher delivery, worker activation, ROLE/RESTRICTED A/B and
manager/admin probes, missed A/B/A, delayed remove/reintroduce, content replacement,
completed/cancelled content A → B → A, and DB role revocation with the same JWT. It verifies existing personal documents
are unchanged. Each publish uses a fresh Operator process, and delivery is
explicitly delayed between publications.

Supply all source/metadata variables, the built `RAG_TEST_OPERATOR` and
`RAG_TEST_DISPATCHER` paths, host-side `RAG_PACKAGE_ARTIFACT_ROOT`,
`CLAMAV_ADDRESS`, `AI_DISPATCHER_DATABASE_URL`, `RAG_INTERNAL_AI_URL`,
`RAG_TEST_INTERNAL_AI_URL`, `RAG_DISPATCHER_BEARER_TOKEN`, predecessor qualification
flags, and optional `RAG_TEST_FIXTURE_ROOT` for private temporary fixture files.
`RAG_TEST_COMPOSE_FILE` must name a JSON Compose configuration for an explicitly
owned `dxos-rag-*` disposable stack with a `worker` service running
`/live/bin/cap-ai-worker`. The runner deliberately stops/starts only that worker
to verify readiness while indexing is paused. It restores the worker on exit.
Credentials remain private environment/configuration inputs and are never printed.

The package runner also requires `RAG_TEST_UI_URL` and real IdP storage states
`RAG_TEST_UI_STORAGE_A`, `_B` and `_ADMIN` for Chromium app search/citation,
manager grant controls without READ, and app/role switching. Prepare them after
the UI build, using the real browser installation described in the personal guide. Missing prerequisites are NOT_RUN, never PASS. Runtime support
is bounded by the qualified parser envelope and observed live evidence; this
page is not itself a test result or permission to enable another installation.

Optional semantic retrieval and grounded answers build on these same source and
grant checks; see [model retrieval and answers](models.md). Model
readiness does not change application Ready or lexical readiness.
