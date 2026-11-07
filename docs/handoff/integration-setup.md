# Disposable integration environment

This setup isolates the backend/platform handoff in Compose project
`opendx-integration` and database `opendx_integration`. It is not release
qualification. See [verification](../development/verification.md) for evidence
semantics and [compatibility](integration-compatibility.md) for unresolved gates.

Prerequisites: Python 3, Docker Engine and Compose supporting `!override` (2.24.4
or newer), local loopback ports 15432/18085/18082 and sufficient resources for
PostgreSQL, Keycloak, SeaweedFS, ClamAV and Kestra. Image tags remain those in
`compose.yaml`; cached images do not justify substituting versions.

Run from the repository root:

```sh
export TEST_DISPOSABLE_DATABASE=yes
python3 scripts/integration/environment.py inspect
python3 scripts/integration/environment.py up
python3 scripts/integration/environment.py down
TEST_ALLOW_RESET=opendx-integration python3 scripts/integration/environment.py reset
```

The first invocation generates ignored `.env.integration` (0600) and private
`build/integration/s3.json`. Do not print rendered Compose configuration or commit
these files. The inspect output is a sanitized target summary, not a readiness
result. `up` starts infrastructure only with a bounded 180-second Compose wait
(and 600-second command ceiling); services without healthchecks are merely
started. `down` retains state. `reset` checks effective project/database,
container service labels, mounted volumes and volume ownership before removing
only this project's declared volumes and recreating infrastructure. It verifies
the actual database name when existing containers are present. It refuses a
stopped/unreachable PostgreSQL rather than weakening the guard. Keycloak, object
store, Kestra, scanner database and PostgreSQL persistence are all project scoped.
Never use these commands against a shared database or reuse this project name for
another stack. No broad prune or shared-row reset is used.

Configuration validation (no secret output):

```sh
docker compose --env-file .env.integration -p opendx-integration \
  -f compose.yaml -f compose.integration.yaml config --quiet
python3 -m unittest tests.harness.test_integration_environment -v
```

All published integration ports bind loopback; APISIX's base fixed 9092 mapping
is removed. Runtime database URLs use `integration_runtime`, not the privileged
`integration_bootstrap` account. The runtime role/grants must be provisioned by
the identity/publication work before starting Hasura or cap-process. Do not start
the entire base Compose stack with its bootstrap defaults. PostgreSQL bootstrap
credentials are private and solely for isolated initialization/operator setup.

Identity and actual supported-package publication are now wired through the
[identity/runtime setup](identity-runtime.md) and the phase workflow below.
Automatic workflow routing and full file lifecycle/recovery remain separate gates.

## Actual package publication and phase data

Task 4 replaces the pending phase boundary described above. After role and
identity provisioning, start restricted Hasura/process and the gateway, then run:

```sh
python3 -m unittest tests.integration.registry_crud_cases -v
TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/seed.py --phase manual
TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/seed.py --phase files
```

The registry suite provisions the actor fixture's shared workspaces/employees,
validates the two actual package paths, applies them in both workspaces, and
reconciles a temporary directory containing only those supported packages.
Single-package apply retains other active packages. Explicit directory reconcile
still removes omitted packages and their managed grants; do not pass all `apps/`
while maintenance/timesheet remain unsupported. Registry publication/CRUD success
is limited to these two packages.

Seeding requires actual Ready snapshots and enabled dependencies in both
workspaces. Each invocation creates fresh UUIDs through authenticated Hasura,
uploads real PDFs to the private bucket, waits for actual ClamAV CLEAN results,
and uses Command API transitions to establish submitted/self-approval/stale
cases. Only shared master/work-item setup uses bootstrap; business records and
state changes use fixture identities and restricted runtime services. Scanner
status is never written by seed SQL.

Manual/files phases stop the whole Kestra server before creating records, so its
workers and queued tasks cannot execute effects. After observing clean fixtures,
they stop ClamAV and upload a further file that remains QUARANTINED for negative
checks. `scanner_stopped` in the private context records this operational state.
The process service remains running and persists scan retries. Resume the real
scanner for Task 7 with:

