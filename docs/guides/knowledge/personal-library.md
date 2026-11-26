---
title: Personal library
description: Enable owner-only document ingestion and retrieval.
diataxis: how-to
audience: [app-author, operator]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# Personal Library

Start the [AI Compose installation](models.md#compose-installation).
Grant the signed-in user a current role in the Ready application whose
knowledge declaration enables Personal Library. The Workspace sidebar
opens the library independently of the current application view.

## Upload and search

1. Choose a Markdown or PDF file up to 20 MiB. The File Service stores an
   immutable owner-only original and quarantines it for scanning.
2. When the scan is clean, Workspace registers the document. The worker
   extracts text and provenance through Docling, including English OCR for
   scanned PDFs. Workspace shows pending, indexing, ready or failed status.
3. Search the library, open a citation for a protected preview, or download
   the original. Full-text search is available without model configuration.
4. Delete a library entry to remove it from new retrieval and citations.
   The File Service retains the protected original.

Only the current owner can read personal knowledge. Holding Platform
Admin does not change this. A revoked library role or disabled account
removes new content access even if the document was already indexed.
Hosted processing is independent of read permission and requires its own
approval. [Authorization](../../reference/rag/authorization.md).

## Verification and limits

Run `make test-ai` for PostgreSQL role, ingestion and citation checks.
The Core File Service path remains separately covered by
`scripts/test-rag.sh source` on a configured installation. Supported
formats, OCR limits and deployment risk are in the [AI guide](models.md).
