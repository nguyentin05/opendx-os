---
title: Backup and restore
description: Back up and restore database metadata, object storage, and audit state together.
diataxis: how-to
audience: [operator]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Backup and restore

Database metadata and object storage bytes must be backed up and restored
together. `core.files` stores immutable SHA-256 digests and object keys, so a
restore that changes either side breaks file verification.

## Quiesced backup

1. Stop write-accepting capability services so in-flight uploads and state
   transitions do not straddle the snapshot:

   ```bash
   docker compose stop cap-process
   ```

2. Run the backup script with the database URL and object directory:

   ```bash
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/backup.sh /var/backups/opendx-os/snapshot-01
   ```

3. Resume services:

   ```bash
   docker compose start cap-process
   ```

Record the application revision, enabled capabilities, and audit checkpoint
outside the backup set. A snapshot without its checkpoint cannot prove integrity
beyond the anchored tail.

## Verified restore

1. Stop `cap-process` before restoring:

   ```bash
   docker compose stop cap-process
   ```

2. Run the restore script:

   ```bash
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/restore.sh /var/backups/opendx-os/snapshot-01
   ```

   `restore.sh` verifies `objects.sha256` before restoring, then verifies that
   every `core.files` record has matching object bytes and a matching SHA-256
   digest before returning `0`.

3. Resume `cap-process` and reconcile Hasura metadata if it changed:

   ```bash
   docker compose start cap-process
   docker compose logs -f operator
   ```

4. Record the result with [Verification](../contribute/verification.md),
   including the revision, environment, and any checks that could not run.

## Model operation state

For optional semantic retrieval, model policy and durable operation state, follow
[model retrieval and answers](../guides/knowledge/models.md). Keep model features
disabled until predecessor and model gates pass and classification, provider,
retention, and deployment-adapter approvals exist. There is no reservation or
settlement to reconcile and no monetary account to release. An operation row in
`MAY_HAVE_SENT` or `UNCERTAIN` records that a call may have reached the provider:
never resend it, and never treat an expired period as permission to retry. If the
database applied the earlier model migration checksums, restore is not a valid
upgrade path; rebuild a named database as described in
[installation](installation.md#rebuilding-an-existing-model-installation).