```sh
docker compose --env-file .env.integration -p opendx-integration \
  -f compose.yaml -f compose.integration.yaml start clamav
```

Do not restart ClamAV before consuming a frozen manual quarantine case. A new
seed phase resumes it to build its own clean fixtures, so consume phases
sequentially. Task 6 replaces the original memory repository/queue with PostgreSQL.
A service restart alone is not workflow recovery qualification; the automatic
phase below verifies persisted lease redelivery and effect deduplication.

`build/integration/{manual,files}.json` are ignored mode-0600 files containing
`TEST_CASES_JSON`, `TEST_INDEPENDENCE_JSON`, URLs, read-only assertion DSN, actual
record IDs/versions and file IDs. Load their environment keys and separately load
renewed bearer tokens from `build/integration/tokens.json`; contexts do not embed
tokens. Refresh tokens after seeding if required by `token-expiry.json`.
Automatic seeding refuses until the actual declared Kestra flow and its configured
publisher routing exist; this prerequisite remains Task 6, not a Task 4 success.
It creates a separate dataset and sends its transition through the real API.

`roles` also provisions `opendx_module_test`, owned by the dedicated CREATEDB role,
and writes its DSN under `module_test`. Operator integration tests require that
test-named anchor and create/drop their own random databases. Assertions retain
`dxos_reader` against `opendx_integration`. Missing private S3 companion config is
reconstructed from existing `.env.integration` credentials without rotating them.

Load a manual context without printing private values (after generating the manual
phase and renewing tokens). This starts only the selected test process:

```sh
python3 scripts/integration/identity.py tokens
python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess
context = json.loads(Path('build/integration/manual.json').read_text())
tokens = json.loads(Path('build/integration/tokens.json').read_text())
env = {**os.environ, **tokens,
       **{k: v for k, v in context.items() if k.startswith('TEST_')}}
subprocess.run(['python3', '-m', 'unittest',
                'tests.integration.acceptance_cases', '-v'], env=env, check=True)
PY
```

`TEST_INDEPENDENCE_JSON` contains `fixture_entity_id` for the OPEN
process_fixture row, its actual `expected_version`, and a separate
`procurement_entity_id` comparison row. The operator is a one-shot CLI and is
not running after publication; the independence runner additionally requires
`TEST_OPERATOR_STOPPED=yes`. Manual context values
`TEST_EFFECT_DELIVERY_PAUSED=yes` and `scanner_stopped=true` correspond to the
whole Kestra and ClamAV containers being stopped, with cap-process continuing to
run. Do not run another seed phase concurrently with these acceptance cases.

Task 6 workflow deployment uses actual Kestra 0.20.7 PostgreSQL repository and
queue in the separate `dxos_kestra` database. Bootstrap creates its dedicated
`dxos_kestra` LOGIN role without superuser, CREATEDB, CREATEROLE, replication or
BYPASSRLS. Runtime owns only that dedicated database. Shared integration data and
storage volumes are preserved during service recreation.

After Ready publication and identity provisioning, run:

```sh
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.workflows deploy
TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/seed.py --phase automatic
TEST_DISPOSABLE_DATABASE=yes python3 -m unittest tests.integration.workflow_cases -v
```

Consume the manual phase before these commands. Deployment imports each actual
manifest-referenced YAML and generates mode-0600 private route/secrets files under
`build/integration/`. The integration process container runs under the invoking
host's nonroot UID/GID to read its private route mount. Compose initialization
creates empty restricted companion files before the first deployment.

**Automatic workflow availability is alpha-only.** The configured signed workflow
subject is alpha-scoped. Routes for beta are intentionally absent: beta events
remain pending/retryable and effects from the alpha principal are denied. Beta
CRUD/command isolation does not establish beta automatic-workflow readiness.
`wrong_workflow` is an integration-only actor with the workflow role and a distinct
subject, used to prove that the role alone cannot invoke effects.

