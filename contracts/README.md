# Proposed C0 backend contracts

Status: **PROPOSED handoff**, pending B review and joint consumer approval. These
artifacts do not mark C0/G3 frozen, validate the existing Operator, or prove real
identity, SQL, Hasura, Kestra, storage, scanner or S01–S08 behavior. Existing apps
and runtime scaffolds are not migrated by this handoff. UI implementation is deferred.

The rationale remains in [data contracts](../docs/contracts/data-contracts.md).
`runtime-v1.openapi.yaml` defines the process HTTP boundary;
`events-v1.schema.json` defines the process event envelope;
`manifest-v1.schema.json` proposes the supported core manifest serialization for
B's validator. `fixtures/procurement.yaml` is a proposal, not the deployed app.
`fixtures/registry-ready.json` uses the exact SQL column names, with unchanged
lowerCamelCase manifest JSON. Its duplicate state/UI/capability snapshots must
match `manifest_spec`. Only a Ready active snapshot is consumed.

## Mandatory semantic validation before side effects

JSON Schema supplies structural constraints; it cannot establish declared-column
references, type-compatible comparisons, role grants or workspace integrity. B's
validator and A's strict runtime decoder must enforce the following before use:

- Reject unknown fields/operators/features, including `semanticType`, old
  `initialState`/`transitions`, arbitrary SQL and undeclared capability semantics.
  Identifiers follow `[a-z][a-z0-9_]*`; domain names have exactly `domain.` prefix.
  No interpolation of unvalidated identifiers or expression strings into SQL.
- System columns are id/workspace_id/work_item_id/status/version/updated_at;
  domain declarations cannot redefine them. Status/version/identity/timestamps
  are never client insert/update fields. `work_item_id` may be an explicit insert
  field with a same-workspace FK. Preset columns cannot also be writable columns.
  Procurement requester_id is preset from signed user identity and immutable.
- Every referenced column, UI field, attachment, role and command must exist.
  Reject duplicate names; validate initial/from/to/editable/terminal state
  consistency. UI creates no permission; file fields require explicit attachment
  declarations. Command payload is empty in this core proposal; no undeclared
  arbitrary business-field mutation is implied.
- `recordFilter` has only `_and`, `_or`, and declared scalar columns compared by
  `_eq`/`_neq`. A filter object conjoins its entries; `_and`/`_or` contain nonempty
  filter arrays. Values are type-compatible literals or exactly
  `X-Hasura-User-Id` / `X-Hasura-Workspace-Id` session values. No relationship
  traversal, `_not`, raw SQL, column-to-column expressions or unknown session
  names. Null `_eq` means IS NULL, null `_neq` means IS NOT NULL; ordinary SQL NULL
  comparisons do not match. Both compiler and runtime evaluator reject unsupported
  forms rather than ignoring them. Always conjoin signed workspace isolation.
- Supported CHECK strings are only the three anchored forms in the schema:
  `length(trim(column)) > 0`, `column > 0`, or
  `column > 0 AND column <= 24`. Both references in the last form must be the
  same declared numeric column; text checks require a text column. No general SQL.
- Composite FKs have exactly `[workspace_id, foreign_column]` referencing
  `[workspace_id, id]` on declared supported shared tables; nullable foreign IDs
  do not make workspace nullable. Require these FKs for work_item_id, requester_id
  and quote_file_id. Shared access columns must exist and be explicitly readable
  for each listed role; this grants no writes or implicit relationships.
- Workflows subscribe only to command.committed in core; effects use declared
  commands, unique step names and literal title/description/priority. Optional
  assignedEmployeeId is a UUID resolved in the event's workspace, never a
  caller-provided assignment. A declared effect cannot exceed the service grant.

## Identity and protocol notes

`identity-claims.json` is decoded illustrative claim data, **not a signed token**.
Its dates are fixed test data. A1 must create signed fixtures and test issuer,
audience, expiry, signature and required claims; do not accept this JSON as
credentials. Require UUID signed user/workspace IDs and nonempty allowed roles;
default and selected roles belong to that set. In this core fixture `sub` equals
the signed user ID; different mappings need explicit acceptance. B seeds employee
IDs consistently; no automatic employee onboarding is provided. `X-Hasura-Role`
selects an already signed grant; raw workspace/user headers never create authority.
Workflow effect calls additionally require the configured service identity.

Command receipt identity includes workspace/actor/app/entity/command/key. The
fingerprint is SHA-256 of canonical JSON `{entity_id,expected_version,payload}`
(object keys sorted recursively; UUID canonical lowercase; integer version).
Scope already includes route app/command. Current authorization is checked before
returning a stored result. Same request/key returns the stored body; different
fingerprint returns 409. Reusing the original request preserves retry semantics.
`command-request.json` and `command-committed.json` are request/body examples;
use an `Idempotency-Key` header with both attempts. Errors always use the OpenAPI
envelope; avoid leaking out-of-scope state, versions or file status.

`event-retry.json` intentionally equals `event-command-committed.json`: transport
retries preserve ID and payload. DB `outbox_events.id` maps to wire `event_id`.
The event is persisted with the command transaction. Delivery does not grant
permission; effects resolve source from the persisted event and current deployed
step mapping, not from a caller-supplied body. Workflow source association is
stored in the processed-step row described in the [SQL handoff](../docs/handoff/backend-sql-handoff.md).

## Fixtures and checks

`fixtures/negative-cases.json` records design inputs and expected future rejection;
these are not executed runtime PASS claims. Contract tests verify sample agreement
and API field ownership, without adding dependencies or claiming full JSON Schema
validation. Run `python3 -m unittest discover -s tests -p test_machine_contracts.py -v`.
Parse JSON with `python3 -m json.tool FILE` and YAML with installed PyYAML.
A standards-compliant JSON Schema/OpenAPI validator remains a B handoff prerequisite;
none is installed in the current Python environment. Schema shape alone does not
validate a manifest's semantic safety.

Runtime private configuration: DATABASE_URL, OIDC_ISSUER_URL, OIDC_JWKS_URL,
OIDC_AUDIENCE, KESTRA_URL, S3_ENDPOINT, S3_BUCKET, CLAMAV_ADDRESS. Real DB tests
require TEST_DATABASE_URL and integration build tags. UI receives only public
issuer/client/callback and gateway configuration. Never expose service credentials.
