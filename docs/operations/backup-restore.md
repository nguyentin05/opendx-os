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

`scripts/backup.sh` and `scripts/restore.sh` work on `OBJECTS_DIR`, a directory
that holds each object of the attachment bucket (`S3_BUCKET`, default
`dxos-attachments`) at its `object_key` path (`<app_id>/<file id>`). SeaweedFS's
own data directory (`seaweedfs-data`, served with `-dir=/data`) stores volumes,
not files by key, so copy the bucket into such a directory with an S3 client
before the backup, and back into the bucket after the restore. The Compose
stack does not publish the SeaweedFS S3 port; run the client inside the Compose
network. `restore.sh` empties `OBJECTS_DIR` before copying the backup in.

## Quiesced backup

1. Stop write-accepting capability services so in-flight uploads and state
   transitions do not straddle the snapshot:

   ```bash
   docker compose stop cap-process cap-ai
   ```

   (`cap-ai` runs only with `compose.rag.yaml`.)

2. Copy the bucket into `OBJECTS_DIR`, then run the backup script with the
   database URL and object directory:

   ```bash
   DATABASE_URL='postgres://<POSTGRES_USER>:<POSTGRES_PASSWORD>@localhost:5432/<POSTGRES_DB>?sslmode=disable' \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/backup.sh /var/backups/opendx-os/snapshot-01
   ```

3. Resume services:

   ```bash
   docker compose start cap-process cap-ai
   ```

Record the application revision and enabled capabilities outside the backup
set. Audit chains are not anchored outside PostgreSQL yet, so a snapshot alone
does not prove that its audit history was not rewritten.

## Verified restore

1. Stop `cap-process` before restoring:

   ```bash
   docker compose stop cap-process
   ```

2. Run the restore script:

   ```bash
   DATABASE_URL='postgres://<POSTGRES_USER>:<POSTGRES_PASSWORD>@localhost:5432/<POSTGRES_DB>?sslmode=disable' \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/restore.sh /var/backups/opendx-os/snapshot-01
   ```

   `restore.sh` verifies `objects.sha256` before restoring, then verifies that
   every `core.files` record has matching object bytes and a matching SHA-256
   digest before returning `0`. Copy `OBJECTS_DIR` back into the bucket
   afterwards.

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
