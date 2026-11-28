---
title: Native knowledge metadata and jobs
description: cap-ai metadata ownership, jobs, leases, delegations, and migration.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# Knowledge metadata and jobs

Alembic owns the `cap_ai` schema. Core owns users, roles, applications,
file originals and its own migration ledger. An AI document records a
private file identity and owner or an app and a declared source identity.
Versions identify verified bytes and current publication provenance.
Grants bind a named user to the app source's incarnation and authority
generation. A document has one desired version and at most one active
version.

Registration writes the document, version and job in one transaction.
The API dispatch loop publishes only committed IDs to PGMQ and records
broker acceptance. Expired or undispatched jobs are recovered with
bounded attempts. Celery acknowledges late and the worker claims a
fenced lease. Only the current lease can atomically activate complete,
eligible chunks. An unavailable optional model leaves full-text search
usable.

Each vector stores its producing immutable model revision and dimension.
Retrieval compares only compatible vectors in the current slot. A chunk
carries document, version, section and available page provenance.
Protected originals remain in Core File Service storage or immutable
package artifacts. [Migration and rebuild](../../operations/installation.md#rebuilding-an-existing-model-installation).
