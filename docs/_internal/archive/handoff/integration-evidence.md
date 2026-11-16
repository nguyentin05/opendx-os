# Final integration evidence

Status: **partial handoff, not frontend-ready**. Authenticated registry discovery
is unavailable: actual role-scoped GraphQL returns HTTP200 `validation-failed`,
`core_app_registry` absent from query_root. Owner: Platform/Operator and App Shell;
next action: agree/implement a workspace/role-filtered discovery interface and
qualify it. Known-app API success does not close this blocker. Beta automatic
workflows remain NOT_READY pending per-workspace service identity/credential
provisioning. UI feature implementation/build is outside this gate.

Runtime artifact: `703741f3d9e14990127a231e2e4561b67d417b5b` on
`integration/backend-platform`. Verification artifact: `5a03cf676bac8bb5cd0bb3546ca8ac97e9f7549c`; `git diff 703741f..5a03cf6 --stat` changes only the positive
browser assertion in `scripts/integration/browser_smoke.py` (3 additions/1 deletion).
Fresh reset/roles ran on703741f and remain exact runtime evidence; rows naming
703741f ran on that runtime, and every later live row was re-executed on the
unchanged runtime at5a03cf6 during this final Task 8 qualification. No Task 1–7
PASS is inherited, and no row reuses an earlier revision's result. The
evidence/documentation commit is separate and changes no runtime artifact.

Environment E: 2026-11-07, Linux/amd64, disposable Compose project
`opendx-integration`, DB `opendx_integration`; Python3.14.8, Go1.27.3,
Compose5.6.1, Node26.12.0/npm11.20.0, Chromium155.0.8059.84.
Images: PostgreSQL17-alpine, Keycloak26.0, Hasura2.45.1, APISIX3.10.0-debian,
Kestra0.20.7, SeaweedFS3.80, ClamAV1.4; process built from runtime artifact.
Root cwd R: `/home/ximofam/.codex/worktrees/34a2/opendx-os`.
Private evidence L: `build/integration/task8/` under R (ignored mode0600 logs);
no credentials or raw environment dumps are committed. Separate module DSN uses
`dxos_module_test` on `opendx_module_test` and creates/drops random test databases.
Live assertions use `dxos_reader`; runtime uses `dxos_process`/`dxos_hasura`;
bootstrap is restricted to disposable initialization/Operator/master fixtures.

