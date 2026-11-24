---
title: Deployment-selected AI model infrastructure
description: LiteLLM Proxy becomes the only provider transport, and each deployment selects at most one model per purpose instead of managing a runtime registry.
diataxis: explanation
audience: [platform-developer, security-reviewer, operator]
status: active
owner: platform
last_reviewed: 2026-11-25
---

# ADR 0011: Deployment-selected AI model infrastructure

- **Status:** Accepted. Supersedes the decisions listed under
  [Supersession](#supersession). Every other decision in
  [ADR 0009](0009-native-rag-identity-and-source-boundaries.md) and
  [ADR 0010](0010-model-operations-without-monetary-admission.md) stands.
- **Date:** 2026-11-25
- **Decision owner:** repository/product owner.
- **Reviewed baseline:** `main` at `cee901aef0def26d6c0c78e959b0ebdcd8f29ad3`.

## Context

The baseline has two model-management mechanisms that both exist to change the
active model without a deployment.

1. A private Go service, `capabilities/internal/knowledge/modeladapter` served by
   `capabilities/cmd/cap-ai-model-adapter` and started only by the
   `compose.model-adapter.yaml` overlay, translates one reviewed
   `bounded-http-v1` envelope into one OpenAI-compatible upstream call. It owns
   provider protocol details in Go: request shaping, response validation,
   credential handling and per-provider quirks.
2. A runtime registry in `cap_ai` — `model_candidates`, `global_model_slots` and
   `model_slot_events`, created by `0005_model_registry.sql` and extended by
   `0011_model_administration.sql` — lets an operator import candidates and
   promote, roll back or disable a "slot" per capability. `cap_ai.model_profiles`
   holds the immutable reviewed profile, `cap_ai.processing_policy_profiles`
   binds policy revisions to exact profile revisions, and
   `capabilities/internal/knowledge/models` holds the `Gateway`, `Policy`,
   `OperationStore`, `Registry` and `RuntimeResolver` that consume all of it.

Both mechanisms carry coordination that nothing else needs: candidate imports,
slot revisions, activation events, digest-in-URL routing, a second saturated
review surface, and a Go translation layer that duplicates a maintained
open-source project. At the same time, changing a model is an operational event
that already requires a reviewed decision, a redeploy and, for embeddings, a
re-embedding of affected chunks.

The consumer inventory behind this decision is recorded in
[Consequences](#consequences): 218 matches in 99 files for the adapter and
package-layout query, 229 in 34 files for the registry query, 92 in 21 files for
the deployment-flag query, and 83 in 19 files for the processing/embedding-job
query.

## Decision

1. **LiteLLM Proxy is the only provider transport.** A single private Tier 1
   container, pinned by image digest from an official source, performs provider
   protocol translation, retries that the platform has disabled, and inference
   transport. The `bounded-http-v1` protocol, its serving package
   (`internal/knowledge/modeladapter`), its command
   (`cmd/cap-ai-model-adapter`), its Compose overlay and its TLS/credential
   plumbing are retired. LiteLLM is infrastructure, not an authorization
   service: Go keeps business authorization, model identity, payload and result
   bounds, grounding, citation validation, operation state and error mapping.
2. **A deployment selects at most one model per purpose.** The only purposes are
   `EMBEDDING` (satisfying the existing `EMBED` operation purpose) and `LLM`
   (satisfying `GENERATE`). A purpose may be absent. There are no candidates, no
   slots, no promotion, no rollback, no per-application models, no `VLM` and no
   `RERANKING` slot, and no automatic or configured fallback between models. A
   purpose whose route is absent, unqualified, suspended or failing stays
   unavailable and lexical full-text search remains available.
3. **Model identity is digest-derived.** Identity is a canonical digest over the
   reviewed deployment declaration: provider prefix, exact upstream model
   identifier and revision, approved endpoint or `api_base`, non-secret request
   parameters, and — for embeddings — vector dimension and indexing/query
   encoding semantics. The pinned proxy image digest and the proxy configuration
   digest are part of the reviewed declaration. Secrets are excluded, so rotating
   a provider key alone does not change identity and does not trigger
   re-embedding. A mutable upstream alias cannot by itself prove identity: an
   operator qualifies a pinned model or explicitly versions an upstream change,
   and an unreviewable route is rejected rather than described as safe.
   `cap_ai.model_profiles(id, revision)` and the functions that take a profile id
   and revision stay the storage key, with both derived deterministically from
   the digest so historical rows keep their meaning.
4. **Changing a model is a coordinated stop/drain deployment, never a runtime
   mutation.** The sequence is: validate the declaration and pinned digests;
   stop or drain the model-calling API and worker and prove no model operation is
   in flight; start the pinned proxy with the generated configuration; probe each
   configured purpose with bounded synthetic payloads; reconcile the immutable
   profile and approval metadata idempotently under a restricted deployment
   identity; start the API and worker so that all three consume one deployment
   snapshot. A snapshot mismatch denies model calls. A synthetic probe is
   technical verification only and grants no processing permission.
5. **Migration is additive by default.** Retired relations are removed only by a
   reviewed migration after their readers are gone; until then they are retained
   without writers. No migration deletes source documents, transcripts, original
   files, policy rows, approvals, durable operation rows or embedding
   generations, and no applied checksum is rewritten in place. ADR 0010's named
   rebuild exception stays exactly as narrow as ADR 0010 records it; this ADR
   adds no new clean-slate exception and no database reset.
6. **The effective processing modes are `DISABLED`, `LOCAL_ONLY` and
   `HOSTED_ALLOWED`.** They are derived from the existing authority, not a new
   source-trust or parser policy: `DISABLED` means the policy is not enabled, the
   installation-wide model-call switch is off, the purpose has no configured
   route, or the identity is suspended; `LOCAL_ONLY` means an enabled policy
   whose `execution` is `LOCAL_ONLY`; `HOSTED_ALLOWED` means an enabled
   `APPROVED_PROVIDER` policy with a current audited approval bound to the exact
   digest identity. Existing rows keep their meaning: a PERSONAL
   `LOCAL_ONLY` source-policy revision is not reinterpreted as blanket hosted
   permission, and an approval bound to a superseded profile revision does not
   authorize the new identity. Unknown state denies.
7. **Source, access and processing guarantees are retained.** ADR 0009 remains
   authoritative for identity, source authorization, personal ownership,
   retrieval filtering, citation reauthorization and processing approval. In
   particular: hosted processing of a `LOCAL_ONLY` source is refused even when
   the source is readable; a turn whose evidence cannot be processed returns
   authorized search-only results instead of switching models or answering from
   disallowed evidence; unapproved query embedding falls back to full-text
   search; missing, denied or unavailable models never widen access; and neither
   technical verification nor deployment grants hosted approval.
8. **Dispatch guarantees are retained.** ADR 0010 remains authoritative for the
   durable operation boundary: exactly one operation row per operation key,
   `MAY_HAVE_SENT`/`UNCERTAIN` is never resent, the final authority check
   precedes the network side effect, ambiguous outcomes surface as
   `MODEL_CALL_UNCERTAIN`, and no monetary admission returns. Because the pinned
   proxy is configured with retries, fallbacks, caches, message logging and
   callbacks disabled, one admitted Go operation still means at most one external
   dispatch.
9. **The CLI stays minimal, and suspension is reachable without a redeploy.**
   `dxos-model` keeps `validate`, `verify`, `status`, `policy approve` and
   `policy revoke`, and gains `policy suspend` with its explicitly reviewed
   counterpart `policy resume`. `import`, `activate`, `deactivate`, `slots`, the
   rollback path and redundant list/inspect views are retired with the registry.
   `policy suspend` must block subsequent dispatch for a purpose or for the whole
   installation through durable database authority with an audited actor,
   without depending on restarting the API, the worker or the proxy; it is the
   emergency control and must be verified as an equivalent of the existing
   runtime kill switch. Deployment-time `enabled: false` remains the planned
   switch. There is no public runtime model mutation API.
10. **Local inference is a first-class deployment route.** A local route is a
    trusted operator-controlled endpoint and model — an optional Ollama
    container with operator-provisioned weights, or an operator-managed
    vLLM-compatible server — reached through a qualified LiteLLM route. It is not
    an `execution: local` string. Endpoint ownership, routing and the absence of
    cloud forwarding are validated; untrusted overrides and redirects are
    refused. A local-only deployment must demonstrate that no source or query
    reaches hosted inference, and a mixed deployment must demonstrate that local
    operations stay on the local route.
11. **The proxy is private and database-free.** It publishes no host port and no
    management route, is reachable only from `cap-ai` and `cap-ai-worker` over
    the private network, requires `LITELLM_MASTER_KEY`, and runs in
    configuration-only mode: no `DATABASE_URL`, no
    `general_settings.store_model_in_db`, no Admin UI, no model, key, team or
    budget tables. Client-supplied `model`, `api_base`, header, endpoint,
    routing and credential overrides are rejected; unsupported required
    parameters fail instead of being dropped.

### Pinned proxy prerequisite

| Item | Value |
| --- | --- |
| Image | `ghcr.io/berriai/litellm:v1.110.3` |
| Multi-architecture index digest | `sha256:1050d2c9ca58399de9cd51df6eb503812af697717f54d2f08c1c30589c75e1f7` |
| `linux/amd64` manifest digest | `sha256:0dad43ff915ff3c621311258c0ae1a1caf3495a94f0e3630a8303ff8cb5d4c85` |
| Release | `v1.110.3`, published 2026-11-24; image `LITELLM_RELEASE_TAG=v1.110.3` label, built 2026-11-24T04:06Z |
| Base image | Chainguard `wolfi-base`; entrypoint `docker/prod_entrypoint.sh`, `--port 4000` |
| License | MIT outside `enterprise/`; the image also contains `enterprise/`, which is commercially licensed and must stay unlicensed and unused |

The tag is pinned by digest, not by tag name. Configuration-only operation was
verified against the release source and the published documentation: `store_model_in_db`
defaults to `False`, and from v1.103.0 a key declared in the configuration file is
owned by the file while the database owns only undeclared keys, so a deployment
with no database and no `store_model_in_db` treats the file as the sole source of
truth.

Route constraints that the deployment must set and the tests must assert:
`router_settings.num_retries: 0` and `num_retries_per_request: 0` (the router
default is 2), no `fallbacks`, `context_window_fallbacks` or
`content_policy_fallbacks`, `litellm_settings.turn_off_message_logging: true`,
`general_settings.disable_spend_logs: true`, no callbacks or telemetry, no
prompt/response cache, no virtual keys, teams or budgets. The provider prefixes
`openrouter/`, `ollama/` and `hosted_vllm/` resolve at this release, and Ollama
embeddings use `/api/embed`.

Two prerequisites remain unresolved and must be closed before a real route is
declared qualified; neither may be asserted as done:

- **Open advisory.** GHSA-w5h5-479w-p5hx (high, JWT/SSO email fallback) affects
  `< 1.111.0` and no stable `1.111.0` release existed on 2026-11-25, so the pinned
  version is exposed if and only if JWT/OIDC administration sign-in is
  configured. This deployment configures master-key authentication only and no
  JWT/OIDC sign-in, and must re-check the advisory at every reconciliation.
- **No published image signature.** No cosign signature or OCI referrer exists
  for the pinned index digest, so provenance rests on the registry digest plus
  the image's own release label. Signature verification stays open.

### Route qualification status

No hosted or local model is qualified by this decision, and this ADR deliberately
names no model identifier, revision or vector dimension. A real route needs an
operator-provided provider account and credential, the exact upstream model
identifier and revision, the measured embedding dimension, a retention and
training-use review, and spend approval. Until that happens, deterministic tests
use synthetic fixture routes served by a local fake, and a deployment with no
qualified route stays on lexical search. Recording a route as qualified without
that evidence would be a false claim, so real-route qualification remains a
blocked deployment gate.

## Supersession

This ADR supersedes only the following, and it does not edit the historical
records.

From [ADR 0009](0009-native-rag-identity-and-source-boundaries.md):

- The implementation-delta row for `cap-ai/0005_model_registry.sql` and the
  runtime resolver — "active slots identify profiles … only Platform Admin
  manages installation-wide lifecycle". Slot-based activation and its immutable
  binding are replaced by deployment-selected digest identity (decisions 2, 3
  and 4). The retained requirements in that row — immutable bindings and
  digests, no approval inherited from activation — survive as digest identity
  and explicit approval.
- The implementation-delta row for
  `capabilities/internal/knowledge/answers.go` combined with the ADR 0010
  consequence that shipping a local serving adapter is out of scope, which
  together made qualified local inference unreachable: a deployment could refuse
  local serving instead of selecting it. Decision 10 makes qualified local
  inference a selectable route for both purposes, and decision 7 keeps the
  guarantee that those clauses protected — a `LOCAL_ONLY` source is never sent
  to hosted inference — as an explicit route check. ADR 0009 does not otherwise
  exclude local inference, and no other clause is read as doing so.
- The requirement that hosted transport pass through the bounded adapter
  protocol, and any provider-translation responsibility it implies for Go
  (decision 1).

From [ADR 0010](0010-model-operations-without-monetary-admission.md):

- The "slot authority" element in its consequences list, which named slot
  authority among the unchanged controls. Slot authority is replaced by
  deployment selection, digest identity and suspension. Profile enablement,
  processing approval, resource bounds, the kill switch, the durable operation
  matrix, `MODEL_CALL_UNCERTAIN`, the v2-declaration shape of immutable identity
  and limits, and the named-rebuild exception all stand.
- The `bounded-http-v1` adapter key as a required part of a declaration, as
  recorded in its decision 3. The successor declaration schema is settled by the
  migration task; it must keep immutable identity, purpose, execution class,
  resource limits and admin/runtime separation, and it must still reject retired
  schemas.

Retained without change: ADR 0007 identity and DB-authoritative roles; ADR 0009
source access predicates, owner-only personal sources, audited hosted-PERSONAL
approval, per-source AGENT authorization, owner-only historical transcripts with
fresh reauthorization, least-privilege identities and the prohibition on
superuser/`BYPASSRLS` and direct model access to originals, the database or
secrets; ADR 0010 durable operation state and its public uncertainty surface.

## Consequences

Retired or replaced objects, with the baseline readers and writers that the
inventory found. "Retire" means the object loses its last reader and is removed
by a reviewed migration; nothing here is dropped by this decision.

| Object | Baseline writers | Baseline readers | Decision |
| --- | --- | --- | --- |
| `cap_ai.model_candidates` | `registry_import_candidates` executed by `dxos_ai_model_catalog`; `registry_guard_history` trigger | `registry_resolve_slot`, `registry_embedding_lease_profile`, `model_admin_list`, `model_admin_change_slot`, `RuntimeResolver.validDeclaration`, integration tests | Retire. No reader survives deployment-selected identity |
| `cap_ai.global_model_slots` | `model_admin_change_slot`; fixture updates in tests | `registry_resolve_slot` via `Registry.Resolve`/`RuntimeResolver` via `Store.modelBinding`; `agent_model_policy` and `agent_query_model_policy` slot pins; `dxos-model slots`; tests | Retire. `LLMSlotRevision`/`EmbeddingSlotRevision` pins are replaced by the digest identity |
| `cap_ai.model_slot_events` | `model_admin_change_slot`; `registry_guard_history` trigger | `model_admin_slots`, `model_admin_list`, registry integration tests | Retire with the administration lifecycle |
| `cap_ai.registry_resolve_slot`, `registry_import_candidates`, `registry_guard_history`, `registry_guard_slot`, `model_admin_session`, `model_admin_actor`, `model_admin_list`, `model_admin_slots`, `model_admin_change_slot` | Deployment identity and Platform Admin sessions | `internal/knowledge/models/{registry,slots}.go`, `modelprovisioning/admin.go`, `cmd/dxos-model` | Retire with their grants to `dxos_ai_gateway`, `dxos_ai_model_catalog` and `dxos_ai_model_admin` |
| `cap_ai.registry_embedding_lease_profile` | — | Worker lease resolution | Rewrite to resolve the deployment identity from the lease, keeping lease fencing |
| `cap_ai.agent_model_policy`, `cap_ai.agent_query_model_policy` | — | `models/authority.go`, AGENT and query approval paths | Retain, replacing slot pins with the digest identity |
| `cap_ai.model_profiles`, `cap_ai.processing_policies`, `cap_ai.processing_policy_profiles`, `cap_ai.agent_processing_approvals` and events, `cap_ai.embedding_generations`, `cap_ai.chunk_embeddings`, `cap_ai.model_operations` | Deployment and approval identities | Gateway, `Policy`, `OperationStore`, worker, retrieval | Retain; identity derivation changes, meaning does not |
| `queue_embeddings`, `claim_embedding`, `embedding_work`, `save_embedding`, `finish_embedding`, `fail_embedding`, `search_vectors`, `search_personal_vectors`, `model_policy`, `worker_model_policy`, `personal_processing_approval`, `claim_model_operation`, `begin_model_operation`, `finish_model_operation` | Worker, API and Gateway | Worker, retrieval, answers, authority | Retain. The existing re-embedding job path is reused, not replaced |
| `internal/knowledge/modeladapter` (8 files), `cmd/cap-ai-model-adapter`, `compose.model-adapter.yaml`, `scripts/check_model_adapter_env.py`, `scripts/integration/test_model_adapter_deployment.py`, `RAG_MODEL_ADAPTER_*` and `MODEL_ADAPTER_*` settings | — | Gateway transport, `modelprovisioning`, deployment tests, the model guide and the Tier descriptions | Retire once the replacement is verified |
| `internal/knowledge/models/{slots,registry,catalog}.go` and the `import`/`activate`/`deactivate`/`slots`/`list`/`inspect` CLI commands | Operator and `dxos-model` | Runtime resolution and provisioning tests | Retire; `validate`, `verify`, `status`, `policy approve`, `policy revoke`, `policy suspend` remain |

Other documented consequences:

- A model change becomes a redeploy. Operators gain a small, auditable
  declaration and lose runtime switching they did not need; the platform loses a
  Go transport layer it should not maintain.
- An installation that had qualified a hosted route through the adapter must
  requalify it through the proxy: the identity, approval binding and digest all
  change, and the stricter pre-migration behaviour applies until a reviewed
  mapping exists.
- The proxy is the only new external dependency. It is pinned by digest, private,
  database-free and configured so that its own retry, fallback, cache and
  logging features cannot weaken the Go dispatch invariant. A future proxy
  weakness is a platform risk, so the advisory check above is part of routine
  reconciliation rather than a one-time step.
- Fresh-bootstrap evidence for the migration task: a database bootstrapped with
  the model flags disabled contains no model, registry, operation or embedding
  relation and no gateway, catalog or model-admin role, while a bootstrap with
  all flags enabled contains all of them. The additive migration must therefore
  keep the lexical-search deployment valid without any model table.

## Consequences for documentation

- [The model guide](../guides/knowledge/models.md), [Tier
  boundaries](../architecture/tiers.md) and the
  [control plane](../architecture/control-plane.md) still describe the bounded
  adapter, the registry and the slot administration. They must describe the
  pinned proxy, the deployment declaration and the minimal CLI before this
  decision is implemented, and they must not present the old lifecycle as
  current.
- [Authorization](../reference/rag/authorization.md), the
  [API contract](../reference/rag/api.md) and
  [answers](../reference/rag/answers.md) must replace slot pins and adapter
  references with digest identity while keeping every retained predicate.
- [Installation](../operations/installation.md) and
  [verification](../contribute/verification.md) must record the coordinated
  stop/drain deployment, the pinned digest, the synthetic probe evidence and the
  emergency suspension check as the evidence standard for a model change.
