---
title: RAG authorization
description: Source authorization and identity model for native RAG.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# RAG authorization and source trust

## Document access

Core owns ACTIVE user status, role membership, enabled capabilities and
Ready applications. Application source reads require a qualifying
app-owned role within the manifest ceiling. RESTRICTED mode also requires
a named-user grant for the current source incarnation and authorization
generation. MANAGE is checked independently and does not confer READ.
Personal knowledge requires the current owner and live library eligibility.
Platform Admin does not bypass either path.

Search authorizes current source candidates before lexical or vector
ranking, then checks current access before returning content. Citation
previews and originals check live source version and authority again.
When Core publishes a different generation or withdraws an application,
an old indexed version stops qualifying immediately.

## Assistant source selection and historical transcripts

The Assistant resolves qualifying roles from the caller's current Core
membership for each source app. Its search tool can narrow results to
personal or one application. Historical messages are visible only to
their owner. Current reads and model egress reauthorize retained
provenance. Revoked or unprovable derived context is dropped before
reuse. Access to a historical message never grants live citation access.

## Service and database authority

API, worker, dispatcher and broker logins are distinct. Core access uses
narrow SQL grants or defined source-authority functions. The Go File
Service serves private bytes using its verified worker identity, fenced
delegation and live owner-only file checks. The dispatcher uses its own
service identity and existing Core publication outbox. Internal paths
are outside the public APISIX route.

## Model-processing policy

Hosted egress requires an explicit applicable allow for the bound
revision, purpose and each data class in the prompt. An installation
denial cannot be overridden by an application. Local access still
requires current actor or job authority and an approved destination.
Enforce purpose limits, exact transport URL, DNS-pinned connections and
no redirects before sending credentials or content.

The persisted operation key protects against replay after uncertainty.
Run no second send for the key, including retries from the task executor,
SDK or graph. The provider key is encrypted in PostgreSQL and remains
write-only at the API. [Model administration](../../guides/knowledge/models.md).
