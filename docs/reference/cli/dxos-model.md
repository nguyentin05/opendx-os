---
title: dxos-model CLI
description: Commands for validating, importing, verifying and administering model catalog revisions.
diataxis: reference
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-24
---

# dxos-model CLI

```text
dxos-model <command> [options]
```

`dxos-model` is an out-of-band deployment tool for the model catalog. It is not a
service, not an Operator extension and not on any end-user request path: an
operator runs it explicitly in a protected administrative environment. It reads
files, stdin and environment variables only, and it never opens a public route.

The full deployment procedure is in
[model answers](../../guides/knowledge/models.md). This page documents the command
contract.

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
| `validate --catalog FILE [--adapter-config FILE] [--adapter-origin URL]` | Offline validation of a catalog, the registry declaration digests and the route binding. No database, network or secret |
| `validate --adapter-config FILE` | Offline route-only mode. No catalog is read; see [route-only validation](#route-only-validation) |
| `import --catalog FILE` | Declare the catalog's disabled candidates through the catalog login. Never activates a candidate |
| `verify --catalog FILE --profile ID --revision N` | One bounded synthetic call to the reviewed adapter route for the named candidate |
| `list [--after-id ID --after-revision N] [--limit N]` | One bounded page (1–100, default 50) of registry metadata |
| `inspect --profile ID --revision N` | One exact profile revision |
| `slots` | The four global slots with their revision, binding and usability |
| `activate --slot EMBEDDING\|LLM --profile ID --revision N --expected-slot-revision R --review FILE [--rollback]` | One audited slot transition for an independently reviewed candidate |
| `deactivate --slot EMBEDDING\|LLM --expected-slot-revision R --reason TEXT` | Unbind a slot and record the reason |
| `policy approve --file FILE` | One audited PERSONAL processing approval on the protected approval session |
| `policy revoke --approval UUID --reason TEXT` | Withdraw one PERSONAL processing approval |

Every command accepts `--json` and produces one stable JSON document instead of
human-readable lines. `--help` exits 0.

### Route-only validation

`validate --adapter-config FILE` without `--catalog` derives each configured
route's identity from the reviewed adapter configuration. It prints, per route,
the digest, the served path `/v1/routes/<digest>/call`, and each served model rule
with its purpose, exact embedding dimension and reviewed limits. Limits and
dimensions come from the route declaration, never from a provider response.

```text
route digest=83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082 path=/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call protocol_version=1 type=openai-compatible base_url=https://embed.example.com/v1 credential_ref=EMBED_PROVIDER_API_KEY
model example-embed-v1 purpose=EMBED dimension=1536 max_input_tokens=8191 max_output_tokens=1 max_bytes=65536 timeout_ms=30000
```

This is how an operator obtains the endpoint path before a catalog exists. The
digest and path are computed by the same canonicalization the adapter uses, so a
hand-derived value cannot disagree with the served route. `credential_ref` is the
environment reference name, never a secret value. The mode reads no catalog,
opens no database and makes no network call.

With `--json`, the same information is an array of objects:

```json
[
  {
    "digest": "83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082",
    "path": "/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call",
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
  }
]
```

Supplying both `--catalog` and `--adapter-config` keeps the catalog cross-check:
every candidate on the bundled adapter origin must match its configured route
exactly, and each output line gains the route path. Supplying neither flag is an
argument error (exit 2).

## Protected token input

`list`, `inspect`, `slots`, `activate`, `deactivate`, `policy approve` and
`policy revoke` act for one authenticated operator:

| Input | Contract |
| --- | --- |
| `--token-file FILE` | Owner-readable regular file, mode `0400`/`0600` (no group or other access), at most 16 KiB |
| `--token-stdin` | The same bound, read from standard input |

Exactly one input is required. There is no flag that accepts a token value, and
no `--actor`: the actor is the verified subject of the token, never a
caller-supplied identifier. The token is verified before any database call
against `OIDC_ISSUER_URL`, `OIDC_JWKS_URL` and `OIDC_AUDIENCE` (the platform's
existing issuer, key set and audience). It is never printed, logged or persisted.

A token obtained through the existing platform login flow is supplied to the
command; there is no password grant, interactive prompt or token storage in the
CLI.

## Environment

| Variable | Used by | Purpose |
| --- | --- | --- |
| `AI_MODEL_CATALOG_DATABASE_URL` | `import` | Restricted catalog login `dxos_ai_model_catalog`; the only DSN import reads |
| `AI_MODEL_ADMIN_DATABASE_URL` | `list`, `inspect`, `slots`, `activate`, `deactivate` | Restricted administration login `dxos_ai_model_admin` |
| `AI_MODEL_APPROVAL_DATABASE_URL` | `policy approve`, `policy revoke` | The protected `dxos_authz` login, the only session the PERSONAL approval functions execute for |
| `OIDC_ISSUER_URL`, `OIDC_JWKS_URL`, `OIDC_AUDIENCE` | every management command | Token verification; all three are required |
| `MODEL_ADAPTER_TOKEN`, and any route credential reference | `verify` | The Gateway's adapter token is the candidate's `credential_ref`; no vendor key is read |

A command opens exactly the one database its own command needs and never falls
back to another login. DSNs must be URL-form PostgreSQL URLs (`postgres://` or
`postgresql://`); a keyword/value DSN is rejected before anything opens.

## Exit codes

| Code | Meaning | Labels |
| --- | --- | --- |
| 0 | Success | — |
| 2 | Invalid arguments, configuration or catalog | `MODEL_CLI_ARGUMENTS`, `MODEL_CATALOG_INVALID`, `MODEL_ADMIN_INVALID` |
| 3 | Denied | `MODEL_CATALOG_DENIED`, `MODEL_ADMIN_DENIED` |
| 4 | Conflict (stale slot revision, immutable declaration) | `MODEL_CATALOG_CONFLICT`, `MODEL_ADMIN_CONFLICT` |
| 5 | Unavailable dependency | `MODEL_UNAVAILABLE` |
| 6 | A mutation or call may have been sent | `MODEL_CALL_UNCERTAIN`, `MODEL_ADMIN_UNCERTAIN` |

Only the stable label is printed; an error never repeats a DSN, a token, a path, a
statement or an upstream body. Code 6 means the outcome is unknown: reconcile it
from `slots`, `inspect` or the approval record before running another reviewed
action. No command retries automatically.

## JSON output

| Command | Shape |
| --- | --- |
| `validate` | Array of `{profile_id, revision, provider_id, purpose, digest, external?}`, or the route-only array shown above |
| `import` | `{created, unchanged}` |
| `verify` | `{profile_id, revision, declaration_digest, purpose, passed, dimension?, citation_ids, usage?, checked_at}` — never response text, endpoint or key |
| `list`, `inspect` | Array of bounded profile metadata: `{profile_id, revision, purpose, execution, provider_id, model_id, dimension, enabled, adapter_key, declaration_digest}` |
| `slots` | Array of `{capability, slot_revision, profile_id, profile_revision, enabled}` |
| `activate`, `deactivate` | `{capability, profile_id, profile_revision, slot_revision, action, event_id}` |
| `policy approve` | `{approval}` |
| `policy revoke` | `{approval, changed}` |

## Review files

`activate` requires a review document with exactly these fields:

```json
{
  "declaration_digest": "<64 lowercase hex characters>",
  "qualification": {"profile_id": "...", "revision": 1, "declaration_digest": "...", "purpose": "...", "passed": true, "dimension": 1536, "citation_ids": ["E1"], "usage": {"input_tokens": 12, "output_tokens": 6}, "checked_at": "2026-11-24T10:00:00Z"},
  "attestation_ref": "<opaque human review reference>"
}
```

The qualification object is the JSON report `verify` printed, bound to the same
profile, revision, purpose and digest. `attestation_ref` is the independent human
review record; it is not derived from the probe, and a passing probe never
approves anything by itself. `policy approve` requires exactly the approved
PERSONAL review fields defined in
[model-processing policy](../rag/authorization.md#model-processing-policy).

A review file is at most 64 KiB, a single JSON object, with no duplicate,
case-aliased, unknown or trailing content. The database function re-validates
every value and the current platform authority; the CLI's check is a rejection of
documents that cannot be the expected one, not a substitute for that check.

## Security boundary

- The CLI is a trusted deployment client, not a remote access security boundary.
  Possession of an administration DSN is deployment infrastructure privilege: an
  operator who can connect can also read what that login can read. Protect the
  host, the token files and the environment files accordingly.
- A successful mutation requires the current Core `role_operator` of an ACTIVE
  user, checked inside the same transaction that writes the slot and its audit
  event. The database verifies the exact session (`session_user` and the role
  setting) and rejects a privileged or substituted session.
- `import` cannot promote. `activate` and `deactivate` cannot approve processing,
  and the approval commands cannot change a slot. `policy approve` and
  `policy revoke` refuse to run on any login other than `dxos_authz`.
- `verify` sends one fixed synthetic request to the reviewed adapter endpoint
  named by the catalog. It does not qualify a provider, activate a slot, set a
  qualification flag or grant processing permission.
- No command adds a public health, control or mutation route, and no credential
  moves from a protected file into an argument list, a rendered Compose document
  or a shell history.
