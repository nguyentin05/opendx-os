---
title: Design mistakes to recognize
description: Recurring design mistakes and how to avoid them.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Design mistakes to recognize

Read when reviewing, or when an implementation approach might break an invariant. This is an explanation that points to the source of truth, not a copy of the entire rule set.

| Symptom | Consequence | Source to apply |
| --- | --- | --- |
| A `shared.tasks` table holds every app's columns/states | State and schema lifecycle become coupled across apps | [Shared entities](../reference/data-contracts/shared-tables.md) |
| Only locking `UPDATE status` or only setting a SQL DEFAULT | Insert/upsert can put a record into an invalid state | [Write matrix](../reference/data-contracts/writes-and-versions.md) |
| Only the Command API increments the version | Editing content through CRUD leaves an old approval request valid | [Version and command](../reference/data-contracts/writes-and-versions.md) |
| FK by ID only, filtering application or department only on the root record | A record can reference another application's or department's resource | [Identity and FK](../reference/data-contracts/identity-and-roles.md) |
| Using PostgreSQL GRANT or a UI button as all of Hasura authorization | Runtime permissions are not enforced on the right access path | [Write and read permissions](../reference/data-contracts/writes-and-versions.md) |
| Updating the DB then calling Kestra once over HTTP | Effects are lost or duplicated on crash/retry | [Version, audit, and retry](../reference/data-contracts/writes-and-versions.md) |
| Using only `entity_id` to deduplicate exports | Wrongly merges applications or entity types, or loses lineage | [Files, OT, and BI](../reference/data-contracts/files-ot-bi.md) |
| Scanning a file, then allowing the clean object to be overwritten | The content used differs from the content scanned | [Files, OT, and BI](../reference/data-contracts/files-ot-bi.md) |
| Letting the UI switch to any role for a demo | The client escalates the user's permissions itself | [Security](../reference/security/index.md) |
| A capability hard-codes one app, or bootstrap contains app DDL | Adding an app requires editing infrastructure/core | [Architecture boundaries](index.md#architecture-boundaries) |
| An unlocked hash-chain, with the checkpoint stored only in the same DB | The chain forks, or rewriting anchored history goes undetected | [Audit](../reference/data-contracts/writes-and-versions.md) |
| Inserting the same header into every format | JSON, frontmatter, or shebang becomes invalid | [Source hygiene](../contribute/source-hygiene.md) |
| Treating a checklist as automation, or an unrun test as passing | The report does not reflect evidence | [Verification](../contribute/verification.md) |
