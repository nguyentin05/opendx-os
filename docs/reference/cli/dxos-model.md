---
title: dxos-model CLI
description: Commands for validating, verifying and reporting a model deployment, and for administering processing approvals.
diataxis: reference
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-25
---

# dxos-model CLI

```text
dxos-model <command> [options]
```

`dxos-model` is the minimal deployment tool for a model-enabled OpenDX-OS
installation. It is not a service, not an Operator extension and not on any
end-user request path: an operator runs it explicitly in a protected
administrative environment. It reads files, stdin and environment variables only,
and it never opens a public route.

It has four operator surfaces and no model lifecycle: it never selects, activates,
rolls back or inspects a model at runtime. The reviewed deployment declaration and
the exact selection the deployment reconciled from it are the only model
authority ([ADR 0011](../../adr/0011-deployment-selected-litellm-models.md)). The
deployment procedure is in [model answers](../../guides/knowledge/models.md).

## Build

```bash
(cd capabilities && go build -o ../bin/dxos-model ./cmd/dxos-model)
```

The binary is a local build artifact: do not commit `bin/dxos-model`, bake it into
a published image or copy it into an application package. Packaging that ships it
must follow [source hygiene](../../contribute/source-hygiene.md).

## Commands

| Command | Purpose |
| --- | --- |
| `validate --config FILE [--json]` | Offline. Loads the declaration, proves the proxy configuration renders from it, and reports each purpose's mode and qualification. No database, network or secret |
| `verify --config FILE [--json]` | One bounded synthetic call per configured purpose through the same private proxy transport the runtime uses. Reports per-purpose outcomes; approves nothing |
| `status --config FILE [--json]` | Read-only and sanitized. Per purpose: the declared identity, the effective mode, the durable selection and the one reason that blocks it, plus the embedding index counts |
| `policy approve --file FILE (--token-file FILE \| --token-stdin)` | Records one audited PERSONAL processing approval |
| `policy revoke --id UUID --reason TEXT (--token-file FILE \| --token-stdin)` | Withdraws one approval |
| `policy suspend (--token-file FILE \| --token-stdin)` | Withdraws every live model purpose in one audited transaction |

`--config` defaults to `models.yaml`. Every command accepts `--json` and then
prints one stable JSON document instead of human-readable lines. `--help` exits 0.

The retired administrative surface — `import`, `activate`, `deactivate`, `slots`,
`rollback`, `list`, `inspect` — is not part of this command set. It is rejected as
an argument error and is not documented as reachable.

### Deployment-internal rendering and reconciliation

`deployment render --config FILE --out FILE` and `deployment reconcile --config
FILE` are the deployment's own start-up steps, not an operator model lifecycle:
they render the proxy configuration from the same declaration and record the exact
selection through `cap_ai.reconcile_deployment_models`. They are deliberately
absent from the usage text, and a purpose the declaration omits is withdrawn.

## The declaration

The declaration is at most 64 KiB of YAML with at most one embedding and one
generation route. [`models.example.yaml`](../../../models.example.yaml) is the
reference shape. `validate` prints, per purpose, the effective mode (`DISABLED`,
`LOCAL_ONLY` or `HOSTED_ALLOWED`), whether the declaration is qualified, and the
fixed identity (`profile`, `revision`, `provider`, `model`, optional `dimension`,
`alias`). It opens no database and makes no network call:

```text
EMBED LOCAL_ONLY qualified profile=embed-primary revision=1 provider=ollama model=ollama/embed dimension=1536 alias=embed
GENERATE LOCAL_ONLY qualified profile=generate-primary revision=1 provider=ollama model=ollama/llama alias=generate
```

A declaration the runtime cannot accept — invalid content, an unrenderable route,
an unqualified local route — exits 2 and names no path or value.

## status

`status` merges the declared identity with the durable selection and the index
counts, on the restricted deployment login. It prints no endpoint, credential,
token, document, app or content. A purpose is reported `selected`, or `blocked`
with exactly one reason: `route absent`, `installation switch off`, `not
selected`, `suspended` or `snapshot mismatch`.

## Protected token input

`policy approve`, `policy revoke` and `policy suspend` act for one authenticated
operator:

| Input | Contract |
| --- | --- |
| `--token-file FILE` | Owner-readable regular file, mode `0400`/`0600` (no group or other access), at most 16 KiB |
| `--token-stdin` | The same bound, read from standard input |

Exactly one input is required. There is no flag that accepts a token value, and
no `--actor`: the actor is the verified subject of the token, never a
caller-supplied identifier. The token is verified before any database call against
`OIDC_ISSUER_URL`, `OIDC_JWKS_URL` and `OIDC_AUDIENCE` (the platform's existing
issuer, key set and audience), and the database re-checks that subject's current
platform authority inside the audited statement. The token is never printed,
logged or persisted.

