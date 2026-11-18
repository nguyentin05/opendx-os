---
title: Model answers
description: The optional answer gateway, citations, and hosted-processing rules.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-18
---

# Optional model Gateway and answers

The selected-scope answers API and optional cross-app agent share the model
Gateway. Migrations 0006–0007 enable owner conversations and DB-resolved
scopes. Migrations 0008–0010 enable reviewed hosted PERSONAL processing,
PERSONAL hybrid retrieval and AGENT model authority when configured. See
[owner conversation routes](../api/capability-routes.md#owner-conversations)
and [installation prerequisites](../../guides/knowledge/models.md#compose-installation).

`POST /api/cap/ai/answers` accepts
`{scope,query,max_citations?,require_generation?}` and returns
`{status,answer,results,citations,reason?,model_profile?,usage?}`. Status is
ANSWERED, SEARCH_ONLY or NO_EVIDENCE; `answer` is null without supported
generation. Profile references expose only ID/revision. A client cannot choose
classification, provider, credentials, tools or endpoints. Every supplied context
source and its processing policy are freshly checked immediately before egress
and immediately before the non-streaming response, including uncited context.
A changed source READ snapshot, including a replacement run of the same version,
discards the new answer and retrieved titles/citations with `AUTHORIZATION_CHANGED`. Missing DB/policy/model authority denies the affected
operation; no-evidence returns NO_EVIDENCE without a generation call.

Hosted processing requires independent current query and per-source approval.
The [canonical processing policy](authorization.md#model-processing-policy)
permits PERSONAL EMBED/GENERATE only through audited Platform Admin approval
binding purpose, provider/profile revision, enabled state, source/query
classification and retention/training-use review. No active slot, budget or
per-user opt-in supplies that approval. Unapproved PERSONAL source/query egress
denies; no fallback may route it to another provider. Deployments without a current matching review retain authorized lexical search.

For the new `AGENT` chat path, source selection uses all current DB-authoritative
memberships server-side and includes only the caller's PRIVATE personal sources.
Each actual source app and every supplied evidence item is independently checked;
a client role list, JWT business-role claim or model-suggested app cannot widen
access. Existing Hasura/capability selected-role APIs retain ADR 0007 semantics.

Gateway reservations charge installation/app/user accounts atomically under the
[billing policy](authorization.md#model-operation-billing-attribution).
PERSONAL-only calls retain library-app attribution; cross-app AGENT operations
use the server-configured genuine Ready `AI_AGENT_BILLING_APP_ID` account.
That account grants no source access or egress permission. Possible billed
timeouts retain UNCERTAIN funds and never resend the same operation automatically.
Model unavailability preserves authorized FTS; optional generation returns
SEARCH_ONLY, while required generation with evidence distinguishes 403
POLICY_BLOCKED, 429 QUOTA_EXCEEDED and 503 MODEL_UNAVAILABLE. Semantic generations
and dimensions remain independent from lexical readiness. APP and PERSONAL
bounded hybrid retrieval must retain current source/role/policy checks before
ranking and final response.

Persisted chat message/answer snapshots are owner-only and survive ordinary
source/role revoke. Linked originals and citation previews require current source
READ. A stored answer, excerpt or summary cannot become new evidence without
fresh source and processing authorization; revoked source-derived material is
excluded from subsequent generation even though its historical snapshot remains
readable. See [transcript policy](authorization.md#agent-source-selection-and-historical-transcripts).

The optional additive model migration and deployment adapter contract are in
[model retrieval and answers](../../guides/knowledge/models.md). No profiles,
provider/classification approvals or local models are enabled by bootstrap.
