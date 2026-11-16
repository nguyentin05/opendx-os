---
title: Native knowledge metadata and jobs
description: cap-ai metadata ownership, jobs, leases, delegations, and migration.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Native knowledge metadata and jobs

cap_ai owns the fourteen metadata/job tables: collections, documents, allowed
roles, named-user grants, processing policies, package sources/revisions and
publications, versions, processing runs, ingestion jobs/attempts, request receipts
and source delegations. Package reconciliation is scoped to the current Core
publication; see [package knowledge](../../guides/knowledge/package-knowledge.md). No cap_ai table is tracked by Hasura or granted to browser SQL roles.
Core has no capability/domain FK. Original, source, owner, recipe and version
identities are immutable; active/desired pointers have same-document deferred FKs.

`dxos_ai_owner` is a non-login owner. `dxos_ai_api` has current Core reads and
EXECUTE on scoped lifecycle functions, with no direct cap_ai table reads/writes
and no Core identity writes. `dxos_ai_worker` can only claim, heartbeat,
checkpoint, retry/recover and validate exact-source delegation through reviewed
functions; it cannot change document ACLs or active pointers directly.
`dxos_ai_dispatcher` executes scoped package snapshot/reconcile and Core knowledge
notification claim/acknowledgment functions; it has no arbitrary chunk or grant access. SECURITY DEFINER functions use a fixed search path; the controlled public
schema is needed by the existing Core audit hash trigger's pgcrypto dependency,
and PUBLIC cannot create there. Sign-in remains the sole identity mirror.

`POST /api/cap/ai/documents` accepts only `collection_id?`, `file_id`, `title?`
and `parser_profile`. `standard-text-v1` pins local recipe limits without claiming
a qualified parser. The server resolves the current library and owner, accepts a
CLEAN OWNER_ONLY file, and atomically writes document/version/PARSE job, audit and
receipt. `Idempotency-Key` is required. Equal retries reconstruct the same IDs
after current authorization; changed payload returns 409. Different keys for the
same owner/collection/source/recipe converge. There is no cross-owner hash dedup.
Only CLEAN PDF sources under the supported text recipe are admitted in this slice;
owner-authorized unsupported MIME/recipe requests return 415 without durable work.
Source existence is concealed before reporting owner-only admission errors.

`POST /documents/{id}/versions` accepts only `file_id` and `parser_profile` and
requires an `If-Match: "<revision>"` header. Receipt replay precedes revision
comparison but follows current authority. A new desired version preserves the
old active pointer. A request to reselect a superseded or terminal source/recipe
returns 409; it cannot report a cancelled historical job as newly queued. A fresh
private file identity is required for another processing request in that case.
`GET /documents`, `/documents/{id}`, `/collections?kind=PERSONAL`
and `/ingestions/{job_id}` expose only owner metadata and safe error codes.
Lists have bounded owner/library-bound cursors and no total count, worker identity,
object key or original bytes. Other owners and application admins receive 404.
`DELETE /documents/{id}` requires the key/revision, tombstones atomically,
cancels live work/delegations and appends audit; evidence/originals are retained.
No policy-edit, sharing, search, parsing or model API is enabled by this slice.

Jobs use SKIP LOCKED, a 60-second lease, 15-second heartbeat interval, at most two
live leases per worker and five attempts. Every claim creates a new attempt/run
and increments the fence. Retry uses bounded exponential full jitter; recovery
reclaims expired work without deleting originals/history. Activation remains
unavailable until a qualified derived-content pipeline exists. Delegations use
256-bit random opaque tokens, persist hashes only, expire no later than the lease,
and bind worker/job/fence/run/source/hash/owner/local policy. Validation reads
metadata only. The internal File Service validates before and after bounded object
retrieval and independently enforces CLEAN/hash/owner/private policy.

Bootstrap runs the same reviewed migrations through `09_rag_migrations.sh`, after
the app-capability registry exists; upgrades use `scripts/migrate-rag.sh` with
`--scope core --through 0001` then `--scope cap-ai --through 0001`. Both paths use
the shared checksum/version ledger and advisory lock. Rollback disables routes
and workers while retaining source guards, originals, metadata and evidence.
