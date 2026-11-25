---
title: Model answers
description: Configure the optional model gateway for knowledge answers.
diataxis: how-to
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-24
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
retrieval and the mixed-source AGENT model boundary. Migration 0011 adds the
narrow audited slot administration functions and the deployment administration
login; it follows the same agent-schema opt-in and changes no default. Follow the
[ADR 0009 migration/consumer delta](../../adr/0009-native-rag-identity-and-source-boundaries.md#contract-amendment-and-implementation-delta-2026-11-18)
before enabling the new behavior; provisioning an active slot alone cannot
implement or authorize it.

## Compose installation

The base `compose.yaml` runs the regular stack. Add `compose.rag.yaml` for the
knowledge API, private File Service delegation, package dispatcher, parser and
worker. Add `compose.models.yaml` only for model schema/runtime support. Use
the same file order and project name throughout an installation.

1. From a clean checkout, run:
   ```bash
   make rag-up
   ```
   It uses the same Compose project as `docker compose up`; set
   `COMPOSE_PROJECT_NAME` and `POSTGRES_PORT` to run a separate stack.
   The target calls `scripts/rag-up.sh`, which creates `.env.rag` (git-ignored,
   mode 0600) when it is missing. It fills random DB passwords, a parser token
   and the three Keycloak client secrets, and sets the fixed service subjects,
   client ids and FTS flags. It never prints or overwrites a secret; a later run
   leaves the file unchanged.
2. The script starts the base and RAG files with
   `docker compose --env-file .env --env-file .env.rag -f compose.yaml -f
   compose.rag.yaml up -d --build` (merging the files for older Compose
   releases). It waits for `personal_library` to become `Ready`, sets
   `AI_AGENT_PROCESSING_APP_ID=personal_library` and `RAG_AGENT_ENABLED=true`, and
   reruns `up -d`. The UI is at `http://localhost:5173`; the Assistant and
   personal library are on. `scripts/test-compose.sh` and the demo seed remain
   optional follow-ups.
3. The overlay enables `cap-ai` in `ENABLED_CAPABILITIES`. That list is applied
   only when the PostgreSQL volume is first initialized. If an existing volume
   was created without it, `rag-up` stops with a message to recreate that volume;
   it does not delete data. The models overlay also requires
   `AI_GATEWAY_DATABASE_PASSWORD` and selects PostgreSQL 17 with pgvector. To
   upgrade retained data, use the checksummed migration procedure below,
   register cap-ai and provision its restricted roles and passwords before app
   reconciliation, and back up the volume before changing its image.
4. The services obtain their own tokens: `dxos-rag-worker`,
   `dxos-file-service` and `dxos-rag-dispatcher` are confidential realm clients
   with service accounts, each with a fixed subject that is an ACTIVE Core
   identity. The realm imports them disabled with a generated secret, so a stack
   without this overlay cannot mint service tokens. `rag-up` starts Keycloak
   first, then enables the three clients through the admin API with the
   `RAG_*_CLIENT_SECRET` values from `.env.rag`. Keycloak's public hostname is
   fixed with `KEYCLOAK_HOSTNAME` (default `http://localhost:8085`) so tokens
   fetched over the internal network carry the issuer the services verify; it
   must match `OIDC_ISSUER_URL`. The worker, File Service delegation client
   and dispatcher refresh at 75% of the token lifetime, so no manual renewal or
   container recreation is needed. `PARSER_SERVICE_TOKEN` is still a separate
   random secret of at least 32 characters, and the parser and worker share a
   private socket volume and UID 10001. API, worker and dispatcher each connect
   under the corresponding restricted DB login.
5. Model activation stays manual. Add `compose.models.yaml`, provide the schema
   flags, catalog and credential file, and follow the steps below. For the bundled
   adapter, additionally prepare the private files and add
   `compose.model-adapter.yaml` as the last `-f` file
   ([bundled hosted adapter](#bundled-hosted-adapter-opt-in)). `make rag-up` never
   enables models and never starts the adapter.
6. Before enabling model calls, qualify the declared routes (`dxos-model validate`
   and `dxos-model verify`) and provision the processing reviews below. Then have
   the deployment reconcile the reviewed selection, and enable `RAG_MODELS_ENABLED`
   for the API and worker and recreate them. The reviewed declaration and its
   reconciled selection are the only model lifecycle: there is no runtime catalog,
   no separate qualification flag and no slot to activate. Missing review, a
   selection the deployment did not reconcile or an unusable proxy keeps the
   affected operation unavailable — the process still boots, FTS parsing and
   search still work, and no model call is made.

### Advanced: bring your own identities

Use this path when the deployment already issues tokens or manages its own
Keycloak clients.

- Set `RAG_WORKER_SERVICE_SUBJECT`, `RAG_FILE_SERVICE_SUBJECT` and
  `RAG_DISPATCHER_SERVICE_SUBJECT` to distinct OIDC subjects and register each
  as an ACTIVE Core identity. The shipped realm clients use the fixed subjects
  in `.env.example`.
- Either supply renewable audience-matched tokens in
  `RAG_WORKER_BEARER_TOKEN`, `RAG_FILE_BEARER_TOKEN` (cap-process receives it
  as `RAG_INTERNAL_BEARER_TOKEN`) and `RAG_DISPATCHER_BEARER_TOKEN`, or leave
  them empty and set the `RAG_*_CLIENT_ID` / `RAG_*_CLIENT_SECRET` pairs. A
  configured static token wins; otherwise the service uses client credentials.
  Static tokens must be renewed before expiry and their consumers recreated.
- Configure the same issuer, JWKS and audience on the services
  (`OIDC_ISSUER_URL`, `OIDC_JWKS_URL`, `OIDC_AUDIENCE`) and the internal token
  endpoint (`OIDC_TOKEN_URL`). Set `KEYCLOAK_HOSTNAME` to the public origin of
  that issuer, so tokens requested over the internal Keycloak hostname still
  carry the issuer the services verify.
- Use a separate random `PARSER_SERVICE_TOKEN` of at least 32 characters and
  separate DB passwords.
- Set `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` and
  `RAG_PARSER_QUALIFIED` only after verifying the source and parser
  prerequisites. Set `RAG_METADATA_ENABLED=true` and enable
  `RAG_PACKAGE_ENABLED`, `RAG_PERSONAL_ENABLED`, or both. Enable
  `RAG_PRIVATE_UPLOAD_ENABLED` for private uploads.
- Run `docker compose -f compose.yaml -f compose.rag.yaml up -d --build` with
  your env file. Select a registered `Ready` app for
  `AI_AGENT_PROCESSING_APP_ID`, set `RAG_AGENT_ENABLED=true`, and rerun the same
  command. With models off, chat returns owner-scoped lexical evidence.

## Storage and activation

Apply `cap-ai/0004_models_vectors.sql` explicitly with `scripts/migrate-rag.sh`
only after pgvector is provisioned. This is additive after package migration 0003;
never change checksums of applied migrations, with the single coordinated
exception recorded in
[ADR 0010](../../adr/0010-model-operations-without-monetary-admission.md).
Bootstrap skips 0004 unless
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

### Rebasing an existing installation

`0004` and `0005` were rebased in place for the coordinated baseline
([ADR 0010](../../adr/0010-model-operations-without-monetary-admission.md)).
A database that applied the earlier model checksums cannot be upgraded in place:
both runners detect the conflict before any domain SQL and refuse to proceed, and
no ledger edit or checksum bypass is an accepted remedy. The installation owner
must rebuild a named database on the approved revision, bootstrap it, load valid
`schema_version: 2` declarations and current model approvals, and start matching
binaries against it. Do not restore a `cap_ai` dump wholesale onto the rebased
schema: it reintroduces the removed monetary relations and rows and carries
operation rows that no longer match. Re-derive model state instead—re-publish
packages, re-register sources, re-import candidates, re-approve processing
policies and re-issue grants. The scoped rebuild procedure, approved evidence and
service window are in [installation](../../operations/installation.md#rebuilding-an-existing-model-installation).

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
Migration 0011 supplies that operation; the `dxos-model activate`, `deactivate`,
`slots` and `list` commands
([CLI reference](../../reference/cli/dxos-model.md)) are its only interface.
Authenticate the Platform Admin, verify the candidate digest, purpose, limits
and adapter, and record the review. The administration transaction
enables the reviewed profile, advances the expected slot revision to bind that
exact profile ID and revision, and records the actor and action in
`cap_ai.model_slot_events`. Concurrent revisions require a fresh review of
the current slot. API, worker, catalog and Gateway logins cannot do this.

The model tables hold immutable profile/policy revisions, policy allowlists,
independent embedding generations and durable operation state. Policy/profile revisions may
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

After activating a new EMBEDDING revision, keep the worker running. Each
`Worker.Once` poll resolves the current slot and calls `cap_ai.queue_embeddings`
through the worker login for eligible active document versions, including
previously parsed documents. The new revision needs its own reviewed processing
policy allowlist or PERSONAL approval before a job can be queued. Queueing is
idempotent for the version and exact profile identity. The worker then resolves
the fenced lease's pinned revision before building its generation. Keep old
provider routes available until their outstanding leases have finished.

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
through a protected `dxos_authz` connection after authenticating the Platform Admin,
either with `psql` as shown here or with
`dxos-model policy approve|revoke` on `AI_MODEL_APPROVAL_DATABASE_URL` and a
protected token ([CLI reference](../../reference/cli/dxos-model.md)).

For a retained default volume, back up first and connect as the deployment database
administrator using standard libpq environment variables (`PGHOST`, `PGPORT`,
`PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE`). Set nonempty, URL-safe
`AI_DATABASE_PASSWORD`, `AI_WORKER_DATABASE_PASSWORD` and
`AI_DISPATCHER_DATABASE_PASSWORD` in the same protected environment. From the
repository root, run the following before reconciling apps. The registry upsert
and role creation are idempotent; rerunning updates these three passwords.

```sh
psql -X -v ON_ERROR_STOP=1 -f platform/postgres/capabilities/cap_ai.sql
psql -X -v ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_ai_api','dxos_ai_worker','dxos_ai_dispatcher'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', name);
    END IF;
    EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT', name);
  END LOOP;
END $$;
\getenv api_password AI_DATABASE_PASSWORD
\getenv worker_password AI_WORKER_DATABASE_PASSWORD
\getenv dispatcher_password AI_DISPATCHER_DATABASE_PASSWORD
ALTER ROLE dxos_ai_api PASSWORD :'api_password';
ALTER ROLE dxos_ai_worker PASSWORD :'worker_password';
ALTER ROLE dxos_ai_dispatcher PASSWORD :'dispatcher_password';
SQL
```

Use deployment secrets to supply these same passwords to the runtime services.
For model storage, additionally provision the restricted `dxos_ai_gateway`
login and its password through deployment administration before applying the
model migrations; the fresh-volume bootstrap uses `08_runtime_passwords.sh`.
Set `DATABASE_URL` in the protected environment to the administrator connection
URL for the migration runner.

For an FTS-only upgrade, `scripts/migrate-rag.sh --database-url-env DATABASE_URL
--scope cap-ai --through 0007` skips optional 0004/0005. The existing explicit
`--through 0004` and `--through 0005` opt-ins retain their meaning. To install
audited PERSONAL processing, hybrid search and AGENT model authority, set all
three schema flags to `true` in the migration environment: `RAG_MODELS_SCHEMA_ENABLED`,
`RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED`, and
`RAG_AGENT_MODELS_SCHEMA_ENABLED`.
Use `--through 0010` to apply these migrations, or `--through 0012` to add the
audited slot administration functions, the `dxos_ai_model_admin` login and
PERSONAL approval authority locks. The
bootstrap runner uses the same prerequisites. The agent-model flag defaults to
`false` and also gates later model-dependent migrations when present. None of
these flags activate a profile, slot or approval, and migration 0008 seeds no
approvals; migration 0011 seeds no slot event either.

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
The Gateway rechecks current approval after the operation claim and dispatch intent; workers
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
content and embedding profile are unchanged. Each build has its own durable
operation identity, and retries of that build keep the same identity. Hybrid search
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
  `AI_AGENT_PROCESSING_APP_ID`, and `query_classification` set to `PERSONAL`.
  Apply this review even when every selected source is APP. Each PERSONAL
  source still needs its matching source-policy review for its library app.
  Each APP source still needs its existing approved processing policy.

Both operations require a current review for the question because any global
question may contain PERSONAL information. The processing consumer needs its own
query review, while READ and source processing authority come from each
actual source.

## Coordinated deployment with `make ai-up`

`make ai-up` deploys one reviewed declaration on the existing Compose project and
moves the API and worker to one verified snapshot. It is the only supported way to
change the live model selection; `dxos-model` still validates, probes and reports,
and `deployment render|reconcile` remain deployment-internal.

```sh
cp models.example.yaml models.yaml   # then fill in a reviewed route
make ai-up
make ai-up AI_UP_ARGS="--config /etc/dxos/models.yaml --secret-env /etc/dxos/model-secrets.env"
```

`--secret-env` is an owner-only (`0600`) environment file holding the values of the
credential references the declaration names (for example `OPENROUTER_API_KEY`).
Only referenced keys are copied into the generated proxy environment; no value is
printed. `--env-file` selects the installation environment file (default
`.env.rag`). Generated private files live in `build/ai-up/`; the model settings the
Compose project reads live in `.env.models`. `--model-env FILE` (or
`AI_UP_MODEL_ENV`) and `--state-dir DIR` (or `AI_UP_STATE_DIR`) relocate the latter
two, which is how a disposable or parallel deployment keeps its files away from
testing installations. The deployment lock is keyed by the Compose project in a
stable per-user directory (`$XDG_RUNTIME_DIR/dxos/ai-up`, falling back to
`~/.cache/dxos/ai-up`), so two checkouts deploying the same project serialize
even with different state directories; `AI_UP_LOCK_DIR` overrides that location
for tests.

The deployment runs in this order, and every step happens before the next one
touches a running service:

1. **Validate offline.** The declaration is loaded, rendered and reported by the
   same Go code the runtime uses. An invalid declaration exits non-zero and leaves
   a running valid deployment untouched.
2. **Check the Compose target.** The project, service set and required overlays are
   checked; a model declaration without the proxy overlay, or an Ollama route
   without the local overlay, stops before anything starts. An existing project is
   reused, never recreated.
3. **Provision the optional schema — only when the declaration selects models.**
   PostgreSQL starts; for a declaration that enables a route and declares at least
   one purpose, the migration authority applies the additive `cap_ai` chain
   (including pgvector and the deployment authority) when the volume lacks it, and
   sets the passwords of the restricted `dxos_ai_gateway` and
   `dxos_ai_model_catalog` logins. A declaration that disables models, or declares
   no route, provisions neither pgvector nor the model schema: it initializes and
   runs the same FTS schema `make rag-up` does, and the schema switches in
   `.env.models` follow the declaration. No migration checksum is reset, no
   database is dropped, and `down -v` is never run. A volume that cannot be
   repaired in place is reported with the documented rebuild path.
4. **Start the proxy only if selected.** `litellm` is started when the declaration
   enables a route; the Ollama overlay is added only for a declared Ollama route and
   never downloads weights. A local endpoint outside these overlays (an operator's
   own vLLM-compatible service) must already be reachable on the private network.
5. **Probe every configured route.** One bounded synthetic call per purpose, through
   the same private transport the runtime uses. A route that does not answer is
   reported and is not selected.
6. **Drain, reconcile once, restart.** The model-calling API and worker are stopped
   first, so no service dispatches while the selection changes and the old worker's
   lease can no longer be renewed. One reconciliation records exactly the qualified
   purposes; a failed purpose is withdrawn in the same transaction rather than
   replaced by another model. The API and worker then start on that snapshot.
7. **Check the started identity.** `dxos-model status` must report the exact
   declared profile for every selected purpose, and the runtime logs must not
   report a degraded model path. A mismatch stops the model-calling services
   instead of leaving a mismatch dispatching.

A failure after the cutover leaves the installation serving authorized lexical
search and exits non-zero, so it is visible in automation. The previous model is
never restored automatically: correct the declaration or the endpoint and rerun
`make ai-up`. Rerunning the same declaration is idempotent — it creates no profile,
generation or job and restarts no service. Changing only the generation route
leaves the embedding identity and every completed vector untouched; changing the
embedding route creates a new immutable profile, keeps old generations isolated and
re-embeds eligible documents through the existing worker (`Worker.Once`) without a
reparse, re-OCR or lexical reset.

`make rag-up` stays the FTS-only path: it never provisions pgvector or the model
schema, never starts the proxy and leaves `RAG_MODELS_ENABLED=false`. A `make ai-up`
declaration that disables models behaves the same way on a fresh volume: no
pgvector, no model schema, no proxy, and the same lexical runtime.


API and worker independently load `models.RuntimeFromEnvironment`. Defaults are off.
The runtime has exactly one model source: the reviewed deployment declaration
(`RAG_MODEL_DEPLOYMENT_FILE`, default `models.yaml`) and the exact selection the
deployment reconciled from it. Enabling requires `RAG_MODELS_ENABLED`,
`RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` and `RAG_PARSER_QUALIFIED` to be
`true`, plus `AI_GATEWAY_DATABASE_URL`, the private proxy origin
`RAG_MODEL_LITELLM_ENDPOINT` and its master key `LITELLM_MASTER_KEY`. There is no
`RAG_MODELS_QUALIFIED` and no runtime catalog. The Gateway connection must log in
as `dxos_ai_gateway`, without superuser or BYPASSRLS. It executes the policy and
operation-state functions and cannot export chunks, change grants or mutate
policy. The API and worker keep their existing roles. Bootstrap accepts
`AI_GATEWAY_DATABASE_PASSWORD` only with the optional schema setting. Store secrets
in deployment secrets, never in manifests or the browser.

A valid declaration whose live state disagrees with it — an unreconciled,
withdrawn or drifted selection, an unreachable authority, or a generated proxy
configuration that is not exactly what the declaration renders — boots with model
calls unavailable and leaves FTS parsing and search intact. Only a declaration the
runtime cannot accept at all (invalid content, an unrenderable route, an
unqualified local route, models enabled without their qualified predecessors) is a
startup error. The declaration's shape is documented in
[`models.example.yaml`](../../../models.example.yaml) and the
[dxos-model CLI reference](../../reference/cli/dxos-model.md): it is YAML with at
most one embedding and one generation route, and each route names its provider,
model, limit bounds and (for embeddings) dimension. HTTPS is required for a hosted
`api_base`; redirects, URL credentials and query strings are rejected. A LOCAL
route additionally requires `RAG_LOCAL_MODELS_QUALIFIED=true`; no local model is
bundled or prequalified, and local-model serving is outside the new harness scope.
`RAG_MODEL_LITELLM_CONFIG_FILE`, when set, must be byte-identical to
`models.RenderProxyConfig(deployment)`, so a hand-edited proxy route is never
served. APP and owner-only PERSONAL search use bounded FTS and embedding fusion
with the selected profiles. PERSONAL hybrid search requires migration 0009. Hosted
PERSONAL embedding and generation each require a current audited approval for that
purpose. Absent or unqualified semantic models preserve authorized FTS. PERSONAL-only
model operations retain the actual registered library app for attribution,
regardless of UI context.

The HTTP adapter receives a fixed system instruction, model, purpose, bounded
JSON input and pinned limits. It returns bounded `text`, `vector`,
`citation_ids`, `usage` and optional `status`; unknown fields reject. An optional
`X-Request-ID` supports operation attribution. This is a deployment adapter
protocol, not a claim of compatibility with a vendor's API. Qualify its tokenizer,
context, output caps and provider-side pricing agreement before real use. Input token
admission uses a conservative byte bound and a fixed envelope allowance; whole
chunks are omitted rather than truncating tables to fit. No tools, SQL execution,
URL fetching or streaming are exposed.

## Bundled hosted adapter (opt-in)

`compose.model-adapter.yaml` is an optional overlay for the bundled adapter: a
Tier 2 boundary integration that translates the existing `bounded-http-v1`
request into one OpenAI-compatible `POST`, owns no database, holds no application
identity and applies no policy. The Gateway remains the only dispatch authority
and the registry remains the only model-selection authority. Leaving the overlay
out keeps every existing installation on its external-endpoint provider path and
starts no adapter; [`make rag-up`](../../operations/installation.md) is unchanged
and never enables model calls.

The overlay publishes no host port and adds no APISIX route. `cap-ai` and
`cap-ai-worker` reach it at `https://cap-ai-model-adapter:8443` over the private
Compose network and trust it through a CA bundle that also contains the system
roots. Only `HOSTED` `EMBED` and `GENERATE` are served; `VLM`, `RERANKING`,
streaming, tools and local serving remain out of scope.

### Files the owner prepares

Prepare these outside Git, with `umask 077`. Never pass a value as a command
argument, and never render the resolved Compose configuration into a terminal or a
log: `docker compose config` prints environment values.

| File | Contents |
| --- | --- |
| `.env.providers` | Adapter-only upstream vendor keys, one per route `credential_ref`. Loaded only by the adapter |
| `.env.models` | Exactly `MODEL_ADAPTER_TOKEN=<random 32+ bytes>`, the internal Gateway token |
| `model-adapter.json` | The reviewed route declaration |
| `adapter-tls.crt`, `adapter-tls.key` | The adapter's server certificate and private key |
| `adapter-ca-bundle.crt` | System trust roots plus the adapter CA |

```bash
umask 077
{ printf 'MODEL_ADAPTER_TOKEN='; openssl rand -hex 32; } > .env.models
read -rsp 'Embed provider API key: ' KEY; printf 'EMBED_PROVIDER_API_KEY=%s\n' "$KEY" > .env.providers; unset KEY
read -rsp 'Generate provider API key: ' KEY; printf 'GENERATE_PROVIDER_API_KEY=%s\n' "$KEY" >> .env.providers; unset KEY
```

Check the pair before starting the stack. The preflight prints variable names and
static reasons only, never a value:

```bash
python3 scripts/check_model_adapter_env.py \
  --provider-env .env.providers --internal-env .env.models
```

It rejects a provider file that defines `MODEL_ADAPTER_TOKEN` or any reserved
name (`MODEL_ADAPTER_*`, `AI_*`, `AI_MODEL_*`, `RAG_*`, `POSTGRES_*`, `OIDC_*`,
`DATABASE_URL`, `PG*`, `SSL_CERT_FILE`), a repeated name, an internal file that is
not exactly one nonempty `MODEL_ADAPTER_TOKEN`, and a file that grants group or
other access. The adapter loads the provider file first and the internal file
second, so the deployment's own token wins; the preflight is what stops the
collision before Compose silently accepts it.

### Route declaration

Two routes, one per purpose, each with its own immutable provider identity. The
upstream URLs, model identifiers and keys below are placeholders: use the exact
reviewed endpoint, literal model identifier and credential reference. Router
aliases (`openrouter/auto`, `@preset/...`, `:nitro`) are rejected, and an
`openrouter.ai` route additionally requires the reviewed `routing` object.

```json
{
  "schema_version": 1,
  "routes": [
    {
      "protocol_version": 1,
      "type": "openai-compatible",
      "base_url": "https://embed.example.com/v1",
      "credential_ref": "EMBED_PROVIDER_API_KEY",
      "models": [
        {
          "model_id": "example-embed-v1",
          "purpose": "EMBED",
          "dimension": 1536,
          "limits": {"max_input_tokens": 8191, "max_output_tokens": 1, "max_bytes": 65536, "timeout_ms": 30000}
        }
      ]
    },
    {
      "protocol_version": 1,
      "type": "openai-compatible",
      "base_url": "https://generate.example.com/v1",
      "credential_ref": "GENERATE_PROVIDER_API_KEY",
      "models": [
        {
          "model_id": "example-generate-v1",
          "purpose": "GENERATE",
          "limits": {"max_input_tokens": 64000, "max_output_tokens": 2048, "max_bytes": 262144, "timeout_ms": 60000}
        }
      ]
    }
  ]
}
```

A route is immutable configuration. Changing the base URL, the credential
reference, a model, a purpose, a dimension, any limit or a routing policy changes
its digest and therefore its served path, so it needs a new catalog revision and a
new review. Rotating the secret value behind an unchanged reference does not
change the digest.

### Digest and path before the catalog

Derive each route's identity offline, before a catalog exists:

```bash
bin/dxos-model validate --adapter-config model-adapter.json
```

```text
route digest=83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082 path=/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call protocol_version=1 type=openai-compatible base_url=https://embed.example.com/v1 credential_ref=EMBED_PROVIDER_API_KEY
model example-embed-v1 purpose=EMBED dimension=1536 max_input_tokens=8191 max_output_tokens=1 max_bytes=65536 timeout_ms=30000
route digest=7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038 path=/v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call protocol_version=1 type=openai-compatible base_url=https://generate.example.com/v1 credential_ref=GENERATE_PROVIDER_API_KEY
model example-generate-v1 purpose=GENERATE max_input_tokens=64000 max_output_tokens=2048 max_bytes=262144 timeout_ms=60000
```

The digests are path segments, not routing decisions: the adapter serves each
route only at its own `/v1/routes/<digest>/call` path. See the
[CLI reference](../../reference/cli/dxos-model.md#route-only-validation).

### Catalog declaration

Use the derived path in each provider endpoint. Two provider IDs keep the two
routes independent, and the profile revisions carry the exact dimension and
limits from the route:

```json
{
  "schema_version": 2,
  "providers": [
    {
      "id": "example-embed",
      "adapter": "bounded-http-v1",
      "endpoint": "https://cap-ai-model-adapter:8443/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call",
      "credential_ref": "MODEL_ADAPTER_TOKEN"
    },
    {
      "id": "example-generate",
      "adapter": "bounded-http-v1",
      "endpoint": "https://cap-ai-model-adapter:8443/v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call",
      "credential_ref": "MODEL_ADAPTER_TOKEN"
    }
  ],
  "models": [
    {
      "adapter": "bounded-http-v1",
      "profile": {
        "id": "embed-primary",
        "revision": 1,
        "purpose": "EMBED",
        "execution": "HOSTED",
        "provider_id": "example-embed",
        "model_id": "example-embed-v1",
        "credential_ref": "MODEL_ADAPTER_TOKEN",
        "dimension": 1536,
        "limits": {"max_input_tokens": 8191, "max_output_tokens": 1, "max_bytes": 65536, "timeout_ms": 30000},
        "enabled": false
      }
    },
    {
      "adapter": "bounded-http-v1",
      "profile": {
        "id": "generate-primary",
        "revision": 1,
        "purpose": "GENERATE",
        "execution": "HOSTED",
        "provider_id": "example-generate",
        "model_id": "example-generate-v1",
        "credential_ref": "MODEL_ADAPTER_TOKEN",
        "limits": {"max_input_tokens": 64000, "max_output_tokens": 2048, "max_bytes": 262144, "timeout_ms": 60000},
        "enabled": false
      }
    }
  ]
}
```

The provider `credential_ref` is the Gateway's internal adapter token; the route's
own `credential_ref` names the vendor key inside the adapter. Cross-check the
catalog against the routes offline:

```bash
bin/dxos-model validate --catalog model-catalog.json --adapter-config model-adapter.json
```

```text
embed-primary@1 example-embed EMBED fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa /v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call
generate-primary@1 example-generate GENERATE ac8d1b0c1de5dd293603c9a15d83e3f63900c581e78c8753c8abc5bb9e42de7c /v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call
```

The third column is the immutable declaration digest the review must carry. A
candidate that the route does not permit exactly (model, purpose, dimension,
limits no greater than the ceiling) fails here, before any database exists.

### TLS trust

The adapter serves TLS only on `cap-ai-model-adapter:8443`; the certificate SAN
must contain that name. Generate a private CA for this installation and keep it
out of the adapter's own trust: the adapter verifies public vendor endpoints with
the system roots.

```bash
umask 077
openssl ecparam -name prime256v1 -genkey -noout -out adapter-ca.key
openssl req -x509 -new -key adapter-ca.key -sha256 -days 3650 \
  -subj '/CN=OpenDX-OS bundled model adapter CA' -out adapter-ca.crt
openssl ecparam -name prime256v1 -genkey -noout -out adapter-tls.key
openssl req -new -key adapter-tls.key -subj '/CN=cap-ai-model-adapter' -out adapter-tls.csr
printf 'subjectAltName=DNS:cap-ai-model-adapter\nextendedKeyUsage=serverAuth\n' > adapter-tls.ext
openssl x509 -req -in adapter-tls.csr -CA adapter-ca.crt -CAkey adapter-ca.key \
  -CAcreateserial -days 825 -sha256 -extfile adapter-tls.ext -out adapter-tls.crt
cat /etc/ssl/certs/ca-certificates.crt adapter-ca.crt > adapter-ca-bundle.crt
chown 10001:10001 adapter-tls.key adapter-tls.crt model-adapter.json adapter-ca-bundle.crt
chmod 0400 adapter-tls.key adapter-tls.crt model-adapter.json adapter-ca-bundle.crt
```

`capabilities/Dockerfile` runs the service as UID 10001, and Compose mounts a file
secret with the host file's ownership and mode, so the key, certificate and
configuration must be owned by that UID and readable by it alone. The CA bundle is
not secret, but `cap-ai` and `cap-ai-worker` also run as UID 10001 and must be able
to read it: an operator-only `0600` bundle leaves both services unable to verify
the adapter. Never `chmod` the private key to 644 and never add it to Git. The
bundle mounts read-only into
`cap-ai` and `cap-ai-worker` as `SSL_CERT_FILE`; it must keep the system roots
(`cat /etc/ssl/certs/ca-certificates.crt ...`), because removing them would break
every other TLS connection those services make. An empty or adapter-only bundle is
not a supported configuration: verification is never disabled.

Rotation: replace `adapter-tls.crt`/`adapter-tls.key` (or the vendor key value in
`.env.providers`) and recreate only the affected containers with the same Compose
files. Keep `credential_ref` unchanged when only a secret value rotates. Re-review
the route when processing terms, account scope or the endpoint change.

### Compose invocation

Set the five opt-in paths in the protected env file (or a second `--env-file`),
then start the same project with the added overlay:

```bash
docker compose --env-file .env --env-file .env.rag \
  -f compose.yaml -f compose.rag.yaml -f compose.models.yaml \
  -f compose.model-adapter.yaml up -d
```

`RAG_MODEL_ADAPTER_CONFIG_FILE`, `RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE`,
`RAG_MODEL_ADAPTER_TLS_CERT_FILE`, `RAG_MODEL_ADAPTER_TLS_KEY_FILE` and
`RAG_MODEL_ADAPTER_CA_FILE` are required by the overlay: omitting one stops
Compose with a static message naming the missing variable. Validate the same file
list with `docker compose ... config --quiet`, which prints nothing; never inspect
the rendered form, which resolves secrets.

### Database logins and migrations

Apply `0012_personal_admin_authority_locks.sql` before using the CLI for PERSONAL
approval changes. It holds the operator identity and membership rows through
each mutation, serializing concurrent role withdrawal. It uses the existing
non-login owner and narrow row-lock privileges introduced by 0011.

Migration `0011_model_administration.sql` is additive: narrow audited slot
functions plus the `dxos_ai_model_admin` login, no password and no seeded profile,
slot or approval. It follows the existing agent-schema opt-in, so a runner that
stops at `0010` keeps stopping there and no default changes.

Provision the two new deployment logins as the database owner, with passwords read
from the protected environment rather than written in a statement:

```sh
psql -X -v ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_ai_model_catalog','dxos_ai_model_admin'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', name);
    END IF;
    EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT', name);
  END LOOP;
END $$;
\getenv catalog_password MODEL_CATALOG_DATABASE_PASSWORD
\getenv admin_password MODEL_ADMIN_DATABASE_PASSWORD
ALTER ROLE dxos_ai_model_catalog PASSWORD :'catalog_password';
ALTER ROLE dxos_ai_model_admin PASSWORD :'admin_password';
SQL
```

`dxos_ai_model_catalog` executes only the candidate importer; it cannot activate,
change a slot or read protected knowledge. `dxos_ai_model_admin` executes only the
administration functions and validates the exact session plus the current Core
`role_operator`. The approval commands use the existing protected `dxos_authz`
login and its password; no CLI command runs on the owner login.

Apply the schema with all three schema flags, then verify the ledger:

```bash
RAG_MODELS_SCHEMA_ENABLED=true RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true \
RAG_AGENT_MODELS_SCHEMA_ENABLED=true \
  scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope cap-ai --through 0012
```

A retained installation that conflicts with an earlier `cap_ai` checksum must
stop: no ledger edit, checksum bypass or reset command is accepted. Follow the
named owner-approved rebuild in
[installation](../../operations/installation.md#rebuilding-an-existing-model-installation),
which rebuilds a separate database and re-derives model state.

### Verify, review and reconcile

The minimum operator surface is `validate`, `verify`, `status` and
`policy approve|revoke|suspend`; the exact contract is in the
[dxos-model CLI reference](../../reference/cli/dxos-model.md). `validate` is
offline and schema-only: it prints the per-purpose mode and states which routes the
declaration declares (plus what the runtime still needs, such as
`RAG_LOCAL_MODELS_QUALIFIED` for a local endpoint) and never claims the runtime
serves them; `verify` makes exactly one bounded synthetic call per configured
purpose through the same private proxy transport the runtime uses and reports
per-purpose outcomes. Neither approves anything:

```bash
bin/dxos-model validate --config models.yaml
bin/dxos-model verify --config models.yaml --json > verify.json
```

The report carries the purpose, the effective mode and the bounded usage, never
response text, an endpoint or a key. A transport failure after a possible send
exits 6 and is never re-sent: reconcile the state with `status` before another
reviewed attempt. The deployment then records the reviewed selection from the same
declaration through `cap_ai.reconcile_deployment_models`; that reconciliation is
deployment-internal and is not an operator command. A purpose the declaration
omits is withdrawn, and a withdrawn or drifted selection denies every model call
while authorized FTS remains available.

Write the review that binds the probe to one independent human attestation. The
`attestation_ref` is the review record, not a copy of the probe:

```json
{
  "declaration_digest": "fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa",
  "qualification": {"profile_id": "embed-primary", "revision": 1, "declaration_digest": "fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa", "purpose": "EMBED", "passed": true, "dimension": 1536, "citation_ids": [], "checked_at": "2026-11-24T10:00:00Z"},
  "attestation_ref": "review/2026-11-24/model-board/embed-primary"
}
```

Read the deployment state. `status` is read-only, sanitized and unauthenticated by
contract: it resolves no operator identity, needs no token and changes nothing, and
it reads only the sanitized deployment report and index counts through the narrow
catalog login. It reports the selected profile per purpose and the one reason that
blocks it (route absent, installation switch off, not selected, snapshot mismatch,
local route unqualified). A purpose is `qualified` only from the current selection
plus the deployment's own local qualification, so run it with the deployment's
`RAG_LOCAL_MODELS_QUALIFIED` for a local route:

```bash
export AI_MODEL_CATALOG_DATABASE_URL   # the narrow deployment login
bin/dxos-model status --config models.yaml
unset AI_MODEL_CATALOG_DATABASE_URL
```

OIDC identity and operator attribution are mandatory only for the actions that
change state: `policy approve`, `policy revoke` and `policy suspend` each verify one
operator token and re-check that subject's current platform authority in the
database. `status` has no such path and no token input.

The retired candidate-import and slot commands (`import`, `activate`, `deactivate`,
`slots`, `rollback`, `list`, `inspect`) are gone: they are not a model lifecycle
and no runtime consults a slot. The legacy `model_candidates`/`global_model_slots`
tables and the bundled adapter's own catalog remain only as data and adapter
configuration until they are removed together; no runtime path reads them.

### PERSONAL processing approval

Slot activation never approves personal egress. Record the separate audited
PERSONAL approval for each purpose on the protected `dxos_authz` session:

```json
{
  "profile_id": "embed-primary",
  "profile_revision": 1,
  "provider_id": "example-embed",
  "purpose": "EMBED",
  "source_policy_id": "local-only",
  "source_policy_version": 1,
  "source_classification": "PERSONAL",
  "query_app_id": "personal_library",
  "query_classification": "PERSONAL",
  "retention_evidence": "review/2026-11-24/retention",
  "training_use_evidence": "review/2026-11-24/training-use",
  "region_evidence": "review/2026-11-24/region"
}
```

```bash
export AI_MODEL_APPROVAL_DATABASE_URL   # the protected dxos_authz login
bin/dxos-model policy approve --file approval-embed.json --token-file operator.jwt
unset AI_MODEL_APPROVAL_DATABASE_URL
```

`query_app_id` must be the genuine registered app that is currently `Ready` for the
query; confirm the deployed app identifier rather than assuming a directory name,
and use the value configured as `AI_AGENT_PROCESSING_APP_ID` for AGENT queries.
`GENERATE` needs its own review with its own provider, profile revision and
`purpose`. Withdraw one approval with
`bin/dxos-model policy revoke --id <uuid> --reason '<reason>' --token-file
operator.jwt`; the withdrawn revision cannot be reactivated. APP `NON_PERSONAL`
processing keeps its separately reviewed procedure above and is not a PERSONAL
approval.

### Enable, rotate and retire routes

Only after the declaration is verified and the required approvals exist, set
`RAG_MODELS_ENABLED=true` together with the declaration path, the private proxy
origin and its master key (`RAG_MODEL_DEPLOYMENT_FILE`,
`RAG_MODEL_LITELLM_ENDPOINT`, `LITELLM_MASTER_KEY`) for `cap-ai` and the worker and
recreate them. Missing any of them, or a selection the deployment has not
reconciled, keeps inference off; authorized FTS remains available, and the process
still boots.

When a route changes, declare the new route and a new provider ID (one endpoint per
provider ID), verify it, and keep the old and new routes and IDs until every fenced
embedding job and pinned operation has drained or failed closed. Beware the ordering:
once the selection moves, a job queued for the superseded embedding revision can no
longer be claimed, and such jobs stay visible in `status` as pending or failed
counts until the deployment reconciles back to the revision they were queued for —
reconcile back if you need them drained, or accept them as failed and re-ingest.
Retire the old configuration explicitly: remove its route from the declaration,
reconcile, and confirm the old digest no longer resolves. Never reinterpret existing
vectors under a new dimension.

Environment files are not encrypted and are not a boundary against a privileged
host or container operator; they only keep secrets out of Git, images, rendered
Compose output and shell history.

## Retired registry compatibility

`RAG_GLOBAL_MODEL_REGISTRY_ENABLED` is retired: no Go code reads it, and the
[R1 registry contract](../../reference/rag/api.md#global-model-registry-foundation-r1)
is retained only as the historical record of that design. The runtime no longer
has a registry mode, a catalog or DB `ACTIVE` slots; the deployment's reconciled
selection of the exact declared profiles is the only model authority
(ADR 0011). The `model_candidates`, `global_model_slots` and `model_slot_events`
tables, their functions and the bundled adapter's catalog stay as data and adapter
configuration until they are removed together; no runtime path reads them.

Apps and users cannot select models. An unavailable, withdrawn or mismatched
selection preserves authorized FTS and optional search-only answers; required
generation with evidence returns 503 `MODEL_UNAVAILABLE`. VLM is reserved and
unbound, and RERANKING remains disabled.

For new chat, `AGENT` source selection is server-side across all current DB role
memberships and owner-only PERSONAL sources. The turn accepts only a question. Role claims, client lists and LLM app suggestions confer no authority.
Existing Hasura/capability APP routes keep ADR 0007's selected-role behavior.

## Durable operations and token observations

The platform enforces no monetary ceiling. There are no budget accounts,
reservations, allocations, usage ledger or priced profiles, and nothing to
provision before a model call. Spend limits belong to the provider agreement;
the platform's own controls are the resource bounds (`max_input_tokens`,
`max_output_tokens`, `max_bytes`, `timeout_ms`), profile enablement, slot
authority, processing approval and the kill switch.

Every model operation keeps exactly one durable `cap_ai.model_operations` row for
its operation key. Only the model gateway, through
`cap_ai.claim_model_operation`, `cap_ai.begin_model_operation` and
`cap_ai.finish_model_operation`, may create or transition it. The row binds the
actor, attributed app, immutable profile ID/revision, purpose and a fingerprint of
the authority snapshot and payload, and no model response or source text is
stored. Direct table and dispatch-token access is revoked from the API, worker and
catalog roles. Operation keys bind actor, profile, source/policy fingerprint and
payload hash, so a concurrent or replayed call intent cannot send the same
operation twice.

For cross-app AGENT model operations, configure `AI_AGENT_PROCESSING_APP_ID` to a
genuine registered app currently `Ready`; the retired
`AI_AGENT_BILLING_APP_ID` name is not read. Missing or invalid configuration
denies the model call. The app is attribution and query-review scope: it requires
the independent PERSONAL query review described above, but it supplies no budget
and no source authority. Every actual source app and PERSONAL source must pass
its own READ and processing checks. See
[canonical attribution and durable state](../../reference/rag/authorization.md#model-operation-attribution-and-durable-state).
The RAG overlay supplies the configured consumer to cap-ai. The public APISIX
route preserves selected-role behavior for existing knowledge APIs.

Recheck authority after the claim, after persisting intent and immediately before
send. A state that may have been sent (`MAY_HAVE_SENT` or `UNCERTAIN`) is never
resent by the same or a later attempt. An uncertain EMBED job becomes an
ingestion job `FAILED` with `error_code` `MODEL_CALL_UNCERTAIN`. That code is
public, not a private database value: a caller who currently holds READ on the
job's document sees it from `GET /api/cap/ai/ingestions/{id}`
(`{"status":"FAILED","error_code":"MODEL_CALL_UNCERTAIN"}`) because the route
serializes `cap_ai.ingestion_metadata` unchanged, and any other caller keeps the
concealed `404 NOT_FOUND` with no status and no code. The job state `FAILED` is
distinct from the durable operation state `UNCERTAIN`: the job says the embedding
build did not complete, the operation says a call may have been sent and must
never be resent. Operation identity for each embedding chunk stays stable across
lease fences, so a crash after send cannot create an automatic second call. Other
explicitly approved new attempts need their own operation identity.

Token counters are observational. They are recorded, together with an optional
provider request ID and latency, only for a succeeded operation; `UNCERTAIN` and
`FAILED_UNSENT` rows carry no counters. There is no reconciler, invoice inspector
or settlement adapter in this baseline, and no public usage-admin endpoint. No
content READ is conferred by an operation row.

A stuck operation is an owner-run review, not an automatic transition. The
`model_operations_stuck` index on `(state, intent_at)` gives the oldest ambiguous
rows per attributed app; the gateway, API and worker roles cannot read the table,
so the owner runs the query with owner credentials:

```sql
SELECT app_id,
       state,
       count(*) AS operations,
       min(intent_at) AS oldest_intent
FROM cap_ai.model_operations
WHERE state IN ('MAY_HAVE_SENT', 'UNCERTAIN')
GROUP BY app_id, state
ORDER BY oldest_intent;
```

`intent_at` is null only for a `CLAIMED` row, so every listed row has already
committed intent. The oldest rows are resolved by the owner against the provider
agreement; the platform neither resends nor settles them.

## Answer API

`POST /api/cap/ai/answers` accepts `{scope,query,max_citations?,require_generation?}`.
Response: `{status,answer,results,citations,reason?,model_profile?,usage?}`.
`answer` is null for SEARCH_ONLY/NO_EVIDENCE. Status is ANSWERED, SEARCH_ONLY or
NO_EVIDENCE. At most eight whole evidence chunks enter generation. Every supplied
context source is checked after the model returns, including uncited context.
If any becomes unavailable, the response contains no old text, titles, results or
citations and reason `AUTHORIZATION_CHANGED`. Fabricated citation IDs abstain with
`UNSUPPORTED_ANSWER`. ID validation establishes provenance, not semantic truth.

Policy/outage returns scoped search-only unless `require_generation=true`, which
returns 403 POLICY_BLOCKED or 503 MODEL_UNAVAILABLE. A model call that may have
been sent returns `SEARCH_ONLY` with reason `MODEL_CALL_UNCERTAIN` (HTTP 200,
null answer, no citations, no usage), or 503 `MODEL_CALL_UNCERTAIN` with
`require_generation=true`. There is no quota response.
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
readiness, the current personal zero-egress restriction, durable uncertain
operations with bounded invocation counts and answers. Those existing cases do not
prove the amended approval policy.
The Go integration suite separately exercises approved PERSONAL EMBED/GENERATE, denied unapproved
source/query egress, disabled/mismatched approval and retention/training-use
review, per-source multi-role authority, retained owner-only snapshots without
revoked-context reuse, and AGENT operation attribution to the configured
processing app. Earlier R01–R16 source/package gates remain prerequisites. A
deterministic fake gold case
measures transport, citation/provenance plumbing, token observation and latency; it
does not qualify real language-model groundedness, injection robustness,
tokenizer limits or provider invoicing. The fake LOCAL adapter exercises the
shared dispatch boundary only; no LOCAL serving adapter ships. Missing
real-provider/classification approval leaves hosted production enablement
blocked. No overall S01–S08 release claim follows.

`RAG_MODEL_DEPLOYMENT_FILE` today names the YAML deployment declaration described
above; `models.example.yaml` is the reference shape. The retired JSON catalog
below (`schema_version` 2) belongs to the bundled adapter and the retired registry
loader only. No runtime path reads it, and it is retained with the adapter until
that adapter is removed:

```json
{
  "schema_version": 2,
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

A model entry in that retired shape has `adapter: "bounded-http-v1"` and a `profile`
with `id`, `revision`, `purpose`, `execution`, `provider_id`, `model_id`,
`credential_ref`, and resource-only `limits` (`max_input_tokens`,
`max_output_tokens`, `max_bytes`, `timeout_ms`); EMBED also requires `dimension`.
Monetary fields are not part of schema version 2. Follow the linked canonical
contract for validation.
Startup never imports candidates. The runtime verifies the immutable declaration
digest against the provider metadata of the exact declared revision; endpoint or
loopback changes require a new profile revision and provisioning. Fenced embedding
lease profile reads use narrowly granted worker functions. Neither the retired
registry switch nor a slot binding provides any production qualification.

## Explicit real hosted qualification

Run `scripts/test-rag.sh real-models` only in an owner-approved disposable
installation. Setting `RAG_REAL_MODELS_OPT_IN=true` authorizes synthetic hosted
requests, possible provider charges and withdrawal of the supplied GENERATE
processing approval at the end of the test. The runner leaves that approval
disabled. Restore it only through a fresh, independently reviewed approval.

Use the CLI procedure above to validate, verify and reconcile the exact EMBED and
GENERATE routes first. Independently approve PERSONAL content and
queries for both purposes with `policy approve`. Record the returned approval
UUIDs. Both approvals must cover the dedicated library app, the `local-only@1`
source policy, exact profile revisions and their actual processors. Supply the same
opaque `attestation_ref` in the recorded human review and as
`RAG_REAL_MODELS_REVIEW_REF`. A provider behind OpenRouter also needs review
of the pinned underlying processor and routing settings.

Prepare a separate installation whose Ready personal-library app ID begins with
`ragreal`. Give fixture actors A and B its allowed library role and keep the
library empty. Actor A uploads the synthetic document. Actor B must have no
access to A's document. Use running File Service, scanner, parser, cap-ai and
worker processes with their documented runtime identities and TLS trust.

Supply these environment variables through the protected test environment.

| Variables | Required values |
| --- | --- |
| `TEST_DATABASE_URL` | Disposable database name beginning `test_` or ending `_test` |
| `RAG_TEST_AI_API_URL`, `RAG_TEST_FILE_API_URL` | Actual test service origins |
| `RAG_TEST_OIDC_TOKEN_A`, `RAG_TEST_OIDC_TOKEN_B` | Valid, distinct authenticated fixture sessions |
| `RAG_REAL_TEST_APP`, `RAG_REAL_TEST_ROLE` | Dedicated `ragreal` library app and allowed role |
| `RAG_REAL_EMBED_PROFILE`, `RAG_REAL_EMBED_REVISION` | Exact reviewed active EMBEDDING profile |
| `RAG_REAL_GENERATE_PROFILE`, `RAG_REAL_GENERATE_REVISION` | Exact reviewed active LLM profile |
| `RAG_REAL_EMBED_APPROVAL`, `RAG_REAL_GENERATE_APPROVAL` | Actual enabled PERSONAL approval UUIDs |
| `RAG_REAL_MODELS_REVIEW_REF` | Opaque shared slot review reference |
| `RAG_REAL_MODEL_CLI` | Path to the built `dxos-model` executable |
| `RAG_REAL_ADMIN_TOKEN_FILE` | Owner-only operator access-token file accepted by the CLI |
| `AI_MODEL_APPROVAL_DATABASE_URL` | Protected `dxos_authz` connection to the same host, port and disposable database as `TEST_DATABASE_URL` |
| `CLAMAV_ADDRESS`, `RAG_INTERNAL_AI_URL` | Running scanner and worker's internal AI endpoint |
| `RAG_PARSER_QUALIFIED`, `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` | Qualification gates for the actual ingestion deployment |

Preflight requires `OIDC_ISSUER_URL`, `OIDC_AUDIENCE` and `OIDC_JWKS_URL`,
an executable CLI and an owner-readable token file with no group or other access.
Connection-target overrides in either DSN or `PGHOSTADDR`, `PGSERVICE` and
`PGSERVICEFILE` are refused before hosted work. Keep
vendor keys in the adapter's secret file. The runner uses service tokens and the
protected approval session, and consumes safe model metadata from PostgreSQL.

```sh
RAG_REAL_MODELS_OPT_IN=true scripts/test-rag.sh real-models
```

The gate uploads fresh Markdown through File Service, waits for scanner/parser
processing and a complete matching vector generation, then requires a document
that lexical search missed to appear in hybrid retrieval. It requires a grounded
`BLUE-17` answer with an accessible chunk citation and successful EMBED/GENERATE
operation evidence. It checks the denied fixture actor and withdraws the
GENERATE approval before checking that no further generation was dispatched.
The final output contains safe document, run, generation and profile identities.
Capture it in a local qualification report under `.scratch/` together with the
tested commit, command, result and reviewed routing evidence.

Missing prerequisites exit 2 with `NOT_RUN`. A transport or provider failure
exits 1 and is never retried automatically. A failed run can leave the synthetic
document and approval withdrawal in place. Inspect the safe operation state and
prepare another isolated empty fixture before a new qualification attempt.

A second live embedding revision remains a separate owner-run qualification.
Activate its independently reviewed candidate and approval, keep both adapter
routes available, allow worker polling to build the new complete generation and
verify its exact profile revision and dimension before claiming a live switch.
Record this criterion as `NOT_RUN` when a second reviewed revision is unavailable.
The deterministic revision-isolation tests cover the code regression. Record
only provider/model combinations that actually passed the live gate.
