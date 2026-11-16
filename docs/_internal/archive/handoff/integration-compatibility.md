# Backend/platform integration compatibility

Branch: `integration/backend-platform`. Runtime artifact:
`703741f3d9e14990127a231e2e4561b67d417b5b`; verification-only follow-up:
`5a03cf6` (positive browser assertion only). See [final evidence](integration-evidence.md)
for full revisions, fresh results and remaining blockers. Status: **partial handoff**;
known-app APIs and dynamic frontend discovery are separate surfaces.

Source pins: main `3b81e3c0e17e249e4e78b2cbef628b6d544cf854`, platform
`70c29046cfd723d63be36860b9eecce8308f6864`, backend
`d4cc99978725d809b4c1503e0090c6fa55c32654`. Platform merge `1dfa3f7` preceded
backend merge `0a5e3c9`; source branches/main/remotes were preserved. Both source
branches had identical `contracts/` trees; implementation corrections below do
not redefine the normative [data](../contracts/data-contracts.md) or
[security](../contracts/security-and-acceptance.md) contracts.

Every Gate 0 inventory surface has a current disposition below; closely related
rows are combined. The historical baseline's “pending” wording is superseded by
these revision-specific entries; this table does not claim release acceptance.

| Surface | Producer | Consumer | Discrepancy and resolution | Owner | Verification / disposition |
| --- | --- | --- | --- | --- | --- |
| Active versus attempt registry | SQL/Operator | Backend registry | Separate attempt state; transactional Ready publication retained | Operator/backend | Actual two packages publish in both workspaces; failed update preserves active state; closed for supported set |
| Command receipts/outbox leases | SQL/process | Publisher/retries | Required receipt scope, fingerprint and durable leases implemented | Platform/backend | Manual retries/conflicts/concurrency plus automatic real crash/lease redelivery; closed for scoped assertions |
| Workflow step result and FKs | SQL/process | Kestra effects | Composite identity/result fields implemented | Platform/backend | Duplicate real deliveries/effects persist one work item/result; closed for alpha |
| File identity and retries | SQL/files | Scanner/download/submit | Immutable identity, hash, retry state implemented | Platform/backend | Real clean/infected/quarantine/replacement/recovery; app-retention FK policy remains separate design, not a newly imposed gate |
| Version/audit context | SQL/triggers | Hasura/process | Trusted context now checked by session_user; one generated bump/audit | Platform/backend | CRUD increments once, stale/concurrent losers leave no extra audit/event; closed for selected scope |
| Runtime DB privileges | Compose/runtime roles | Hasura/process | Bootstrap DSN defaults replaced; separate process/Hasura/reader/caller/module roles | Platform/backend | Real privilege/impersonation/CLEAN denial; closed in disposable environment |
| Actor provisioning | Keycloak fixture provisioning | JWT/Hasura/process | Exact signed subjects/employee/workspace/roles provisioned reproducibly | Platform/backend | Real expiry/renewal/forgery/grant checks; closed for fixture actors |
| Workflow principal | Keycloak/config | Effect authorization | role_workflow and configured alpha subject aligned | Platform/backend | Wrong subject/workspace denied; alpha closed, beta automatic NOT_READY |
| Manifest parser/packages | Strict Operator | App packages | procurement and process_fixture supported; maintenance/timesheet relationship fields unsupported | App/Operator | Actual supported-set apply/reconcile; legacy apps deferred, business behavior preserved |
| Generated SQL/Hasura | Operator planner | Authenticated CRUD/commands | Desired-set apply and restricted generated grants repaired | Operator/backend | Real presets/protected fields/nested/upsert/relationships/composite FKs; closed for supported apps |
| Workflow endpoint/auth | Compose/deployer | Publisher/Kestra | Actual declared namespaces/webhooks deployed; key auth distinct from Basic admin auth | Platform/backend | Real webhook positive/rejected-key controls; closed for declared alpha flows |
| Workflow destination routing | Ready manifest route map | Publisher | Workspace/app/version/workflow destination selected, no silent global fallback | Backend/platform | Both real alpha app effects and Go outbound Basic/destination/missing-route retry; beta deliberately lacks route |
| Kestra persistence | PostgreSQL repository/queue | Restarted workflows | Dedicated dxos_kestra owner/database; persistent queue replaces memory | Platform | Real publisher crash and token reload retain executions; closed for local runtime, backup/restore deferred |
| Storage/scanner setup | SeaweedFS/ClamAV | Real S3/scanner adapters | Private bucket/SigV4, explicit scan limits, readiness and durable retry configured | Platform/backend | Full lifecycle and actual 20 MiB/EICAR adapters; closed for scoped local setup |
| Service probes | Shell/Python probes | Operators | Require exact expected auth failures and real ClamAV PONG; healthz remains liveness | Platform/backend | Controlled status regressions plus actual services; closed; no healthz readiness overclaim |
| Safe environment/seed | Guarded helpers | Live phase runners | Project/database/volume checks and authenticated fresh API/scanner data | Integrator | Fresh disposable reset; phase isolation; Kestra delayed until dedicated DB provisioned; closed for disposable scope |
| Historical handoff wording | Operational docs | Frontend/integrators | Superseded blanket pending claims linked to final evidence | Integrator | Current guide/evidence supersede historical source-branch qualification |
| Frontend registry discovery | Operator/Hasura/API surface | Dynamic App Shell | No authenticated registry endpoint; core_app_registry absent from query_root | Platform/Operator + App Shell | **Blocking discovery gap**: actual browser and live role query fail; agree filtered interface and qualify before frontend-ready |
| Browser gateway access | APISIX CORS | Browser localhost:15173 | Explicit allowlist added for auth/role/idempotency preflights | Platform | Actual Chromium business query/process response/clean download; direct process remains CLI/service only |

Automatic availability is alpha-only for both supported apps. Beta CRUD/commands
and pending-event retention do not qualify beta automatic effects. Per-workspace
service credential delivery is required before adding beta routes. Local effect
token renewal is explicit operator work, not a daemon.

No multi-destination fan-out contract was added. Existing-database migration,
retention policy changes, external audit anchoring, release artifacts and
clean-machine/backup qualification require separate scope. Data/AI success,
S02/S07/S08 and frontend feature implementation/build remain deferred. The
[frontend guide](frontend-integration.md) supplies known-app payloads and the
concrete discovery blocker without exposing administration credentials.