The publisher posts persisted event JSON with `Idempotency-Key: event_id` to its
private single destination. `KESTRA_AUTH_SCHEME=Basic` and `KESTRA_API_TOKEN` carry
private encoded Kestra transport credentials; the historical Bearer-only claim
applies to legacy endpoint mode. The signed effect token remains separate.
Automatic seeding validates both deployed app flow bodies, actual running
publisher configuration, route identities/version/webhook key, and signed-token
workspace/role/lifetime before creating any automatic-phase business data.

Renew before `build/integration/workflow-deployment.json`'s private
`token_expires_at` (allow at least 90 seconds for deployment/work):

```sh
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.workflows renew
```

This acquires fresh Keycloak tokens, writes base64 `SECRET_DXOS_*` environment
values privately, recreates Kestra to load them, verifies its authenticated API,
reimports declarations, and always recreates the publisher to load the private
route map, including package/version-only changes with unchanged image/environment.
The PostgreSQL queue/repository preserves existing execution IDs during this
planned reload. Token renewal is explicit local operator work, not an autonomous
renewal daemon; keep the workload paused/bounded during renewal and do not let
long-running effects outlive the token. Re-run renewal before any later live
suite. No token or webhook key belongs in a tracked example.

The restart case pauses actual Kestra, commits a fresh event, observes its
`in_flight` lease, SIGKILLs the real process service, restores Kestra and starts
cap-process without resetting PostgreSQL or volumes. It waits at most 100 seconds
for the real 60-second lease to expire and re-deliver. Exact event/body/attempt
state and UTC action times are private in `workflow-recovery.json`; sanitized
qualification evidence is recorded in the Task 6 report.

## Files and supporting-service checks

Consume a fresh files phase sequentially; it stops actual Kestra before seeding
and freezes a quarantine fixture by stopping ClamAV. The file suite starts the
scanner as needed and leaves ClamAV, SeaweedFS and cap-process running. It keeps
Kestra stopped so submit rejection has an isolated effect window:

```sh
TEST_DISPOSABLE_DATABASE=yes python3 scripts/integration/seed.py --phase files
TEST_DISPOSABLE_DATABASE=yes python3 -m unittest tests.integration.file_cases -v
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.supporting_services file-adapters
```

The file suite performs real ClamAV/SeaweedFS stop/start and process-worker restart
without deleting volumes. Cleanup restores those services even on test failure.
It uses fresh uploaded IDs per case and the harmless EICAR string inside a PDF
stream; appending EICAR beyond PDF EOF does not exercise the real PDF scanner.
Ignored `build/integration/file-recovery.json` records UTC actions and row states.
ClamAV explicitly allows 25 MiB streams/files and 50 MiB scan expansion; its
healthcheck requires protocol PING/PONG. The adapter test scans a full 20 MiB
object as well as clean content and EICAR using actual services.

After the file phase, restore Kestra with explicit signed-token renewal before
using the automatic workflow or webhook positive-control test:

```sh
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.workflows renew
TEST_DISPOSABLE_DATABASE=yes python3 -m unittest tests.integration.supporting_service_cases -v
python3 -m unittest tests.operations.test_supporting_services -v
TEST_DISPOSABLE_DATABASE=yes python3 -m scripts.integration.supporting_services probe
```

The supporting helper resolves addresses only from the guarded project's actual
containers and supplies private S3 credentials to the child adapter test. It never
prints credentials. The shell probe's `KESTRA_URL` is the **admin API**
`/api/v1/flows/search` for this check, distinct from the runtime publisher's base
URL and secret webhook destination. Shell success requires S3 403 XML AccessDenied,
Kestra API 401, and a real ClamAV PONG. Generic404, redirects, server errors and
connection failures are failures; controlled HTTP tests qualify these exit
semantics only. Actual webhook-key authentication is qualified separately using
real execution/effect observations. OCR remains deferred and unavailable.

Selected file/S06, real recovery and strict probe results are recorded in
[file lifecycle evidence](file-lifecycle-runtime.md). The extra controlled-IP
case temporarily reassigns only the verified process endpoint on its project
network, restores its exact original IP/aliases in finally, and checks unchanged
gateway start time plus direct/gateway original bytes. This is separate from
the natural scanner/storage/process restart case.
