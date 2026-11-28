---
title: Run the local stack
description: Start OpenDX-OS on a workstation with Docker Compose.
diataxis: tutorial
audience: [evaluator, operator]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Run the local stack

From the repository root:

```bash
scripts/init-env.sh          # writes .env with random secrets
docker compose up -d --build
scripts/test-compose.sh      # waits for services and probes each entry point
```

The first start creates the database, enables the capabilities listed in
`ENABLED_CAPABILITIES`, and installs the application packages under `apps/`.
Database passwords and the Kestra database are created once per data volume; after
changing them, recreate the volume with `docker compose down -v`.

## What is running

| Service | Local address |
| --- | --- |
| Workspace UI (App Shell) | <http://localhost:5173> |
| API gateway (APISIX) | <http://localhost:9080> |
| Hasura GraphQL API (the console is disabled) | <http://localhost:8080> |
| Keycloak admin | <http://localhost:8085/admin> |
| Kestra | <http://localhost:8082> |
| cap-process (direct; clients go through the gateway) | <http://localhost:8091> |
| PostgreSQL | `localhost:5432` |

The knowledge services (cap-ai, the personal library and the Assistant) are not
in this stack; start them with `make ai-up`
([installation](../operations/installation.md)).

The `operator` service reconciles `apps/` every 30 seconds. It installs new or
changed packages, deploys their workflows to Kestra, and repairs Hasura metadata
changed by hand. Follow it with:

```bash
docker compose logs -f operator
```

When the Operator stops, installed applications keep serving; runtime services do
not call it per request.

## Check the installation

```bash
scripts/test-compose.sh
docker compose ps
```

If a service is unhealthy, inspect its logs before changing configuration:

```bash
docker compose logs --tail=100 <service>
```

Next: [complete a first request](first-request.md).
