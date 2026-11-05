# Design mistakes to recognize

Read when reviewing, or when an implementation approach might break an invariant. This is an explanation that points to the source of truth, not a copy of the entire rule set.

| Symptom | Consequence | Source to apply |
| --- | --- | --- |
| A `shared.tasks` table holds every app's columns/states | State and schema lifecycle become coupled across apps | [Shared entities](../contracts/data-contracts.md#2-shared-entities) |
| Only locking `UPDATE status` or only setting a SQL DEFAULT | Insert/upsert can put a record into an invalid state | [Write matrix](../contracts/data-contracts.md#4-write-matrix-and-field-ownership) |
| Only the Command API increments the version | Editing content through CRUD leaves an old approval request valid | [Version and command](../contracts/data-contracts.md#5-version-command-audit-and-retry) |
| FK by ID only, filtering workspace only on the root record | A record can reference another workspace's resource | [Identity and FK](../contracts/data-contracts.md#1-identity-and-same-workspace-links) |
| Using PostgreSQL GRANT or a UI button as all of Hasura authorization | Runtime permissions are not enforced on the right access path | [Write and read permissions](../contracts/data-contracts.md#4-write-matrix-and-field-ownership) |
| Updating the DB then calling Kestra once over HTTP | Effects are lost or duplicated on crash/retry | [Version, audit, and retry](../contracts/data-contracts.md#5-version-command-audit-and-retry) |
| Using only `entity_id` to deduplicate exports | Wrongly merges apps/workspaces or loses lineage | [Files, OT, and BI](../contracts/data-contracts.md#6-files-ot-and-bi) |
| Scanning a file, then allowing the clean object to be overwritten | The content used differs from the content scanned | [Files, OT, and BI](../contracts/data-contracts.md#6-files-ot-and-bi) |
| Letting the UI switch to any role for a demo | The client escalates the user's permissions itself | [Security](../contracts/security-and-acceptance.md) |
| A capability hard-codes one app, or bootstrap contains app DDL | Adding an app requires editing infrastructure/core | [Architecture boundaries](README.md#architecture-boundaries) |
| An unlocked hash-chain, with the checkpoint stored only in the same DB | The chain forks, or rewriting anchored history goes undetected | [Audit](../contracts/data-contracts.md#5-version-command-audit-and-retry) |
| Inserting the same header into every format | JSON, frontmatter, or shebang becomes invalid | [Source hygiene](../development/source-hygiene.md) |
| Treating a checklist as automation, or an unrun test as passing | The report does not reflect evidence | [Verification](../development/verification.md) |
