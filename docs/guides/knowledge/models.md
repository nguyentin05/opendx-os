---
title: Model answers
description: Configure the optional model gateway for knowledge answers.
diataxis: how-to
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-18
---

# Optional model retrieval and answers

The model boundary is optional. Personal libraries and package lexical search
continue without pgvector, profiles, credentials or model availability. Personal
content and questions may use hosted EMBED/GENERATE only under the amended
[processing policy](../../reference/rag/authorization.md#model-processing-policy):
audited Platform Admin approval is separate from model-slot activation and
requires purpose, provider/profile revision, enabled state, independent
source/query classification and retention/training-use review. There is no
per-user hosted opt-in. Unapproved PERSONAL egress denies. Hosted RERANK,
OCR/VLM and local-model serving are outside this increment. App selection is
not a permission grant. Legacy `/rag` remains unavailable.

Migrations 0008–0010 add audited PERSONAL processing, PERSONAL semantic
retrieval and the mixed-source AGENT model boundary. Follow the
[ADR 0009 migration/consumer delta](../../adr/0009-native-rag-identity-and-source-boundaries.md#contract-amendment-and-implementation-delta-2026-11-18)
before enabling the new behavior; provisioning an active slot alone cannot
implement or authorize it.

## Compose installation

The base `compose.yaml` runs the regular stack. Add `compose.rag.yaml` for the
knowledge API, private File Service delegation, package dispatcher, parser and
worker. Add `compose.models.yaml` only for model schema/runtime support. Use
the same file order and project name throughout an installation.

1. On a fresh deployment, include `cap-ai` alongside the existing
   `ENABLED_CAPABILITIES`. The RAG overlay supplies that list by default when
   the variable is absent. If using `.env.example`, edit its explicit list.
   Provide separate URL-safe secrets in `AI_DATABASE_PASSWORD`,
   `AI_WORKER_DATABASE_PASSWORD` and `AI_DISPATCHER_DATABASE_PASSWORD`.
   The models overlay also requires `AI_GATEWAY_DATABASE_PASSWORD` and selects
   PostgreSQL 17 with pgvector. Both PG17 images use the same data directory.
   To upgrade retained data, use the checksummed migration procedure below,
   provision the missing restricted role passwords through deployment admin
   operations, and back up the volume before changing its image. Init scripts
   run only on an empty volume.
2. Supply distinct OIDC UUIDs and renewable audience-matched access tokens
   for the worker, File Service and dispatcher in the variables in
   `.env.example`. Register the worker subject as an ACTIVE Core identity.
   Configure the same issuer, JWKS and audience on the services. Use a separate
   random `PARSER_SERVICE_TOKEN` of at least 32 characters. Renew tokens and
   recreate their consumers before expiry. API, worker and dispatcher each
   connect under the corresponding restricted DB login. Inside cap-ai the
   private delegation/reconcile handlers use the worker and dispatcher
   connections. The model Gateway has a separate restricted login.
3. Set `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` and
   `RAG_PARSER_QUALIFIED` only after verifying the source and parser
   prerequisites in the deployment. Set `RAG_METADATA_ENABLED=true` and
   enable `RAG_PACKAGE_ENABLED`, `RAG_PERSONAL_ENABLED`, or both.
   Enable `RAG_PRIVATE_UPLOAD_ENABLED` for private uploads. The Operator scans
   declared package sources and writes approved bytes into a private named
   volume. cap-ai reads those bytes. The parser and worker share a separate
   private socket volume and UID 10001. File Service and cap-ai delegation
   listeners have no published host ports or gateway routes.
4. With the agent off, bootstrap the stack and reconcile the intended app packages.
   Use `docker compose -f compose.yaml -f compose.rag.yaml up -d --build` for
   FTS-only mode. For model storage, also pass `-f compose.models.yaml` before
   `up`, supply the schema flags below and configure `RAG_MODEL_CATALOG_FILE`
   and `RAG_MODEL_CREDENTIAL_ENV_FILE`. The latter is a private env file
   containing only credentials named by the catalog, readable by API and
   worker. An empty file suffices when the catalog references no secrets.
   Keep vendor credentials in the approved adapter deployment.
5. Select an actual registered `Ready` app for `AI_AGENT_BILLING_APP_ID`.
   Confirm its state through the application registry before enabling chat.
   Set `RAG_AGENT_ENABLED=true` and rerun the same Compose command. cap-ai
   validates the configured billing app at startup. With models off, chat
   returns owner-scoped lexical evidence. The Assistant uses the UI origin,
   which proxies protected requests through APISIX.
6. Before enabling model calls, qualify the bounded adapter, import disabled
   candidates, complete Platform Admin review and activate the exact EMBEDDING
   and/or LLM revisions through the deployment's audited slot procedure.
   Provision the processing reviews and budget accounts below. Then enable
   `RAG_MODELS_QUALIFIED`, `RAG_MODELS_ENABLED` and
   `RAG_GLOBAL_MODEL_REGISTRY_ENABLED` for the API and worker and recreate
   them. The agent only resolves global slots. Missing review, binding, budget
   or adapter keeps the affected operation unavailable. The optional overlays
   provide no active slot, budget, account identity or provider approval.

## Storage and activation

Apply `cap-ai/0004_models_vectors.sql` explicitly with `scripts/migrate-rag.sh`
only after pgvector is provisioned. This is additive after package migration 0003;
never change checksums of applied migrations. Bootstrap skips 0004 unless
`RAG_MODELS_SCHEMA_ENABLED=true`; that setting requires an image containing the
extension. No enabled profiles or provider approvals are seeded. Source guards,
originals, audit and lexical tables are retained when models are disabled.

The optional global registry adds `cap-ai/0005_model_registry.sql` after 0004.
Use `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope cap-ai
--through 0005` to opt in explicitly (the named environment variable contains the
connection URL). The runner applies and checksums 0004 and 0005 independently.
Bootstrap requires both `RAG_MODELS_SCHEMA_ENABLED=true` and
`RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true` for 0005; both schema flags default
to false. These schema flags do not enable runtime models or the registry.
Migration 0005 creates four empty slots and no candidates, enabled profiles,
provider approvals or bindings. VLM and RERANKING cannot be bound in R1.

The separate `dxos_ai_model_catalog` deployment login may execute only
`cap_ai.registry_import_candidates(jsonb)` within the registry. It cannot activate
profiles, change slots, policies or grants, or read protected knowledge. Provision
its credentials through deployment secrets; no runtime API/worker uses this login.
`dxos_ai_gateway` gains read-only `cap_ai.registry_resolve_slot(text)` execution.
The importer requires the exact catalog session identity and rejects unknown
fields, invalid limits and immutable revision conflicts atomically. Its R1
candidate subset is HOSTED EMBED/GENERATE; it stores only adapter keys and
credential environment-variable references, with no endpoint or credential value.
The caller-supplied declaration digest is metadata; full normalized declarations
are also compared, so equal digests never authorize conflicting definitions.
Activation requires an independently reviewed Platform Admin deployment operation.
R1 supplies candidate import and runtime slot resolution. The deployment must
provide its audited promotion/rollback procedure before enabling hosted models.
Authenticate the Platform Admin, verify the candidate digest, purpose, limits,
pricing and adapter, and record the review. The administration transaction
enables the reviewed profile, advances the expected slot revision to bind that
exact profile ID and revision, and records the actor and action in
`cap_ai.model_slot_events`. Concurrent revisions require a fresh review of
the current slot. API, worker, catalog and Gateway logins cannot do this.

The nine model tables hold immutable profile/policy revisions, policy allowlists,
independent embedding generations and accounting. Policy/profile revisions may
be disabled, but their definitions and allowlist history cannot be rewritten.
Provision each new approved policy/profile revision through reviewed operations;
record the audited Platform Admin approval and immutable review metadata in the
same provisioning transaction. Document policies and query policy are checked
separately. Existing APP processing keeps enabled `NON_PERSONAL`, app-bound hosted query
policies. Migration 0008 adds independently reviewed PERSONAL source and query
processing. AGENT consumers must still apply the canonical per-source predicates.
Mixed-source context retains PERSONAL handling requirements. Request JSON cannot
assert classification or choose credentials/endpoints. Do not amend an existing
local-only policy into a hosted approval; create a new reviewed revision and
explicitly bind eligible sources. Disabled or mismatched approvals deny egress.

EMBED jobs pin `profile@revision`, lexical run, chunk hashes and dimensions.
Only a complete generation is READY; lease loss, source removal, a newer desired
version or a disabled policy blocks writes/activation. Exact cosine ranking
materializes eligible active sources before ranking. No ANN or reranker is
installed. Fusion uses top 50 per list, RRF constant 60, stable ties, four chunks
per document and at most 20 results. Source policy/profile eligibility is checked
again for vector retrieval. Role membership is never embedded into vectors.

## Audited PERSONAL processing provisioning

Migration `0008_personal_model_processing.sql` adds the approval interface to the
existing `dxos_authz` deployment connection. Both approval and disable operations
require an ACTIVE Core user with a current DB `role_operator` grant. Application
admins, API, worker, Gateway and `dxos_ai_model_catalog` cannot approve processing.
The functions authenticate `session_user`, reject substituted roles and privileged
runtime logins, and audit the verified actor and deployment session. There is no
management UI in this increment. The deployment operator runs these functions
through a protected `dxos_authz` connection after authenticating the Platform Admin.

For an FTS-only upgrade, `scripts/migrate-rag.sh --database-url-env DATABASE_URL
--scope cap-ai --through 0007` skips optional 0004/0005. The existing explicit
`--through 0004` and `--through 0005` opt-ins retain their meaning. To install
audited PERSONAL processing, hybrid search and AGENT model authority, set all
three schema flags to `true` in the migration environment: `RAG_MODELS_SCHEMA_ENABLED`,
`RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED`, and `RAG_AGENT_MODELS_SCHEMA_ENABLED`.
Use `--through 0010` to apply these migrations. The bootstrap runner uses the
same prerequisites. The agent-model flag defaults to `false` and also gates
later model-dependent migrations when present. None of these flags activate a
profile, slot or approval, and migration 0008 seeds no approvals.

The minimal approval object binds one immutable provider/profile revision and
purpose, one existing PERSONAL `LOCAL_ONLY` source-policy revision, and an
independent PERSONAL query classification for one Ready app. It records
retention, training-use and region review evidence. This is a processing review,
not a rewrite of the parsing policy: source ownership and `LOCAL_ONLY` parsing
remain unchanged. A matching review is required for every supplied source;
a query-only EMBED call independently requires an applicable query review.
The SQL interface deliberately supports PERSONAL/PERSONAL only; APP reviews
continue through their existing processing-policy contract.

Provision the connection using deployment secrets in standard libpq environment
variables (`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER=dxos_authz`, `PGPASSWORD`, and
`PGSSLMODE`). Do not pass the password or connection URL on the command line.
Set `APPROVER_CORE_USER_ID` to the authenticated Platform Admin Core UUID.
With those variables already supplied, a reviewed operation has this form:

```sh
psql -X -v ON_ERROR_STOP=1 -v approver="$APPROVER_CORE_USER_ID" <<'SQL'
SELECT cap_ai.approve_agent_processing(:'approver'::uuid, '{
  "profile_id":"reviewed-generator",
  "profile_revision":1,
  "provider_id":"reviewed-provider",
  "purpose":"GENERATE",
  "source_policy_id":"local-only",
  "source_policy_version":1,
  "source_classification":"PERSONAL",
  "query_app_id":"personal-library",
  "query_classification":"PERSONAL",
  "retention_evidence":"review-record/retention",
  "training_use_evidence":"review-record/training-use",
  "region_evidence":"review-record/processing-region"
}'::jsonb) AS approval_id;
SQL
```

Replace the example identities and review references with the actually reviewed
immutable profile, Ready app and evidence. EMBED needs its own review with
`purpose: EMBED` and the exact embedding profile revision. The returned UUID
identifies the review. Definitions and audit events are immutable. Retrying an
identical review while it is active returns its existing UUID, including
after a lost response. Approving again after withdrawal creates a new revision.
To withdraw a review, use the same protected connection.

```sh
psql -X -v ON_ERROR_STOP=1 -v approver="$APPROVER_CORE_USER_ID" \
  -v approval="$PROCESSING_APPROVAL_ID" <<'SQL'
SELECT cap_ai.disable_agent_processing(
  :'approver'::uuid, :'approval'::uuid, 'Review withdrawn');
SQL
```

Disable is idempotent and audited atomically; the disabled revision cannot be
reactivated. Set `PROCESSING_APPROVAL_ID` to the returned review UUID before
running the disable example. Disable every applicable review when withdrawing all permission for
a provider/profile, including any separately issued EMBED/GENERATE reviews.
No direct approval-table writes are needed or granted to this connection. Review
records contain references to evidence, never credentials, prompts or source text.
The Gateway rechecks current approval after reservation and call intent; workers
also recheck the exact lease fence, source snapshot and current owner READ.

Migration `0009_personal_hybrid.sql` records the owner, source authority and exact
hosted approval revision on each PERSONAL embedding job and generation. Search
checks current owner READ and source approval before exact vector ranking.
A generation qualifies only when every expected chunk has a usable vector of
the pinned dimension. If the selected collections have no qualified READY
generation, search returns `LEXICAL_ONLY` with `SEMANTIC_UNAVAILABLE` without
sending the query.

A new approval after withdrawal has a new revision. The worker queues a separate
build for the currently approved source snapshot, including when the parsed
content and embedding profile are unchanged. Each build has its own billable job
identity, and retries of that build keep the same identity. Hybrid search
excludes generations tied to a superseded snapshot and pre-0009 PERSONAL
generations that lack a recorded snapshot. Authorized FTS remains available
while the current generation builds.

Migration `0010_agent_model_authority.sql` checks each source's current app,
role, READ snapshot and processing policy before a grounded AGENT generation.
Use the audited provisioning function above for the following global-query reviews.

- For hosted query-only EMBED, review the exact embedding provider and profile
  revision with `purpose` set to `EMBED`, `query_app_id` set to the app being
  searched, and `query_classification` set to `PERSONAL`. APP scopes also need
  their ordinary app-bound processing approval. Repeat this review for each
  eligible app where hosted global-query embedding is intended.
- For AGENT GENERATE, review the exact generation provider and profile revision
  with `purpose` set to `GENERATE`, `query_app_id` set to
  `AI_AGENT_BILLING_APP_ID`, and `query_classification` set to `PERSONAL`.
  Apply this review even when every selected source is APP. Each PERSONAL
  source still needs its matching source-policy review for its library app.
  Each APP source still needs its existing approved processing policy.

Both operations require a current review for the question because any global
question may contain PERSONAL information. The billing consumer needs its own
review and budget, while READ and source processing authority come from each
actual source.

## Deployment configuration

API and worker independently load `models.FromEnvironment`. Defaults are off.
Enabling requires all of `RAG_MODELS_ENABLED`, `RAG_MODELS_QUALIFIED`,
`RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED`, `RAG_PARSER_QUALIFIED` to be
`true`, plus `AI_GATEWAY_DATABASE_URL` and `RAG_MODEL_DEPLOYMENT_FILE`.
The Gateway connection must log in as `dxos_ai_gateway`, without superuser or
BYPASSRLS. It executes policy/accounting functions and cannot export chunks,
change grants or mutate policy. The API and worker keep their existing roles.
Bootstrap accepts `AI_GATEWAY_DATABASE_PASSWORD` only with the optional schema
setting. Store secrets in deployment secrets, never in manifests or the browser.

The current Plan05 private deployment JSON has `providers`, `embed_profile` and
`generate_profile`. Profile references contain `id` and `revision`. Each provider
contains `id`, `endpoint`, `credential_ref` (an environment-variable reference)
and `loopback_test`. Profiles and deployment references must agree. HTTPS is
required; redirects, URL credentials and query strings are rejected. An explicit
`RAG_MODEL_TEST_MODE=true` permits a loopback HTTP test adapter. This is not a
production provider approval. Existing LOCAL profiles additionally require
`RAG_LOCAL_MODELS_QUALIFIED=true`; no local model is bundled or prequalified, and
local-model serving is outside the new harness scope. APP and owner-only
PERSONAL search use bounded FTS and embedding fusion with qualified profiles.
PERSONAL hybrid search requires migration 0009. Hosted PERSONAL embedding and
generation each require a current audited approval for that purpose. Absent or
unqualified semantic models preserve authorized FTS. PERSONAL-only model
operations retain the actual registered library app for billing, regardless of
UI context.

The HTTP adapter receives a fixed system instruction, model, purpose, bounded
JSON input and pinned limits. It returns bounded `text`, `vector`,
`citation_ids`, `usage` and optional `status`; unknown fields reject. An optional
`X-Request-ID` supports billing reconciliation. This is a deployment adapter
protocol, not a claim of compatibility with a vendor's API. Qualify its tokenizer,
context, output caps, pricing and billing agreement before real use. Input token
admission uses a conservative byte bound and a fixed envelope allowance; whole
chunks are omitted rather than truncating tables to fit. No tools, SQL execution,
URL fetching or streaming are exposed.

## Global registry compatibility

The [R1 registry contract](../../reference/rag/api.md#global-model-registry-foundation-r1)
defines a separate `RAG_GLOBAL_MODEL_REGISTRY_ENABLED=false` default. This is an
opt-in loader; the OFF path uses the legacy profile references
described above. Turning it ON requires the additive registry migrations; the
flag does not qualify a provider or bypass existing enablement gates.

With the registry ON, DB `ACTIVE` slots supply the exact EMBEDDING/LLM profile
revisions. Deployment configuration declares candidates and adapter/secret
references only; legacy `embed_profile`/`generate_profile` cannot override slots
or serve as fallback. Apps and users cannot select models. Unbound/unavailable
models preserve authorized FTS and optional search-only answers; required
generation with evidence returns 503 `MODEL_UNAVAILABLE`. VLM is reserved and
unbound, and RERANKING remains disabled. Follow the canonical contract for
provisioning identities, future Platform Admin lifecycle, and consumer impacts.

For new chat, `AGENT` source selection is server-side across all current DB role
memberships and owner-only PERSONAL sources. The turn accepts only a question. Role claims, client lists and LLM app suggestions confer no authority.
Existing Hasura/capability APP routes keep ADR 0007's selected-role behavior.

## Budgets and uncertain charges

Provision exactly one active INSTALLATION account (`installation`), APP account
(app ID) and USER account (user ID, or the genuine worker Core identity for
background ingestion), with the same pricing revision as the profile. Overlapping
or missing periods deny reservation. Accounts lock in UUID order in one
transaction; reservation, allocations and RESERVE events commit together.
Operation keys bind actor, profile, source/policy fingerprint and payload hash.
Concurrent/replayed call intent cannot send the same operation twice.

For cross-app AGENT model operations, configure `AI_AGENT_BILLING_APP_ID` to a
genuine registered app currently `Ready` and provision its APP budget through
admin operations. The model call charges that APP account, the requesting
actor's USER account and INSTALLATION account once. Missing/invalid config,
missing budget or pricing mismatch denies the model call. This pragmatic MVP
attribution reuses the APP ledger; a dedicated platform billing scope is
deferred. The billing consumer requires the independent PERSONAL query review
described above. Every actual source app and PERSONAL source must pass its own
READ and processing checks. See
[canonical billing attribution](../../reference/rag/authorization.md#model-operation-billing-attribution).
The RAG overlay supplies the configured billing consumer to cap-ai. The public
APISIX route preserves selected-role behavior for existing knowledge APIs.

Reserve the bounded worst-case input/output price before persisting intent.
Recheck authority after budget waits and after intent, immediately before send.
If a call might have been billed, retain UNCERTAIN funds and do not resend that
operation. An uncertain EMBED job becomes FAILED with
`MODEL_CHARGE_UNCERTAIN`; it requires operations resolution instead of automatic
retry. Operation identity for each embedding chunk stays stable across lease
fences, so a crash after send cannot create an automatic second billed call.
Other explicitly approved new attempts must be separately accounted.

`models.Reconciler.Once` accepts a deployment-qualified invoice inspector.
Definitive usage settles once in the original period. Unknown invoice outcomes
retain funds; the returned unresolved count is an operations escalation, not a
release instruction. A reconciled overage records the debt and blocks future
reservation instead of hiding cost. There is no real invoice adapter bundled.
Usage tables store units/tokens/pricing and opaque request identity, never
prompts, questions, source text or provider diagnostics. No content READ is
conferred by accounting; there is no public usage-admin endpoint.

## Answer API

`POST /api/cap/ai/answers` accepts `{scope,query,max_citations?,require_generation?}`.
Response: `{status,answer,results,citations,reason?,model_profile?,usage?}`.
`answer` is null for SEARCH_ONLY/NO_EVIDENCE. Status is ANSWERED, SEARCH_ONLY or
NO_EVIDENCE. At most eight whole evidence chunks enter generation. Every supplied
context source is checked after the model returns, including uncited context.
If any becomes unavailable, the response contains no old text, titles, results or
citations and reason `AUTHORIZATION_CHANGED`. Fabricated citation IDs abstain with
`UNSUPPORTED_ANSWER`. ID validation establishes provenance, not semantic truth.

Policy/quota/outage returns scoped search-only unless `require_generation=true`,
which returns 403 POLICY_BLOCKED, 429 QUOTA_EXCEEDED or 503 MODEL_UNAVAILABLE.
No evidence makes no generation call. The app UI shows a grounded answer with
protected citations, explicit abstention or search-only, and cancels old context
on app/role change. Missing PERSONAL approval never triggers hosted fallback.
New chat history is owner-only: old snapshots remain readable after ordinary
source revoke, while linked originals/citations require current source READ.
Reauthorize source-derived history, including summaries, before new generation;
readable history alone is not reusable evidence.

## Qualification

Run capability unit tests, Go integration with real PostgreSQL/pgvector and
`-p 1` (fixtures share cluster-global role DDL), focused race tests, UI tests/build,
and `scripts/test-rag.sh models`. The models runner requires all source-service
inputs plus two explicit loopback fake adapters, test-only profile IDs, Operator,
dispatcher and Chromium/storage-state inputs listed by the shell preflight.
`scripts/integration/rag_model_fake.py` is only a disposable test spy; never
expose its controls publicly. The runner provisions reviewed fixture policy only
for its new test app and `rag-test-fake`, never a real processor.

R11/R12/R17/R18/R19/R20 cover scoped vectors, fresh final authority, independent
readiness, the current personal zero-egress restriction, conservative accounting
and answers. Those existing cases do not prove the amended approval policy.
The Go integration suite separately exercises approved PERSONAL EMBED/GENERATE, denied unapproved
source/query egress, disabled/mismatched approval and retention/training-use
review, per-source multi-role authority, retained owner-only snapshots without
revoked-context reuse, and exact AGENT INSTALLATION/APP/USER charge attribution.
Earlier R01–R16 source/package gates remain prerequisites. A deterministic fake gold case
measures transport, citation/provenance plumbing and latency; it does not qualify
real language-model groundedness, injection robustness, tokenizer limits or
invoice reconciliation. Missing real-provider/classification approval leaves
hosted production enablement blocked. No overall S01–S08 release claim follows.

Registry mode reads a strict catalog from `RAG_MODEL_DEPLOYMENT_FILE`:

```json
{
  "schema_version": 1,
  "providers": [
    {
      "id": "hosted",
      "adapter": "bounded-http-v1",
      "endpoint": "https://provider.example",
      "credential_ref": "MODEL_API_KEY"
    }
  ],
  "models": []
}
```

An empty `models` array supports runtime startup after deployment provisioning.
To provision candidates, each model entry has `adapter: "bounded-http-v1"` and
a `profile` with `id`, `revision`, `purpose`, `execution`, `provider_id`,
`model_id`, `credential_ref`, `pricing_revision`, and `limits`; EMBED also
requires `dimension`. Follow the linked canonical contract for validation.
Startup never imports candidates. Runtime verifies the immutable declaration
digest against provider metadata; endpoint or loopback changes require a new
profile revision and provisioning. Slot reads and fenced embedding lease profile
reads use narrowly granted gateway functions. Registry enablement alone provides
no production qualification.
