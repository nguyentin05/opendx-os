---
title: Files, OT, and BI
description: File quarantine, OT deduplication, export reconciliation, and lineage.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Files, OT, and BI

## Files

New files start as `QUARANTINED`; only the scanning service moves them to `CLEAN` or `INFECTED`. Download, OCR, and commands that use a file must check the caller's permission (role and scope) and `CLEAN` status. Scanned content must not be overwritten under the same file identity; replacing a file creates a new identity and requires a rescan. A scanner error keeps the file unclean.

## OT and BI

The OT and BI requirements below are accepted but not yet implemented: the
cap-data handlers still target retired core tables and move to the `cap_data`
schema in v1.1.0 (#111). `cap_data.ot_events`, `export_batches` and
`data_lineage` exist, but no service writes them yet.

OT validates the device, stores one event per `(device_id, event_dedup_key)`, and emits the standard event `ot.event.accepted`. The app's declared workflow evaluates thresholds and creates the appropriate work. The capability does not pre-select the maintenance app; duplicate delivery adds no further business effects.

The full BI source key is `(app_id, entity_type, entity_id, entity_version)`. Export only records in the app-declared `terminalStates`, and read only successfully published batches. Re-exporting the same source version adds no contribution; choose the latest version by `(app_id, entity_type, entity_id)`. Two entity types with the same UUID remain two distinct sources. Two exports with the same version key but different content must be detected as a reconciliation error rather than arbitrarily choosing one.

Lineage must preserve the full source record set behind each aggregate metric, not just one representative ID. Check the registry, batch, receipt, and lineage DDL against these requirements before connecting consumers; the presence of a table alone does not prove compliance.