A token obtained through the existing platform login flow is supplied to the
command; there is no password grant, interactive prompt or token storage in the
CLI.

## Environment

| Variable | Used by | Purpose |
| --- | --- | --- |
| `AI_MODEL_CATALOG_DATABASE_URL` | `status`, `deployment reconcile` | Restricted deployment login `dxos_ai_model_catalog`; the only session that may read the deployment report or change the selection |
| `AI_MODEL_APPROVAL_DATABASE_URL` | `policy approve`, `policy revoke` | The protected `dxos_authz` login, the only session the PERSONAL approval functions execute for |
| `OIDC_ISSUER_URL`, `OIDC_JWKS_URL`, `OIDC_AUDIENCE` | every policy command | Token verification; all three are required |
| `RAG_MODEL_LITELLM_ENDPOINT`, `LITELLM_MASTER_KEY` | `verify` | The private proxy origin and the one credential the runtime and the probe share. No vendor key is read |

A command opens exactly the one database its own command needs and never falls
back to another login. DSNs must be URL-form PostgreSQL URLs (`postgres://` or
`postgresql://`); a keyword/value DSN is rejected before anything opens.

## Exit codes

| Code | Meaning | Labels |
| --- | --- | --- |
| 0 | Success | — |
| 2 | Invalid arguments, configuration or declaration | `MODEL_CLI_ARGUMENTS`, `MODEL_DEPLOYMENT_INVALID` |
| 3 | Denied | `MODEL_ADMIN_DENIED` |
| 4 | Conflict (immutable revision or concurrent selection change) | `MODEL_ADMIN_CONFLICT` |
| 5 | Unavailable dependency | `MODEL_UNAVAILABLE` |
| 6 | A call may have been sent | `MODEL_CALL_UNCERTAIN` |

Only the stable label is printed; an error never repeats a DSN, a token, a path, a
statement or an upstream body. Code 6 means the outcome is unknown: reconcile it
from `status` or the approval record before running another reviewed action. No
command retries automatically.

## JSON output

| Command | Shape |
| --- | --- |
| `validate`, `status` | `{enabled, purposes: [{purpose, mode, profile_id?, revision?, execution?, provider_id?, model_id?, dimension?, alias?, limits?, qualified, blocked?}], index?}` |
| `verify` | Array of `{profile_id, revision, purpose, passed, reason?, dimension?, citation_ids?, usage?, checked_at}`, never response text, endpoint or key |
| `policy approve` | `{approval}` |
| `policy revoke` | `{approval, changed}` |
| `policy suspend` | `{suspended, purposes, actor_id, occurred_at}` |

## Review files

`policy approve` requires exactly the approved PERSONAL processing fields defined
in [model-processing policy](../rag/authorization.md#model-processing-policy):

```json
{
  "profile_id": "generate-primary",
  "profile_revision": 1,
  "provider_id": "litellm",
  "purpose": "GENERATE",
  "source_policy_id": "local-only",
  "source_policy_version": 1,
  "source_classification": "PERSONAL",
  "query_app_id": "personal_library",
  "query_classification": "PERSONAL",
  "retention_evidence": "review/2026-11-25/retention",
  "training_use_evidence": "review/2026-11-25/training",
  "region_evidence": "review/2026-11-25/region"
}
```

A review file is at most 64 KiB, a single JSON object, with no duplicate,
case-aliased, unknown or trailing content. The database function re-validates
every value and the current platform authority; the CLI's check is a rejection of
documents that cannot be the expected one, not a substitute for that check. A
review records processing evidence only: it cannot enable, change or select a model
route, and no command fabricates a review on the operator's behalf.

## Security boundary

- The CLI is a trusted deployment client, not a remote access security boundary.
  Possession of a deployment DSN is deployment infrastructure privilege: an
  operator who can connect can also read what that login can read. Protect the
  host, the token files and the environment files accordingly.
- `policy suspend` withdraws every live purpose in one transaction on the
  deployment login, as the current operator. The database authenticates the exact
  session, the ACTIVE user and its current `role_operator` grant inside the same
  statement, and rejects a privileged or substituted session.
- `policy approve` and `policy revoke` refuse to run on any login other than
  `dxos_authz`; they cannot change a model route, and no other command can approve
  processing.
- `verify` sends one fixed synthetic request per configured purpose to the private
  proxy. It does not qualify a provider, select a profile, set a qualification flag
  or grant processing permission, and its input is shared with the provisioning
  probe so the two cannot drift.
- No command adds a public health, control or mutation route, and no credential
  moves from a protected file into an argument list, a rendered Compose document
  or a shell history.
