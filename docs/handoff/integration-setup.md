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

Identity, app publication and phase data are pending separate integration gates.
The supported phase entrypoint currently fails closed before creating records:

```sh
python3 scripts/integration/seed.py --phase manual
python3 scripts/integration/seed.py --phase automatic
python3 scripts/integration/seed.py --phase files
```

Actual registry Ready snapshots for both `procurement` and `process_fixture`
are required; caller assertions of readiness are not accepted. Subsequent work
must obtain fixture identities from `tests/fixtures/actors.json`, use authorized
CRUD/command transitions for fresh records, capture submitted/stale versions and
write ignored 0600 `build/integration/{manual,automatic,files}.json` context files
with `TEST_CASES_JSON`, `TEST_INDEPENDENCE_JSON` and phase record IDs. No context
file or READY/PASS assertion is fabricated now. Manual delivery must be paused
including queued work; automatic delivery requires the real Kestra path. The full
seed/reset round trip and unrelated-project preservation live check remain
NOT_RUN until those gates are available.
