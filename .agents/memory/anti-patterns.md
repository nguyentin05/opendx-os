# Design mistakes to recognize

Read when reviewing, or when an implementation approach might break an invariant. This is an explanation that points to the source of truth, not a copy of the entire rule set.

| Symptom | Consequence | Source to apply |
| --- | --- | --- |
| A `shared.tasks` table holds every app's columns/states | State and schema lifecycle become coupled across apps | [Data: sections 2–3](data-contracts.md#2-shared-entities) |
| Only locking `UPDATE status` or only setting a SQL DEFAULT | Insert/upsert can put a record into an invalid state | [Write matrix](data-contracts.md#4-write-matrix-and-field-ownership) |
| Only the Command API increments the version | Editing content through CRUD leaves an old approval request valid | [Version and command](data-contracts.md#5-version-command-audit-and-retry) |
| FK by ID only, filtering workspace only on the root record | A record can reference another workspace's resource | [Identity and FK](data-contracts.md#1-identity-and-same-workspace-links) |
| Using PostgreSQL GRANT or a UI button as all of Hasura authorization | Runtime permissions are not enforced on the right access path | [Rule 02](../rules/02-data-contracts-and-permissions.md) |
| Updating the DB then calling Kestra once over HTTP | Effects are lost or duplicated on crash/retry | [Version, audit, and retry](data-contracts.md#5-version-command-audit-and-retry) |
| Using only `entity_id` to deduplicate exports | Wrongly merges apps/workspaces or loses lineage | [Files, OT, and BI](data-contracts.md#6-files-ot-and-bi) |
| Scanning a file, then allowing the clean object to be overwritten | The content used differs from the content scanned | [Files, OT, and BI](data-contracts.md#6-files-ot-and-bi) |
| Letting the UI switch to any role for a demo | The client escalates the user's permissions itself | [Rule 03](../rules/03-anti-patterns-and-security.md) |
| A capability hard-codes one app, or bootstrap contains app DDL | Adding an app requires editing infrastructure/core | [Rule 01](../rules/01-architecture-and-boundaries.md) |
| An unlocked hash-chain, with the checkpoint stored only in the same DB | The chain forks, or rewriting anchored history goes undetected | [Audit](data-contracts.md#5-version-command-audit-and-retry) |
| Inserting the same header into every format | JSON, frontmatter, or shebang becomes invalid | [Rule 04](../rules/04-pof-50-compliance.md) |
| Treating a checklist as automation, or an unrun test as passing | The report does not reflect evidence | [Verification gate](../hooks/pre-pr-gate.md) |
