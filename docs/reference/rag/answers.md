---
title: Model answers
description: The optional answer gateway, citations, and hosted-processing rules.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Optional model Gateway and answers

`POST /api/cap/ai/answers` accepts
`{scope,query,max_citations?,require_generation?}` and returns
`{status,answer,results,citations,reason?,model_profile?,usage?}`. Status is
ANSWERED, SEARCH_ONLY or NO_EVIDENCE; `answer` is null without supported
generation. Profile references expose only ID/revision. A client cannot choose
classification, provider, credentials, tools or endpoints. Every supplied context
source is freshly checked after generation; any revocation discards the whole
answer and retrieved titles/citations with `AUTHORIZATION_CHANGED`.

Hosted processing requires separate reviewed app-query and per-source immutable
policy/profile approval. Personal content/questions always deny hosted processing,
including fallback. Gateway reservations charge installation/app/user accounts
atomically; possible billed timeouts retain UNCERTAIN funds and never resend the
same operation automatically. Model unavailability preserves lexical search.
Semantic generations and dimensions are independent from lexical readiness;
current source/role checks apply before exact vector ranking and final response.

The optional additive model migration and deployment adapter contract are in
[model retrieval and answers](../../guides/knowledge/models.md). No profiles,
provider/classification approvals or local models are enabled by bootstrap.
