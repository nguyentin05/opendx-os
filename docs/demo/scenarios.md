# Backend demo scenarios (A6 / G20)

Status: prepared fixtures, **real-system acceptance NOT_RUN**. C0 remains proposed;
B must accept SQL/validator/privilege handoff before deploying. UI A5 is deferred.
The existing App Shell is scaffolding, not a compatible connected portal.

Procurement uses only cap-process (including files). B provisions both workspaces,
publishes coherent Ready snapshots, imports the reviewed Kestra workflows and seeds
employees using [actors.json](../../tests/fixtures/actors.json). Every signed token
must have matching sub/user/employee UUID, workspace and allowed role grants. JSON
claim fixtures are not tokens. Workflow calls require role_workflow and the configured
WORKFLOW_SERVICE_USER_ID matching the fixture workflow actor. The beta requester has
a distinct employee UUID because the current core employee primary key is global.
[seed.sql](../../tests/fixtures/seed.sql) is reviewable identity seed input only:
it creates no domain tables, registry rows, files or business records and is never
automatically executed. B owns operational seeding, reset and service administration.

1. As requester, create a procurement DRAFT through Hasura with signed identity
   presets. Upload quote bytes through the process file endpoint, then wait for CLEAN.
   A nullable quote permits draft creation; actual submit/approve require a present
   CLEAN quote (missing or unclean returns 422).
2. Send submit with entity_id, expected_version, payload {}, and a stable
   Idempotency-Key. Retry the identical body/key: same response, one audit/event.
   A different body/key fingerprint returns 409. Manager approval requires current
   version and a different requester identity. Self-approval returns 403.
3. Persisted command.committed reaches the configured Kestra webhook. The workflow
   maps trigger.body.event_id and literal create_review to the effect endpoint;
   it uses a signed workflow bearer token and role_workflow. Duplicate effects
   return the stored work item. The manifest owns the literal work-item fields.
4. Publish process_fixture from apps/process-fixture (underscore is the C0 app ID).
   Its OPEN/IN_REVIEW/DONE lifecycle shares supported employees/files/process
   primitives. It implements no maintenance/timesheet/OT policy. Stop Operator
   after publication and verify command execution and procurement state independence.

Maintenance and timesheet keep their existing relationship, OT and business
policies. Those declarations exceed C0; they are **unsupported / NOT_RUN**, not
silently reduced to process-only samples. AI OCR, BI and data endpoints remain
fail-closed/deferred; no successful AI/data behavior is part of this demo.

## Explicit live invocation

Install psql and provide disposable PostgreSQL/Hasura/process services. Export
TEST_PROCESS_URL (origin), TEST_GRAPHQL_URL (full /v1/graphql URL), TEST_DATABASE_URL,
TEST_DISPOSABLE_DATABASE=yes and real TEST_REQUESTER_TOKEN, TEST_MANAGER_TOKEN,
TEST_NOACCESS_TOKEN, TEST_SELFAPPROVER_TOKEN, TEST_BETA_REQUESTER_TOKEN and
TEST_WORKFLOW_TOKEN signed by the configured issuer. B prepares independent records:
retry is DRAFT with CLEAN quote; stale is SUBMITTED owned by a different requester
with an intentionally older expected_version; denied is DRAFT; quarantined is DRAFT
with QUARANTINED quote; selfapprove is SUBMITTED owned by the selfapprover actor. All records are in alpha. Set TEST_CASES_JSON to an object
with keys retry/stale/denied/quarantined/selfapprove, each containing entity_id and expected_version.
Use a fresh dataset before every invocation because success mutates state.

```sh
python3 -m unittest tests.integration.acceptance_cases -v
```

For independence also grant role_review in the requester token, prepare an OPEN
process_fixture with CLEAN attachment and a separate procurement comparison row.
Set TEST_OPERATOR_STOPPED=yes only after B actually stops Operator; set
TEST_INDEPENDENCE_JSON to {"fixture_entity_id":"UUID","procurement_entity_id":"UUID","expected_version":1}.

```sh
python3 -m unittest tests.integration.runtime_independence_cases -v
```

Missing configuration is an error, never a passing skip. These filenames intentionally
avoid test_ so default unittest discovery stays a static/local-double suite. The live
cases cover selected S01/S03/S04/S05/S06 assertions, not full S01–S08 acceptance:
Hasura CRUD/FK isolation, concurrent approval, scanner restart, webhook import/auth,
operational restart still need the integration window.
Kestra YAML/plugin expression and webhook key configuration require deployment review;
no real Kestra execution is claimed. Supply DXOS_PROCESS_URL, DXOS_WORKFLOW_TOKEN,
DXOS_WEBHOOK_KEY through Kestra secrets. KESTRA_API_TOKEN protects publisher delivery;
DXOS_WORKFLOW_TOKEN is the distinct signed OIDC service token for effect calls.

The live acceptance module additionally requires TEST_WORKSPACE_ID equal to alpha,
TEST_ISOLATED_WORKLOAD=yes and TEST_EFFECT_DELIVERY_PAUSED=yes. B must ensure no
other writers create work items in that disposable workspace during the test and
pause automatic Kestra effect execution (including already queued tasks), so only
the test's explicit HTTP effect calls execute create_review. These flags acknowledge
real operational prerequisites; they do not stop workers. The first explicit effect
must increase the workspace work-item count by exactly one; replay must leave it
unchanged. The created item must match the declared workspace/title/description/
priority/assignment. Each denied command compares the complete protected domain
row and audit/outbox counts immediately after that individual request.
