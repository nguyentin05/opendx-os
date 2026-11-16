---
title: Prerequisites
description: Docker, Compose, and the tools required before starting the tutorial.
diataxis: tutorial
audience: [evaluator, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Prerequisites

You need a workstation with:

- Docker Engine 24 or newer and Docker Compose v2;
- at least 8 GB of RAM available to Docker (16 GB recommended);
- ports `5173`, `8080`, `8085`, `9080`, and `5432` free, or values overridden in
  `.env`;
- `bash`, `curl`, and `git`.

Optional tools:

- Go 1.24+ and Node.js 22+ only if you build the Operator or UI outside Compose;
- Python 3.12+ with PyYAML for repository checks and demo seeding;
- `postgresql-client` for direct database inspection.

The stack binds published ports to `127.0.0.1` by default. Do not expose it to an
untrusted network. Demo credentials and seeded data are for local evaluation only.

Next: [run the local stack](local-stack.md).
