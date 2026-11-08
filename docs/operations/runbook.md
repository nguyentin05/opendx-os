# OpenDX-OS Operational Runbook

## Coordinated Backup & Restore Procedure

Because `core.file_objects` stores immutable SHA-256 digests and S3 object keys, database metadata and object storage bytes must be backed up and restored together while writes are quiesced.

### 1. Quiesced Backup

1. Stop write-accepting capability services to quiesce in-flight uploads and state transitions:
   ```bash
   docker compose stop cap-process
   ```
2. Run `scripts/backup.sh` with `DATABASE_URL` and `OBJECTS_DIR`:
   ```bash
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/backup.sh /var/backups/opendx-os/snapshot-01
   ```
3. Resume services:
   ```bash
   docker compose start cap-process
   ```

### 2. Verified Restore

1. Stop `cap-process` before restoring:
   ```bash
   docker compose stop cap-process
   ```
2. Run `scripts/restore.sh`:
   ```bash
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   OBJECTS_DIR=/var/lib/opendx-os/objects \
   ./scripts/restore.sh /var/backups/opendx-os/snapshot-01
   ```
   `restore.sh` verifies `objects.sha256` before restoring and verifies that every `core.file_objects` record has matching object bytes and SHA-256 digest on disk before returning `0`.
3. Resume `cap-process` and re-reconcile Hasura metadata if needed:
   ```bash
   docker compose start cap-process
   ```
