# Automatic workflow integration evidence

Scope: Task 6, disposable `opendx-integration`, PostgreSQL `opendx_integration`,
actual Kestra 0.20.7 and cap-process, integration/backend-platform based on
`45c103a`. This evidence establishes selected S03 and workflow authorization /
recovery behavior, not complete release acceptance. Setup and explicit private
signed-token renewal are documented in [integration setup](integration-setup.md).

**Automatic effects are qualified for alpha only**, for both procurement and
process_fixture. The alpha workflow subject has no beta workflow authority;
beta routes are deliberately absent. Beta events remain pending/retryable rather
than being dropped or marked delivered. Beta automatic availability is NOT_READY.
The separate `dxos_kestra` database/runtime role uses PostgreSQL repository and
queue; its role has no superuser, CREATEDB, CREATEROLE, replication or BYPASSRLS.

| Check | Result | Actual evidence |
| --- | --- | --- |
| Real API auth | PASS | `/api/v1/flows/search`: unauthenticated 401, legacy Bearer 401, private Basic 200 |
| Both app commands to declared workflows | PASS | Fresh events, actual Kestra SUCCESS executions and create_review tasks, declared work-item fields/source workspace, one persisted result and delivered state |
| Duplicate delivery and effect replay | PASS | Actual repeated webhook executions and signed effect calls return one work identity/result |
| Publisher crash and lease retry | PASS | Actual Kestra pause, SIGKILL/start cap-process, persistent event identity/body, attempt 1 to 2, one effect after lease expiration |
| Renewal and persistent Kestra recreation | PASS | Fresh Keycloak token delivered to actual Kestra secret environment; recreated service retains existing execution IDs; fresh process_fixture command completes |
| Wrong principal and workspace | PASS | Distinct signed workflow-role subject gets 403/no work; configured alpha principal against beta source event gets 403/no work; beta event remains pending |
| Full six-case suite | PASS | `TEST_DISPOSABLE_DATABASE=yes python3 -m unittest tests.integration.workflow_cases -v`: 6 tests, 92.365s, exit 0 |
| Go integration/race | PASS | `cd capabilities && go test -race -tags=integration ./internal/events ./internal/process -count=1` with separate module-test DSN: events 4.183s/process 8.401s, exit 0 |
| Available repository suites | PASS | `make test`: module suites and 24 Python checks pass, exit 0; UI SKIPPED because dependencies absent |

Recovery event `396b2710-795e-4049-81f7-48bc0a1a8c87`, 2026-11-07 UTC:

| Time | Action/state |
| --- | --- |
| 10:22:08.837 | Pause real Kestra, preventing acknowledgment/effect completion |
| 10:22:09.556 | Observe persisted in_flight attempt 1; lease expires 10:23:09.240 |
| 10:22:09.882 | SIGKILL actual cap-process |
| 10:22:10.114 | Restore real Kestra |
| 10:22:11.415 | Start actual cap-process, preserving databases and volumes |
| 10:23:11.554 | Delivered attempt 2, unchanged event ID/body, one effect/result |

The test bounds ordinary effect/execution waits to 75 seconds and lease recovery
to 100 seconds. Private per-action state/body is retained in ignored mode-0600
`build/integration/workflow-recovery.json`. Fresh automatic-phase data is separate
from consumed manual-phase assertions; no manual event-count fixture qualifies
this automatic evidence. Cleanup observes the actual paused state before unpause.

Initial RED exposed absent workflow routing/effects. Owner regressions cover stale
routes, ambiguous subscriptions and transport scheme/header validation. Live
startup also exposed JDBC placeholder parsing and private route-file ownership;
these were repaired before the successful automatic run. The first complete run
found a terminal-status polling race and double-unpause cleanup error; both were
corrected and all six cases rerun successfully.

The version-pinned [Kestra 0.20.7 deployment example](https://github.com/kestra-io/kestra/blob/v0.20.7/docker-compose.yml)
supplies the supported PostgreSQL repository/queue settings. Kestra's
[secrets documentation](https://kestra.io/docs/concepts/secret) describes the
base64 `SECRET_` environment mechanism, verified here through actual tasks and
recreation. No plugin syntax changes were needed: the existing declared HTTP and
Webhook plugin classes imported and executed on the pinned real service.
