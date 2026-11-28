---
title: Python Intelligence runtime
description: AI service topology, document parsing risk, model policy, and cutover authority.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# ADR 0011: Python Intelligence runtime

Accepted on 2026-11-26.

`cap-ai` runs FastAPI, Haystack retrieval and one LangGraph Assistant.
`cap-ai-worker` runs Celery and in-process Docling. Both share a locked
Python image. PostgreSQL provides full-text search, pgvector and PGMQ.
Alembic runs once per deployment and owns only `cap_ai`. Core and the Go
Operator retain identity, role, file and publication authority.

Docling parses Markdown and PDF in the worker. ClamAV admission, file
verification, offline model assets, bounded page counts and Celery time
limits apply to every ingestion. English OCR uses Tesseract. Parser
sandboxing is deferred for this increment. A parser compromise can expose
the worker's DB privileges, broker login and model master key. Deploy this
increment only where that document trust and deployment risk are acceptable.
The previous separate parser and its sandbox qualification no longer apply.

Workspace AI Management replaces the deployment catalog CLI and external
adapter. One active immutable model revision is allowed in each of LLM,
EMBEDDING, VLM and RERANKER. Provider credentials are encrypted with a
deployment-held Fernet key. The Model Gateway is the only SDK call site.
Installation endpoint allowlists and exact revision approvals constrain
hosted processing. Explicit denial takes precedence over approval. Document
READ and permission to send data to a model are separate decisions.

The operation boundary in ADR 0010 remains. A durable operation key
records that a request may have been sent before network egress. A repeated
or uncertain key never dispatches again. Confirmed outcomes may record
observed tokens. The Python registry and SQL interfaces replace the Go
catalog, stored procedures and profile shapes cited in ADR 0010.

Alembic rejects legacy AI schema or ledger entries. A fresh deployment or
an approved AI-only rebuild is required. Original private files and Core
migration history are outside the rebuild scope. Application packages and
private originals can be registered again under current authority.

This decision supersedes parser isolation, Go runtime topology, model
provisioning, processing approval and migration mechanics in
[ADR 0009](0009-native-rag-identity-and-source-boundaries.md) and
[ADR 0010](0010-model-operations-without-monetary-admission.md).
Their source identity, current READ, owner-only history, and durable
uncertainty requirements still apply.
