# Native RAG repository baseline

Inspected 2026-11-13: branch `feat/cap-ai`, HEAD `70271c335e58714301d502f0547d9eb76c8b05e8`. Working tree initially clean for tracked files; planning artifacts in ignored `docs/superpowers/` already existed. This is local source evidence, not a remote ref assertion or runtime test.

## Producer/consumer inventory

| Producer / exact source | Current observation | Consumers / required alignment |
| --- | --- | --- |
| `platform/postgres/init/01_core_schema.sql` | One organization, `core.users`, `core.files` with required app ID; file maximum 20971520 bytes, PDF/PNG/JPEG only | File API/scanner/OCR, claims, Hasura, fixtures; Markdown runtime upload requires a deliberate MIME-contract extension |
| `platform/postgres/init/03_app_registry.sql` | `core.apps`, `core.roles`, `core.user_roles`; role ownership by app/capability | Auth/registry/Operator must converge from workspace-era types |
| `platform/postgres/init/06_runtime_roles.sql` | No cap_ai service/worker roles or grants; broad existing process/reader grants do not authorize new AI access | P2 must add narrow grants, including denial of unrelated capability data |
| `capabilities/internal/files/{service,access}.go` | Queries `core.file_objects` by workspace; owner or readable attachment branch | New file schema and generic personal-owner policy; original download must not bypass document policy |
| `capabilities/internal/files/scanner.go` and scanner path in `service.go` | Existing clean/quarantine/hash and scan retry behavior | Preserve immutable identity and fail-closed scan semantics across migration |
| `capabilities/internal/auth/{auth,middleware,jwt}.go` | Verified-context identity contains WorkspaceID; selected role checked against JWT AllowedRoles; JWKS cache is not membership revocation | P1 removes obsolete workspace dependency and intersects authoritative current DB membership |
| `capabilities/internal/ai/ai.go`, `cmd/cap-ai/main.go` | Mock OCR on legacy files; unauthenticated local Markdown keyword RAG | Replace or retire legacy access; no native ingestion/ACL enforcement currently present |
| `platform/postgres/capabilities/cap_ai.sql` | Registers capability only | P2 capability-owned schema/migration/grants |
| `operator/internal/manifest/{types,parser,validate}.go` | No Knowledge field | Planner/apply/reconcile/CRD conversion and stored-spec serialization require coordinated extension |
| `operator/internal/{planner,apply,reconcile,crdconv}` | Existing app lifecycle and workspace publication logic | P4 source snapshots, durable registration and independent indexing; never model calls in apply |
| `workspace-ui/src/{App.tsx,types/registry.ts}` | Registry/context consumers | P8 app/personal knowledge views, stale-request discard, no key entry |
| `apps/procurement/manifest.yaml`, `apps/procurement/knowledge/` | Workspace-era example, local knowledge files | Package source declarations use canonical nested shape only after parser upgrade |
| `tests/fixtures/`, `tests/integration/`, capability/operator integration tests | Existing workspace/session/file fixtures | Migrate fixtures with contracts; static tests do not prove new DB permissions |
| Accepted ADR 0001/0003 and data/security contracts | Still workspace-based | ADR 0005 must formally reconcile target model and identify superseded clauses before migrations |

## Decision provenance

User-supplied issue #9 reserves ADR 0005 for one organization per installation: independent app/department identifiers, departments in cap_hr, work items in cap_work, no Tier 1 references to Tier 2/3. Issue #10 reserves ADR 0006 for complex business logic placement; no option is selected here. Neither ADR file exists at inspected HEAD. Do not reinterpret SQL drift as accepted supersession.

[ADR 0007](../adr/0007-native-rag-identity-and-source-boundaries.md) records RAG choices. [Preflight](../development/rag-security-preflight.md) records exposure limits. [Historical package](../design/native-rag/2026-11-13/STATUS.md) retains original evidence and contradictions with documented reference-file suffixes.

## Subsequent decision integration

ADR 0005 was committed as `5569f3f` and merged locally into feat/cap-ai at `a0f7781`; [PR #59](https://github.com/nguyentin05/opendx-os/pull/59) targets feat/db-org-model. It is now the accepted organization/ownership decision. The original source observations above remain historical; runtime consumers have not been migrated. The user selected DB-authoritative business roles (Keycloak identity only), so Keycloak-to-DB business-role synchronization is not a target requirement.
