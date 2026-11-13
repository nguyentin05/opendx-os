# Data contracts, permissions, and manifests

This document defines the intended data and manifest contract shared by the
Operator, capabilities, UI, and application packages. The implementation is
partial; existing Go types and sample YAML are not evidence that every requirement
below is enforced. See [architecture and decision changes](../architecture/README.md#decisions-and-contract-changes)
and [security acceptance criteria](security-and-acceptance.md).

## 1. Identity, application and department boundaries

[ADR 0005](../adr/0005-department-and-application-data-model.md) supersedes the workspace model. One installation serves one organization; no workspace claim or row column is part of the target contract. `app_id` identifies application provenance; `org_unit_id` identifies a department in `cap_hr`. They are independent dimensions, not substitutes for one another.

Keycloak verifies identity. An ACTIVE `core.users` row and current `core.user_roles` determine business membership. Requested roles are selectors checked against DB membership, ownership and authorized app/capability scope; JWT business-role claims do not grant permission. New DB grants must work without refreshing token business-role lists once the target cutover is complete. Current JWT-only middleware is partial implementation, not the target authority.

Application roles declare own-record, own-department or organization scope. Missing membership or an unavailable authority denies access. Department-dependent access requires enabled `cap_hr` and authoritative staff membership; organization scope still respects app/resource/column permissions and PRIVATE document ownership. Core never gains a foreign key to a capability/domain table to implement these scopes.

DB role administration is authorized and audited; its transaction commit is the membership-change authority. Hasura, service APIs, original-file routes and registry context must converge on this authority. Token verification alone does not establish current business access. Hasura session delivery and subscription revocation require their own verified mechanism before P1 is considered complete.

Relationships and nested/upsert writes must preserve app, owner and department constraints; an installation-wide UUID reference alone is insufficient authorization. A service with higher DB privileges enforces the same resource predicates. Do not mechanically replace every legacy composite workspace FK with app_id: shared masters and app-produced records have different ownership.

## 2. Kernel and optional capability ownership

The following is the target logical inventory, not a claim that every runtime consumer uses these tables. IDs are UUID except stable app/capability/role identifiers and audit sequence identity. Exact migration mappings must be explicit and preserve retained data.

| Owner | Data / constraints |
| --- | --- |
| `core.users` | Universal identity, Keycloak subject, ACTIVE/disabled status; no department foreign key |
| `core.roles`, `core.user_roles` | Platform/app/capability role ownership and current membership; not per-document ACL |
| `core.apps`, `core.capabilities`, `core.app_capabilities` | Kernel registry identities and declared dependencies, independent of app domain schemas |
| `core.files` | app_id, immutable original identity/object key/checksum, uploaded_by, MIME/size, quarantine/scan result; real app registry reference required |
| `core.audit_logs`, `core.outbox_events` | App/source/actor provenance, append-only audit and durable retryable events; no dependency on capability tables |
| `cap_hr` | Shared organization departments/staff/positions; master data has no app_id |
| `cap_work` | App-origin work items and their provenance; business lifecycle remains in the owning app domain record |
| `cap_process` | Command receipts and processed workflow effects; atomic idempotency/concurrency |
| `cap_data` | Optional physical master data, OT and export/lineage; distinguish shared master identity from app-produced records |
| `cap_ai` | Proposed document collections/ACL/versions/jobs/chunks/model usage, defined by the [RAG contract](rag-api.proposed.md); not currently implemented storage |

Applications use only declared, available capabilities. Capability data may reference Core identifiers; Core cannot depend on a capability or application table/service. References from `core.files` to `core.apps` are kernel registry references, not dependencies on app-owned domain tables. Disabling a capability or unpublishing an app does not authorize dropping retained data.

ADR 0002's domain-attribute ownership remains applicable: app business attributes belong in domain tables, and a shared identity does not grant unrelated relationship traversal. Its older employee/workspace example is superseded by ADR 0005.

## 3. Domain records and state

Domain records retain independent app-owned lifecycle, version, server timestamps and authoritative creator. Work-item linkage, if declared, points to `cap_work`; department linkage, if declared, points to `cap_hr`. System-generated ownership and app/department constraints must be validated on insert and remain immutable except through an explicitly designed authorized operation. Exact manifest fields/presets are coordinated implementation work; old workspace-bearing sample manifests are legacy fixtures until migrated.

Initial state comes from `stateMachine.initial`, version starts at 1, and the server sets time. If the initial state is already submitted, create must enforce its corresponding business conditions atomically. Adding an app does not add a hardcoded app branch in shared runtime code.

## 4. Write matrix and field ownership

| Operation | Caller may supply | Server responsibility |
| --- | --- | --- |
| Create | Allowlisted business columns | derive actor/app scope/initial state/version/time; validate department/related-source access |
| Edit | Allowlisted columns in editable states | current DB role/resource predicate, one version increment, atomic audit |
| Transition | declared command and expected_version | validate authority, state/version, sources and effects in one transaction |
| Upsert/nested | denied unless explicitly designed | enforce both create/edit constraints; cannot reset owner/scope/state/version |
| Delete | denied by default | separate retention/audit policy and authorized scope |

Clients cannot set ownership, scan status, hashes, version or privileged state through arbitrary insert/update fields. `core.files` scan metadata belongs to File Service/scanner. Hasura table/row/column/relationship permission is required in addition to SQL grants, and its session role must originate from current DB authority.

## Legacy consumer transition for sections 5–9

The invariants below concerning atomicity, idempotency, immutable originals, provenance and declarative interfaces remain required. Literal `workspace_id`, old `core.*` capability table names and registry keys describe legacy consumers only and are superseded by ADR 0005 and sections 1–4 above. They are not permission to add workspace fields to new APIs. Before changing the affected module, record its exact app/source/actor key and migration mapping in its implementation plan; do not infer an OT dedup key, audit-chain partition or BI lineage key by replacing workspace text mechanically. This documentation change does not migrate those unrelated consumers or claim their new serialization is settled.

## 5. Version, command, audit, and retry

**Chosen mechanism:** a `BEFORE UPDATE` trigger on the domain table sets `NEW.version = OLD.version + 1` and `updated_at` for every successful UPDATE. The client and the Command API do not increment the version themselves; the Operator generates the trigger together with the table. A retry that has been recognized does not perform another UPDATE.

A command compares `id`, workspace, permissions, state, and `expected_version` in the same transaction using a row lock or an equivalent conditional UPDATE. If the record is valid within the caller's scope but its version has changed, return `409`; do not write state, audit, or a business event for the failed attempt.

Example: the approver reads `version=1`; the requester edits `quoted_amount` through CRUD, making the record `version=2`; an approval command carrying `expected_version=1` must be rejected. When two commands with the same version run concurrently, at most one state transition succeeds.

Each command carries an idempotency key. The lock scope includes workspace, actor, app, entity, and command; the server stores the payload fingerprint and result in the same transaction. Retrying the same key/payload returns the recorded result after checking current permissions, before re-evaluating the old version; the same key with a different payload returns `409`. The receipt schema and its consumers must implement this full scope; storing the result only in memory is insufficient.

Write the change, audit, and durable event in the same transaction. Use Hasura Event Triggers or an equivalently durable outbox to deliver to Kestra; do not rely on an HTTP call to Kestra after commit as the sole delivery mechanism. The workflow deduplicates each effect by event identity and step; a retry after a crash does not create duplicate business effects.

Audit serializes per workspace before reading the chain head and computing the hash; `pg_advisory_xact_lock(hashtext(workspace_id::text))` may be used within the transaction. Checkpoints are signed and stored outside PostgreSQL in OpenBao. Integrity is proven up to the anchored checkpoint; do not claim that the unanchored tail resists history rewriting by a DB superuser.

## 6. Files, OT, and BI

New files start as `QUARANTINED`; only the scanning service moves them to `CLEAN` or `INFECTED`. Download, OCR, and commands that use a file must check permission/workspace and `CLEAN` status. Scanned content must not be overwritten under the same file identity; replacing a file creates a new identity and requires a rescan. A scanner error keeps the file unclean.

OT validates the device/workspace, stores a unique event per the key in section 2, and emits the standard event `ot.event.accepted`. The app's declared workflow evaluates thresholds and creates the appropriate work. The capability does not pre-select the maintenance app; duplicate delivery adds no further business effects.

The full BI source key is `(workspace_id, entity_type, entity_id, entity_version)`. Export only records in the app-declared `terminalStates`, and read only successfully published batches. Re-exporting the same source version adds no contribution; choose the latest version by `(workspace_id, entity_type, entity_id)`. Two entity types with the same UUID remain two distinct sources. Two exports with the same version key but different content must be detected as a reconciliation error rather than arbitrarily choosing one.

Lineage must preserve the full source record set behind each aggregate metric, not just one representative ID. Check the registry, batch, receipt, and lineage DDL against these requirements before connecting consumers; the presence of a table alone does not prove compliance.

## 7. `DXApplication` manifest contract

This is the intended logical shape. Before extending parsers or app packages,
agree on a versioned serialization, validator, and sample that consumers all use.
The existing parser and app examples are partial implementations; differences
from this table must be resolved explicitly rather than guessed independently.

| Field | Meaning and required data |
| --- | --- |
| `kind` | `DXApplication` |
| `metadata` | `name`, `displayName`, `version` of the app package |
| `spec.role` | The app's primary business role; other roles must be declared in the corresponding permissions |
| `spec.sharedAccess` | Declared kernel/capability master reads, columns, roles and relationships; no implicit writes or capability access. Existing core-only serialization needs coordinated extension |
| `spec.domainTable` | Table name, columns/data types, nullability, CHECK constraints, declared ownership/relationship constraints under ADR 0005; the system columns in section 3 are managed by the Operator |
| `spec.domainAccess` | Allowlist of roles/columns for select, insert, and update; permission conditions on records. Deny undeclared operations by default |
| `spec.stateMachine` | `initial`, `editableStates`, `terminalStates` eligible for export, and commands with `from`, `to`, `allowedRoles`; states not in `editableStates` disallow content-editing CRUD |
| `spec.attachments` | File FK columns to check on download/OCR/command; do not infer them from domain column names |
| `spec.workflows` | Flows with an identity, a YAML file in the package, and subscribed events; `ot.event.accepted` or `command.committed` carries workspace/app/entity/version and an event ID as applicable |
| `spec.capabilities` | Capabilities required for the app/features to operate |
| `spec.uiSchema` | `listColumns`, `formFields`, views/actions referencing declared fields and commands; creates no additional permissions |

A workflow is registered only in a deployed workspace/app and runs under a least-privilege service identity. Event content does not grant permissions beyond the workspace. Table names, columns, relationships, and declared expressions must pass validator checks before DDL/metadata generation.

### Implementation gaps

The current Go types use `stateMachine.initialState` and `transitions`; the logical
contract above also needs editable/terminal states and command validation.
Attachment-column bindings, constraint/FK declarations, per-role access rules, and
workflow field names need one serialization shared by the parser, sample apps,
validators, services, and UI. Resolve compatibility and migrations for existing
data/manifests when changing that serialization. An example without matching
validation remains illustrative.

## 8. Semantic concept ownership

[ADR 0002](../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md#3-three-scenario-governance-for-domain-attributes-tier-3)
defines three cases: independent domain attributes, an explicitly shared concept
identified by a `semanticType` URI, and conflicting declarations rejected during
planning/reconcile. A shared concept has one authoritative Write Owner and
Multi-Reader replicas; owner updates produce transactional outbox events for
workflow fan-out. Matching column names alone never establishes shared meaning.

Incompatible types or multiple Write Owners for the same concept must fail before
configuration is published. The manifest serialization, ownership registry,
replica delivery contract, and enforcing validator still need to be specified and
implemented; the current parser does not define `semanticType` fields. This section
records the accepted requirement without claiming that synchronization works.

## 9. Capability interfaces

| Capability | Intended interface |
| --- | --- |
| `cap-process` | `POST /api/cap/process/files/upload`, `POST /api/cap/process/commands/{app_id}/{command}` |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export`, `GET /api/cap/data/lakehouse/analytics` |
| `cap-ai` | Legacy `POST /api/cap/ai/extract`, `POST /api/cap/ai/rag`; proposed secure APIs in [RAG contract](rag-api.proposed.md). Legacy RAG is not an authorized fallback |

Inspect actual handlers and agree on request/response schemas and service
identity before connecting callers. This logical route inventory includes work
still to implement; it is not an assertion that every endpoint is running.

### Current workflow deployment adapter

The process publisher supports a legacy full `KESTRA_URL` with Bearer
`KESTRA_API_TOKEN`, or deployment-owned single-destination routing via
`KESTRA_ROUTES_FILE`. The private JSON route array binds `workspace_id`, `app_id`,
package `version`, `workflow_id`, and a complete `endpoint`. Deployment derives
namespace/flow identity from the manifest-referenced workflow YAML and supplies
the private webhook key; event callers cannot supply a destination. Before each
send, the publisher requires a validated Ready app snapshot and exactly one
workflow subscribed to that event type. Missing, stale, or ambiguous routing
retains the event for retry. Multiple subscribed destinations require a separately
designed delivery contract; they are not silently reduced to one.

`KESTRA_AUTH_SCHEME` explicitly selects `Bearer` (legacy default) or `Basic`;
`KESTRA_API_TOKEN` is the credential following that scheme. Transport credentials
are separate from the signed effect service token. Routes/auth are loaded on
process startup; deployment changes require process recreation. The disposable
integration helper provides that lifecycle and renews private Kestra secrets.
