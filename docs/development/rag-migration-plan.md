# RAG compatibility and migration specification

**Status:** non-executed migration design. User approved preserving existing data and blocking migration when mappings are unclear, 2026-11-13. [ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md) records decisions; Conformance to accepted ADR 0005, remaining legacy-contract alignment and actual dataset inventory block executable DDL.

## Compatibility inventory and cutover

| Consumer/data | Existing / target | Required conversion and validation |
| --- | --- | --- |
| Organization/session | workspace claims and rows / one installation organization | explicit workspace-to-installation map; never merge multiple organizations implicitly; app_id does not equal department |
| Users/roles | workspace-era identity / core.users, app/capability roles | preserve subject IDs, role ownership and historical actor references; use DB-authoritative membership; Keycloak remains identity provider, with no business-role sync dependency |
| Departments/work | workspace/core work assumptions / cap_hr departments, cap_work items | explicit org_unit_id and record mappings; preserve ownership/links; no new Core FK to capability tables |
| Files/scanner | core.file_objects / core.files | preserve file ID, owner, object identity, app ID, checksum, original bytes, scan state and audit; block orphan/ambiguous app/owner mappings |
| App/registry | workspace publication/types / ADR 0005 app registry | inventory all active apps/roles; update Operator, Hasura metadata, runtime registry and UI serialization together |
| Existing package Markdown | filesystem lookup / immutable registered package sources | stable app/source IDs, checksum/provenance and canonical nested declaration; never import runtime private docs as package sources |
| Personal documents | no implemented library / registered library app + cap_ai owner policy | real app registration with generic policy, no fake nullable app IDs or broad role-readable originals |
| Audit/events/receipts | legacy scope keys / approved target identity | preserve actor/object lineage and replay identity; design audit-chain checkpoint compatibility before conversion |
| APIs/fixtures | legacy workspace/file fields / approved contract | explicit versioned compatibility; update integration actors, SQL seeds and direct API tests; no silent alias authority |

## Migration sequence and stop conditions

1. Inventory actual supported source schema/revision and dataset; export explicit mapping manifest, counts, key references and unresolved mappings. Archive before-images and tested backups under authorized retention. Do not log private bytes/keys.
2. Verify conformance to accepted ADR 0005 and the coordinated data/security/claim changes; verify target app/role/source definitions and D04 database-authoritative role administration and Hasura boundary. Stop if any owner/app/org mapping, retained audit link or original object identity is unresolved.
3. P2 defines versioned upgrade runner and narrowly scoped AI API/worker grants. Bootstrap-only init SQL is not an existing-installation migration strategy. Validate empty install separately from legacy upgrade.
4. Stage additive structures/conversion on a disposable restored database; compare counts, FKs, checksums, scan states and negative cross-owner/app reads through actual least-privilege connections. No blanket grant workaround.
5. Cut over compatible claims, File Service/scanner, registry/Hasura, Operator and capability clients in a declared maintenance/write-fence window. Verify old and new binaries cannot write incompatible source records concurrently. Secure/retire legacy RAG before enabling new access.
6. Enable metadata/lexical ingestion first, externally approved model features later. Keep retained originals and derived index rebuild boundaries separate. Remove legacy compatibility only after verification and an explicit follow-up decision.

## Rollback boundary

Before new-format writes, restore previous binaries/config and the verified database/object snapshot as one coordinated rollback. After new-format writes, never overwrite them with the old snapshot: stop writes, retain all new rows/objects/events, and require a tested reverse conversion or forward repair accepted for that dataset. Unsupported reverse mappings block rollback automation. This is a deliberate gate, not a promise of lossless arbitrary down-migration.

Disabled capabilities/apps retain data; removal of access happens before asynchronous cleanup. Index rollback may reactivate only a complete previous eligible version under current policy/source publication; rollback cannot widen grants or resurrect removed sources. Retention/purge of originals and derived assets requires a separate explicit policy.

## Required migration scenarios

`existing_workspace_file_upgrade`: preserve original IDs/hash/owner/scan/audit; unknown workspace/app map prevents any mutation. `post_cutover_new_write`: demonstrate forward repair or reverse conversion without dropping new files. `personal_source_unrelated_app_removed`: owner library survives and outsiders remain denied. `scan_in_progress_cutover`: unclean source cannot become searchable/downloadable. `registry_partial_failure`: last Ready app/config remains usable, desired/active distinction remains visible.

No migration, backup restore, DB authorization or rollback test has been executed by this documentation work.
