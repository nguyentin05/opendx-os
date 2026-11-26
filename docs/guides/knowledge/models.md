---
title: Model answers
description: Configure the optional model gateway for knowledge answers.
diataxis: how-to
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# AI installation and model administration

## Compose installation

Run `scripts/init-env.sh`, then `make ai-up` on a fresh named project.
The helper defaults to `opendx-os-ai` and creates restricted database,
broker and service-identity credentials in `.env.rag`. Set
`COMPOSE_PROJECT_NAME` to use another isolated project. Set
`AI_ENV_FILE=.env` to keep those credentials in `.env` instead; the helper
fills its empty placeholders. Switching files after the database volume
exists needs the same credential values or a fresh volume, because PostgreSQL
applies role passwords only on first initialization. The project needs
free ports for the base stack. The overlay builds PostgreSQL 17 with
pgvector 0.8.7 and PGMQ 1.13.2 and shares one AI image across the API,
Celery worker and one-shot Alembic service.

For a deployment with your own identities, set the credentials in a
private environment file and combine `compose.yaml` and `compose.rag.yaml`.
The API and worker use separate application database logins and the broker
has its own restricted login. Only `ai-migrate` receives the deployment
database identity. Configure distinct, verified Keycloak subjects for the
worker, File Service and dispatcher. Leave static bearer tokens empty to
use renewing client credentials.

Existing Go AI installations require a deliberate cutover. [Installation and
rebuild](../../operations/installation.md#rebuilding-an-existing-model-installation).

## Configure a model in Workspace

Sign in as a current Platform Admin. Open **Quản trị AI**, add an approved
provider, register a model and run its synthetic probe. Select the
revision in one of the four slots. Use the enabled control to stop dispatch
through a provider immediately or clear a single slot to disable its purpose.

| Slot | Purpose | Supported protocol |
| --- | --- | --- |
| LLM | Assistant and evidence-grounded application answers | OpenAI-compatible chat completions |
| EMBEDDING | Compatible vectors for indexing and search | OpenAI-compatible embeddings |
| VLM | Optional image descriptions for indexing | OpenAI-compatible chat with image inputs |
| RERANKER | Optional candidate reranking | Cohere v1 or Infinity rerank |

Register each embedding model with its actual output dimension. Switching
revision isolates old vectors. Existing content stays searchable by text. New
uploads and package versions use the current embedding slot. Full-text search is available without a model.
The Assistant requires an active LLM. No public-cloud route is selected
automatically.

Before registering a provider, set its exact base URL in
`AI_HOSTED_MODEL_ENDPOINTS` (a JSON array of HTTPS URLs) or
`AI_LOCAL_MODEL_ENDPOINTS` (a JSON object mapping approved HTTP(S) URLs to
private IP CIDRs). Use an OpenAI-compatible API base such as
`https://api.openai.com/v1`. Hosted destinations require public routable
addresses. Local destinations require an explicitly approved private
network. The gateway pins a validated address for each operation, verifies
HTTPS host identity and rejects redirects and metadata ranges. Replacing
a destination means registering a new provider and model revision.

Hosted processing starts denied. Approve the applicable data class and
exact model revision for each purpose. Query approval applies to prompts,
Personal approval to private knowledge, and Application approval to
package knowledge. An Application policy can apply globally or to a named
app. An applicable deny takes precedence. Every class in mixed context
must be approved before the gateway sends it.

## Credentials and model-operation logs

Set one Fernet key in `AI_MODEL_MASTER_KEY` for the API and worker. The
deployment helper generates one. Store it outside the database and back
it up separately from database backups. The UI clears submitted secrets
and reports only whether a credential exists. To rotate the master key,
disable the providers, quiesce the AI services, securely retain the old
backup and key, update the deployment key, and replace each provider
credential through AI Management before reenabling dispatch.

The operations view shows safe outcomes and confirmed token observations.
A durable operation whose network result is uncertain stays ineligible
for replay. See [Model Gateway](../../reference/rag/api.md#model-gateway).

## Verification and parser limits

Run `make test-ai` with Docker, uv and English Tesseract installed. It
provisions a disposable PostgreSQL instance and exercises API,
authorization, broker recovery, real PDF and Markdown ingestion, OCR,
retrieval and deterministic model paths. Verify the deployed provider
separately with a synthetic probe.

Docling uses pinned offline layout assets and Tesseract in the worker.
The worker processes one task at a time and limits each input to 20 MiB,
each PDF to 100 pages, and each document to 512 chunks. ClamAV admission
and format, size and checksum checks apply before parsing. A parser
compromise can expose worker privileges. See [ADR 0011](../../adr/0011-python-intelligence-runtime.md).
