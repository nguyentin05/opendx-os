---
title: RAG reference
description: Knowledge contract, metadata jobs, source authorization, and answers.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-20
---

# RAG reference

| Page | Status | Covers |
| --- | --- | --- |
| [Knowledge contract](knowledge-contract.md) | Active | Knowledge v1 manifest fields, publication envelope, and private files |
| [Native metadata and jobs](native-metadata.md) | Active | cap-ai metadata ownership, ingestion jobs, delegations, and migration |
| [Model answers](answers.md) | Active | Optional answer gateway, citations, and hosted-processing limits |
| [API](api.md) | Active; partly implemented | Knowledge HTTP API and the routes cap-ai serves |
| [Authorization](authorization.md) | Active | Source authorization and identity model |

Package publication is implemented independently of indexing. Grants and serving
require their own acceptance gates; a dormant table does not certify a route.
