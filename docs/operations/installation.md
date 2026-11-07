# OpenDX-OS Installation Guide

## Prerequisites

- Docker Engine & Docker Compose v2
- Go 1.24+ (with `GOTOOLCHAIN=auto` for Go 1.27.3 module builds)
- Node.js 22+ and npm (for building `workspace-ui`)
- Python 3.12+ with `PyYAML` (for hygiene and contract verification)
- `postgresql-client` (`psql`, `pg_dump`) for database operations

## Out-of-Tree Archive Installation

1. Build the release archive:
   ```bash
   ./scripts/package_release.sh 0.1.0-demo
   sha256sum -c dist/dxos-open-core-0.1.0-demo.tar.gz.sha256
   ```

2. Extract the archive outside the Git repository and run the installer:
   ```bash
   tar -xzf dist/dxos-open-core-0.1.0-demo.tar.gz -C /tmp
   /tmp/dxos-open-core-0.1.0-demo/scripts/install.sh /opt/opendx-os
   ```

3. Configure environment variables and start services:
   ```bash
   cd /opt/opendx-os
   cp .env.example .env
   docker compose up -d
   ```

4. Validate and apply application manifests using `dxos-operator`:
   ```bash
   ./bin/dxos-operator validate contracts/fixtures/procurement.yaml
   HASURA_URL=http://localhost:8080 HASURA_ADMIN_SECRET=dxos_admin_secret \
   DATABASE_URL=postgres://dxos_admin:dxos_secret@localhost:5432/dxos_db?sslmode=disable \
   ./bin/dxos-operator apply contracts/fixtures/procurement.yaml
   ```
