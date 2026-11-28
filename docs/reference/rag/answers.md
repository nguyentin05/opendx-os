---
title: Model answers
description: The optional answer gateway, citations, and hosted-processing rules.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-26
---

# Model answers and Assistant

Application answers use the requested fixed scope and current evidence.
When generation is unavailable, Workspace can still show authorized
retrieval results. The general Assistant can answer a general question
directly or search current knowledge for internal claims. It abstains when
internal evidence is missing and returns only citation IDs issued by its
retrieval tool in that turn.

The Assistant uses a bounded LangGraph loop, a recent message window, and
a compact conversation summary. Derived material carries source
provenance and is checked before use. Each conversation belongs to one
owner. A message can retain its historical snapshot, and opening its
citation always checks live access.

The runtime sends model data only through the audited Model Gateway.
Generation, embedding, vision and reranking each have an independent
slot and processing decision. Full-text search works with every slot
empty. [Gateway and API](api.md#model-gateway).