| check/assertion | scope | command/action | cwd | revision | environment/versions | status | sanitized evidence | gap/owner |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Fresh disposable recreation | Project/database/volume guards; persistent Kestra startup order | `TEST_DISPOSABLE_DATABASE=yes TEST_ALLOW_RESET=opendx-integration python3 scripts/integration/environment.py reset` | R |703741f |E |PASS |59.66s, L/reset.log; only initial four infra services start |Clean-machine/production migration outside scope |
| Runtime roles | Dedicated runtime/assertion/module credentials | `TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/environment.py roles` |R |703741f |E |PASS |1.43s, L/roles.log; no secret output |None in local scope |
| Identity provisioning/runtime start | Actual Keycloak fixture + restricted runtime | `identity.py provision`; `identity.py tokens`; documented Compose `up -d --no-deps --build hasura cap-process apisix` |R |703741f runtime /5a03cf6 verification |E |PASS |L/provision.log, tokens.log, runtime-up.log; bounded real readiness waits |healthz alone remains liveness |
| Identity boundary | Signed claims, expiry/renewal, role/header spoofing, restricted DB/CLEAN denial, JWKS | `python3 -m unittest tests.integration.identity_cases -v` |R |703741f runtime /5a03cf6 verification |E |PASS |9tests5.381s; L/identity.log; expiry finally asserts restoration204 |Fixture actor scope |
| Actual package registry/CRUD | Both apps/both workspaces, Ready/dependencies, failed update preservation, presets/protected/nested/upsert/FKs | `python3 -m unittest tests.integration.registry_crud_cases -v` |R |703741f runtime /5a03cf6 verification |E |PASS |5tests18.426s; L/registry-crud.log; actual supported-package publication |Frontend registry discovery separate FAIL below |
| Manual commands | S01/S03/S04/S06 selected assertions; effects/scanner frozen | Fresh `seed.py --phase manual`, tokens, context-loaded `python3 -m unittest tests.integration.acceptance_cases -v` |R |5a03cf6 |E |PASS |6tests0.721s; L/acceptance_cases.log; real Kestra stopped |Does not qualify automatic effects |
| Concurrency | Equal-version race, canonical retry/conflict, current authorization and rollback counts | Separate fresh manual seed, context-loaded `python3 -m unittest tests.integration.command_concurrency_cases -v` |R |5a03cf6 |E |PASS |4tests0.693s; L/command_concurrency_cases.log |None in selected scope |
| Independent apps | process_fixture transition preserves procurement with Operator stopped | Separate fresh manual seed, context-loaded `python3 -m unittest tests.integration.runtime_independence_cases -v` |R |5a03cf6 |E |PASS |1test0.182s; L/runtime_independence_cases.log |Only two supported apps |
| Hygiene | Metadata/headers/tier boundaries/manifests | `make check-hygiene` |R |5a03cf6 |E |PASS |L/hygiene.log; exit0 |Static evidence |
| Python discovery | Static/contracts/helper/probe regressions | `python3 -m unittest discover -s tests -v` |R |5a03cf6 |E |PASS |40tests10.749s; L/python.log |Explicit live modules separately executed |
| Operator unit | Actual Go module runner | `go test ./... -count=1` |R/operator |5a03cf6 |E |PASS |L/operator-unit.log; exit0 |None |
| Capability unit | Actual Go module runner | `go test ./... -count=1` |R/capabilities |5a03cf6 |E |PASS |L/capabilities-unit.log; exit0 |None |
| Operator race/integration | Real separate disposable module DBs | `go test -race -tags=integration ./... -count=1` |R/operator |5a03cf6 |E |PASS |L/operator-race-integration.log; exit0 |Module tests do not substitute live services |
| Capability race/integration | Real module DBs, outbound Basic/destination/missing-route retry and process/files | `go test -race -tags=integration ./... -count=1` |R/capabilities |5a03cf6 |E |PASS |L/capabilities-race-integration.log; exit0 |Controlled HTTP adapters distinguished from Kestra |
| Automatic workflows | Both actual alpha app declarations/effects, duplicate delivery, crash/lease retry, principal/workspace denial and renewal | Fresh `workflows renew`, `seed.py --phase automatic`, `python3 -m unittest tests.integration.workflow_cases -v` |R |5a03cf6 |E |PASS |6tests100.415s; L/workflows.log, workflow-renew.log, seed-automatic.log; actual lease recovery |Beta automatic NOT_READY |
| File lifecycle/recovery | Expanded9cases, actual scanner/storage/process/IP faults, missing attachment | Fresh `seed.py --phase files`; `python3 -m unittest tests.integration.file_cases -v` |R |5a03cf6 |E |PASS |9tests146.921s; L/files.log; first attempt one transient20s submit timeout, isolated rerun1.776s, failure preserved L/files-first-attempt-fail.log |No inherited expanded-suite PASS |
| Real file adapters | SeaweedFS SigV4 + actual clean/20MiB/EICAR ClamAV | `python3 -m scripts.integration.supporting_services file-adapters` |R (child capabilities) |5a03cf6 |E |PASS |`TestRealSeaweedFSClamAV`2.189s; L/file-adapters.log; separate S3 credentials only |No S3 secret exposed to browser |
| Actual webhook boundary/probe | Missing/wrong key rejection and positive execution/effect; strict S3/Kestra/ClamAV probes | Final `workflows renew`; `python3 -m unittest tests.integration.supporting_service_cases -v`; `python3 -m scripts.integration.supporting_services probe` |R |5a03cf6 |E |PASS |1test9.070s; L/supporting_service_cases.log; probe0.603s L/supporting_services_probe.log; rejected keys404/no execution, correct key200/deduplicated effect |Admin Basic differs from webhook key auth |
| Browser known-app API | Actual origin15173, auth/role/key preflight, GraphQL and CLEAN bytes | `python3 -m scripts.integration.browser_smoke` |R |5a03cf6 |E |PASS |2.081s; L/browser-smoke.log; gateway/direct business200 data; registry200 validation-failed; gateway process404 readable; direct process TypeError; CLEAN200 46B `matches_fixture:true` |Direct process remains CLI/service only |
| Registry discovery | Authenticated frontend registry query | Actual Chromium `query { core_app_registry { app_id version status manifest_spec } }` via gateway/direct Hasura |R |5a03cf6 (also703741f) |E |FAIL |HTTP200 validation-failed: field absent from query_root; no admin credential exposed |Blocking discovery interface; Platform/Operator + App Shell |
| UI tests/build | UI feature implementation |Not executed |R/workspace-ui |5a03cf6 |E |NOT_APPLICABLE |Explicitly outside gate; existing UI scaffolding incompatible |App Shell owner |
| S02/S07/S08 /release | OT/export/lineage/full release/install/backup |Not executed |R |5a03cf6 |E |NOT_APPLICABLE |Deferred per approved handoff scope |Separate implementation/release work |

File-suite honesty note: the first final-phase attempt ended with one transient
20-second submit timeout in `test_missing_attachment_blocks_submit`. The failing
first-attempt log is preserved as L/files-first-attempt-fail.log. An isolated
rerun passed in1.776s, and the complete 9-case module then passed 9/146.921s in
L/files.log; the PASS row above reports the full passing rerun, not the failure.

## Assertion mapping

- S01: real role/workspace select, relationships, CRUD presets/protected fields,
  nested/upsert/composite FKs, forbidden command/file contexts: identity,
  registry_crud_cases, acceptance and file cases. Passing supported-app assertions
  does not establish a missing registry discovery surface.
- S03: canonical same-key/body retry; changed-body conflict; real duplicate effect,
  webhook and publisher crash/lease retry; one work identity/result.
- S04: CRUD version increments once; stale command409; concurrent same-version
  winners/losers200/409; failed attempts leave no extra audit/event/receipt.
- S05: process_fixture and procurement retain independent state while Operator is
  absent; real alpha automatic effects for both apps are separately tested.
- S06: quarantine/infected download/submit blocked, missing attachment422,
  ordinary caller cannot set CLEAN, app/workspace isolation, immutable identity,
  fresh replacement/rescan and durable scanner/storage/process recovery. OCR
  remains unavailable/deferred, so no OCR-success qualification is implied.
- S02/S07/S08 are deferred, not waived or satisfied for a later release.

Exact setup, private variable names and sequential fresh-context loading are in
[integration setup](integration-setup.md); API payloads/error/key/version behavior
and missing status/discovery surfaces are in [frontend guide](frontend-integration.md).
Historical Task1–7 documents remain attributed to their original revisions.
