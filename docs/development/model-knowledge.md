# Optional model retrieval and answers

The model boundary is optional. Personal libraries and package lexical search
continue without pgvector, profiles, credentials or model availability. Personal
content **and questions** cannot use hosted EMBED, RERANK, GENERATE or OCR. App
selection is not a permission grant. Legacy `/rag` remains unavailable.

## Storage and activation

Apply `cap-ai/0004_models_vectors.sql` explicitly with `scripts/migrate-rag.sh`
only after pgvector is provisioned. This is additive after package migration 0003;
never change checksums of applied migrations. Bootstrap skips 0004 unless
`RAG_MODELS_SCHEMA_ENABLED=true`; that setting requires an image containing the
extension. No enabled profiles or provider approvals are seeded. Source guards,
originals, audit and lexical tables are retained when models are disabled.

The nine model tables hold immutable profile/policy revisions, policy allowlists,
independent embedding generations and accounting. Policy/profile revisions may
be disabled, but their definitions and allowlist history cannot be rewritten.
Provision each new approved policy/profile revision through reviewed operations;
record approver, app query classification, provider/data-handling and retention
approval, and an audit record in the same provisioning transaction. Document
policies and query policy are checked separately. A hosted query requires an
enabled `NON_PERSONAL`, app-bound policy with matching provider and profile;
request JSON cannot assert classification or choose credentials/endpoints.

EMBED jobs pin `profile@revision`, lexical run, chunk hashes and dimensions.
Only a complete generation is READY; lease loss, source removal, a newer desired
version or a disabled policy blocks writes/activation. Exact cosine ranking
materializes eligible active sources before ranking. No ANN or reranker is
installed. Fusion uses top 50 per list, RRF constant 60, stable ties, four chunks
per document and at most 20 results. Source policy/profile eligibility is checked
again for vector retrieval. Role membership is never embedded into vectors.

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

The private deployment JSON has `providers`, `embed_profile` and
`generate_profile`. Profile references contain `id` and `revision`. Each provider
contains `id`, `endpoint`, `credential_ref` (an environment-variable reference)
and `loopback_test`. Profiles and deployment references must agree. HTTPS is
required; redirects, URL credentials and query strings are rejected. An explicit
`RAG_MODEL_TEST_MODE=true` permits a loopback HTTP test adapter. This is not a
production provider approval. LOCAL profiles additionally require
`RAG_LOCAL_MODELS_QUALIFIED=true`; no local model is bundled or prequalified. Personal semantic retrieval is deferred:
personal queries use FTS, while a qualified local generator can consume that
evidence. Local personal generation attributes budgets to the registered library
app, regardless of UI context.

The HTTP adapter receives a fixed system instruction, model, purpose, bounded
JSON input and pinned limits. It returns bounded `text`, `vector`,
`citation_ids`, `usage` and optional `status`; unknown fields reject. An optional
`X-Request-ID` supports billing reconciliation. This is a deployment adapter
protocol, not a claim of compatibility with a vendor's API. Qualify its tokenizer,
context, output caps, pricing and billing agreement before real use. Input token
admission uses a conservative byte bound and a fixed envelope allowance; whole
chunks are omitted rather than truncating tables to fit. No tools, SQL execution,
URL fetching or streaming are exposed.

## Budgets and uncertain charges

Provision exactly one active INSTALLATION account (`installation`), APP account
(app ID) and USER account (user ID, or the genuine worker Core identity for
background ingestion), with the same pricing revision as the profile. Overlapping
or missing periods deny reservation. Accounts lock in UUID order in one
transaction; reservation, allocations and RESERVE events commit together.
Operation keys bind actor, profile, source/policy fingerprint and payload hash.
Concurrent/replayed call intent cannot send the same operation twice.

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
on app/role change. Personal search has no hosted fallback.

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
readiness, personal zero egress, conservative accounting and answers. Earlier
R01–R16 source/package gates remain prerequisites. A deterministic fake gold case
measures transport, citation/provenance plumbing and latency; it does not qualify
real language-model groundedness, injection robustness, tokenizer limits or
invoice reconciliation. Missing real-provider/classification approval leaves
hosted production enablement blocked. No overall S01–S08 release claim follows.
