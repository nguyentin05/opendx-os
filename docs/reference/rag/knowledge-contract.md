---
title: Knowledge contract
description: The knowledge v1 manifest, publication envelope, and private files.
diataxis: reference
audience: [app-author, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Knowledge v1

`spec.knowledge` is optional and explicitly versioned (`version: 1`). It requires
cap-ai. `personalLibrary` declares `enabled` and nonempty `allowedRoles` drawn
from the app-owned roles. A single enabled library is permitted per installation.
A validated enabled library or nonempty `sources` permits `tables: []`, with no
table commands/workflows. This refines ADR 0008; a library owns no dummy table.

`sources` entries declare `id`, safe relative `path` under `knowledge/`,
`mediaType` (`text/markdown` or `application/pdf`), `indexingProfile`
(`standard-text-v1`), and nested `access`. Paths may not traverse or resolve
symlinks outside the package root. `access.mode` is ROLE or RESTRICTED;
`access.allowedRoles` is nonempty and app-owned. RESTRICTED requires
`access.grantsManagedBy` naming a `grantPolicies` map entry whose nonempty
`allowedRoles` lists app-owned manager roles. Management does not grant READ.
ROLE has no manager policy. Flat aliases, unknown fields/versions and cross-app
roles are invalid. Package publication is implemented independently of indexing;
package reconciliation, grants and serving require their own acceptance gates.

The Operator stores a server-only `spec._publication` envelope, never accepted in
user manifests or CRDs. It contains `generation`, `spec_digest`, `package_digest`
and `sources`, each binding `source_id`, `incarnation`,
`authorization_generation`, `declaration_hash`, `artifact_ref`, `sha256`,
`source_path`, `media_type`, `size_bytes`, `parser_profile` and `safety_state`.
`core.apps.publication_generation` matches the envelope. Stored-spec decoders
retain it across unrelated application changes; public declaration schemas do not
expose artifact references or permit generation selection.

A successful changed publication increments its generation in the registry
transaction; an identical reconcile keeps it. Continuously declared sources keep
incarnation. Removal/reintroduction replaces incarnation. Changes to mode, sorted
allowed-role set, manager-policy ID or resolved manager-role set increment source
authorization generation, including A → B → A. Content-only changes preserve that
generation. Failed staging/publication preserves the previously active definition.
The full apply is serialized across Operators, including DDL and Hasura snapshot
handling; this does not place the Operator on user request paths.

Before publication, package bytes are opened relative to a confined package root,
bounded to 20 MiB, verified against declared MIME and scanned locally by ClamAV.
The Operator requires deployment-owned `RAG_PACKAGE_ARTIFACT_ROOT` (private local
storage) and `CLAMAV_ADDRESS`; absent configuration denies package publication.
Approved bytes are copied to immutable app/source/hash references and verified;
mutable package files and Operator uptime are no longer source-read dependencies.
This admission does not establish parser support for arbitrary PDFs. Unreferenced
staged artifacts remain retained pending an approved cleanup policy.

The registry transaction writes a `knowledge.publication` durable outbox event
containing only `(app_id, generation, spec_digest)`. The workflow publisher does
not claim these events. A dedicated knowledge dispatcher must validate current
Core authority; the notification and delayed cap_ai state never authorize a source.

`core.files.read_policy` is immutable: APP_ATTACHMENT (default, including legacy
rows) or OWNER_ONLY. Original identity, owner, app, content hash and policy cannot
be relabelled. `POST /api/cap/process/files/private/upload` accepts only `app_id`
and `file` multipart fields; the server stamps OWNER_ONLY and verified owner.
It requires current DB membership in a Ready library's declared allowedRoles and
enabled dependencies, with no admin bypass. Ordinary upload keeps APP_ATTACHMENT.
Downloads independently enforce policy, CLEAN status and hash, and repeat current
private authorization after bounded object retrieval before emitting bytes.
Protected file routes use strict JWT verification and uncached shared authz;
identity mirroring remains a sign-in responsibility. Tokens require platform email
claims; legacy identity-only tokens are incompatible with these protected routes.

Hasura file permission branches constrain OWNER_ONLY to the owner, current ACTIVE
user, current role membership, Ready app and enabled dependencies. Attachment
relationships cannot widen the private branch. No cap_ai table is tracked.
Private upload defaults off (`RAG_PRIVATE_UPLOAD_ENABLED` absent/false); enable
only in a disposable qualified installation after the live source gate below.
Disablement preserves originals and private guards; no destructive rollback.
