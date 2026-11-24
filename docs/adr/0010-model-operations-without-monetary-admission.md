---
title: Model operations without monetary admission
description: Durable per-operation dispatch state replaces budget reservation and settlement, and the optional model migrations are rebased for a coordinated baseline.
diataxis: explanation
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# ADR 0010: Model operations without monetary admission

- **Status:** Accepted. Supersedes parts of
  [ADR 0009](0009-native-rag-identity-and-source-boundaries.md) listed under
  [Supersession](#supersession-of-adr-0009); all other ADR 0009 decisions stand.
  Slot authority and the `bounded-http-v1` declaration key are superseded by
  [ADR 0011](0011-deployment-selected-litellm-models.md); the durable operation
  decisions here stand.
- **Date:** 2026-11-23
- **Decision owner:** repository/product owner.

## Context

ADR 0009 D10 chose pragmatic MVP accounting for cross-app Agent model calls: a
genuine `Ready` app configured by `AI_AGENT_BILLING_APP_ID` carried an
admin-provisioned APP budget account next to the INSTALLATION and USER accounts.
Admission then depended on `cap_ai.budget_accounts`, `model_reservations`,
`reservation_allocations`, `usage_ledger` and priced profiles
(`pricing_revision`, `input_unit_price`, `output_unit_price`), and an outcome was
described as a settlement.

That model has three problems. The platform does not bill model usage, so a
monetary ceiling duplicates a provider contract instead of protecting the
platform. A reservation is not the fact that matters for safety; the facts that
matter are whether an operation may already have been sent and how many tokens it
used. And pricing revision identity leaked into profile declarations, catalog
documents and fixtures that had no monetary responsibility.

The reviewed design proposal of 2026-11-20 replaced monetary admission with
durable operation state. Tasks 1–3 of the 2026-11-22 clean-slate plan implemented
it. This decision records the result and the rollout rule that follows from it.

## Decision

1. **One durable operation row is the admission and idempotency boundary.** The
   gateway records exactly one `cap_ai.model_operations` row per existing
   operation key, through three narrow `SECURITY DEFINER` interfaces owned by
   `dxos_ai_owner` and executable only by `dxos_ai_gateway`:
   `claim_model_operation`, `begin_model_operation` and `finish_model_operation`.
   States are `CLAIMED`, `MAY_HAVE_SENT`, `SUCCEEDED`, `FAILED_UNSENT` and
   `UNCERTAIN`, with the transition matrix, state-specific data rules and
   finalization replay defined in `cap_ai/0004_models_vectors.sql`. The stored
   dispatch token is private to the owner boundary. Direct table access is
   revoked from every runtime role.
2. **Monetary objects are removed.** `0004` no longer creates `budget_accounts`,
   `model_reservations`, `reservation_allocations`, `usage_ledger` or the old
   `model_calls`, and no longer defines `reserve_model`, `settle_model`,
   `begin_model_call`, `finish_model_call` or `pending_model_charges`. Prices and
   `pricing_revision` are gone from profiles, limits, declarations and the
   runtime. Token counters are observational: they are recorded only for
   `SUCCEEDED` and are NULL for `UNCERTAIN` and `FAILED_UNSENT`.
3. **The strict catalog is schema version 2.** Version 1 and any v2 document
   carrying `input_unit_price`, `output_unit_price` or `pricing_revision` are
   rejected with an explicit unsupported-version or unknown-field error.
   Immutable profile identity, purpose, execution class, resource limits and
   admin/runtime separation are unchanged.
4. **The Agent consumer setting is `AI_AGENT_PROCESSING_APP_ID`.** The value must
   still be a genuine registered app that is currently `Ready`; it scopes the
   Agent's PERSONAL query review and attributes the operation. It is not a budget
   account and confers no source authority. The retired `AI_AGENT_BILLING_APP_ID`
   name is not accepted as a fallback.
5. **Public uncertainty is `MODEL_CALL_UNCERTAIN`.** The optional answer path
   returns HTTP 200 `SEARCH_ONLY` with that reason, a null answer and no
   citations or usage; `require_generation: true` returns 503
   `MODEL_CALL_UNCERTAIN`. `MODEL_UNAVAILABLE` remains a distinct 503 for a
   missing or disabled model binding. `MODEL_OPERATION_CONFLICT` and the other
   binding failures stay internal; only the public surface collapses them into
   the uncertainty code. No client may resubmit on either outcome.
6. **The ingestion surface publishes the same uncertainty code.** Migration
   `0004` stores `error_code='MODEL_CALL_UNCERTAIN'` on an embedding ingestion
   job whose model call may have been sent. That code is not private: a reader
   who currently holds source READ sees it through `cap_ai.ingestion_metadata`
   and through `GET /api/cap/ai/ingestions/{id}`, which serializes the function
   result without remapping it. `MODEL_CALL_UNCERTAIN` therefore names the one
   public uncertainty outcome on both the answer and ingestion surfaces, and it
   is still not a settlement state or a monetary fact. The ingestion job state
   stays `FAILED`, which is distinct from the durable operation state
   `UNCERTAIN`: the job records that the embedding build did not complete, the
   operation records that a call may have been sent and must never be resent.
   Any other reader keeps the existing concealed outcome: no job, no state and
   no error code.
7. **Rollout is a named rebuild, not an upgrade.** The rebased `0004` and `0005`
   are a one-time clean-slate exception described below. Every affected owner
   provisions or rebuilds a named database on the approved revision, bootstraps
   it, loads valid v2 declarations and model approvals, and points matching
   binaries at it. A checksum conflict blocks both migration runners before any
   domain SQL is applied, regardless of optional flags.
8. **Legacy application data is not restored wholesale over the rebased schema.**
   A full dump/restore of `cap_ai` would reintroduce the removed monetary
   relations and rows, and would carry operation rows that no longer match the
   schema. After the rebuild, re-derive the model capability state: re-publish
   application packages, re-register knowledge sources, re-import catalog v2
   candidates, re-approve processing policies and re-issue document grants. Data
   in other capability schemas may be preserved only under the owner's own
   decision and migration review for those schemas; it is never a reason to
   restore `cap_ai` wholesale.

## Supersession of ADR 0009

[ADR 0009](0009-native-rag-identity-and-source-boundaries.md) remains the
identity, source and processing-policy decision. This ADR supersedes only:

- **D10** (cross-app Agent model calls priced against an APP budget account
  configured by `AI_AGENT_BILLING_APP_ID`). It is replaced by decisions 1, 4 and
  5 here: durable operation state, the processing app setting, and the public
  uncertainty code. The requirement that the consumer be a genuine `Ready` app
  and that every actual source be independently authorized is retained.
- The **reservation/settlement requirements** in ADR 0009's contract-amendment
  table ("Model operation/accounting producers", "Keep Gateway
  reservations/settlement") and in the canonical authorization, API and answer
  contracts. Atomic INSTALLATION/APP/USER reservation, idempotent settlement,
  retained funds and usage-ledger invariants no longer apply. The durable
  operation transition matrix and the recorded token observation replace them.
- The **applied-migration rule** that said migrations after `0005` must be
  additive and that applied `0001`/`0004`/`0005` checksums must never change.
  ADR 0009's rule stands for every other migration and every future change. The
  exception is narrow and one-time: `0004` and `0005` were rebased in place for
  the coordinated development baseline, the checksum preflight now detects an old
  applied checksum and refuses to proceed, and the only accepted remedy is the
  named rebuild in decision 7. No ledger row is edited, no checksum is bypassed
  and no existing installation is upgraded in place.

ADR 0009's security decisions are kept unchanged: ADR 0007 identity and
DB-authoritative roles, source access predicates, owner-only personal sources,
audited hosted-PERSONAL processing approval, per-source AGENT authorization,
owner-only historical transcripts with fresh reauthorization before reuse,
least-privilege service and database identities, and the prohibition on
superuser/BYPASSRLS and direct model access.

The operational app is attribution and review scope only. It never grants access
or substitutes its classification for any source's classification, and it is
never selected from request JSON.

## Consequences

- The platform no longer enforces a monetary ceiling. Spend limits belong to the
  provider contract. Resource bounds (input/output tokens, bytes, timeout),
  profile enablement, slot authority, processing approval and the kill switch are
  unchanged and remain the platform's controls.
- The observable safety property changes from "spend is settled at most once" to
  "an operation that may have been sent is never sent again". Token counts are
  evidence for operators, not a billing entry.
- Losing a committed begin, a finalization acknowledgment or a provider response
  leaves `MAY_HAVE_SENT` or `UNCERTAIN`. Neither state permits a resend.
- Operators must read `cap_ai.model_operations` for operational accounting and
  escalation. There is no reconciler and no invoice adapter in this baseline.
- A fake LOCAL adapter exercises the shared dispatch boundary. Shipping a LOCAL
  serving adapter remains out of scope, so a passing fake LOCAL case is not
  evidence that local model serving exists.
- A deployment holding an old applied model checksum cannot start the new
  binaries against its old database. Its owner must rebuild a named database.

## Consequences for documentation

- [Authorization](../reference/rag/authorization.md#model-operation-attribution-and-durable-state),
  [answers](../reference/rag/answers.md) and the
  [API contract](../reference/rag/api.md#model-gateway) describe durable
  operation state and token observation instead of budgets and settlement.
- [The model guide](../guides/knowledge/models.md) documents the v2 catalog, the
  processing app setting, the public uncertainty code on the ingestion surface
  and the qualification gate.
- [Installation](../operations/installation.md) and
  [verification](../contribute/verification.md) document the named rebuild and
  the evidence expected for it.
