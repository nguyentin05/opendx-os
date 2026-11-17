---
title: Verification
description: Available runners, evidence, and status vocabulary.
diataxis: how-to
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-17
---

# Verification and evidence

Choose checks for the requested output and the affected behavior. Identify the
contract and its producers/consumers before editing; afterwards check scope,
consistency, syntax, links, and [source hygiene](source-hygiene.md). Use focused
behavior checks for code changes rather than tests that repeat implementation text.

| Work | Evidence needed |
| --- | --- |
| Design or documentation | Consistent contracts and examples, consumer impact, links, relevant parser/format checks, acceptance scenarios, and open implementation decisions |
| Implementation | Relevant build/static checks, behavior tests, affected [S01–S08](../reference/security/acceptance.md#eight-mandatory-acceptance-groups), and necessary integration checks |
| Release | Implementation checks, all S01–S08 groups, artifact checks, and installation from the archive on the release revision |

A documentation change does not require building future services. A required
implementation check that was not run is an evidence gap; a missing mandatory
release check prevents a readiness conclusion.

## Available runners

Run commands from the repository root unless a working directory is shown. Check
the current `Makefile`, module manifests, and prerequisites first.

| Check | Working directory | Command | Prerequisites and limits |
| --- | --- | --- | --- |
| Repository hygiene | Root | `make check-hygiene` | Python 3 and PyYAML; metadata, text/header checks, static boundary/manifest checks |
| Static contracts | Root | `python3 -m unittest discover -s tests -v` | Python 3 and PyYAML; source/fixture checks, not live database authorization |
| Operator unit tests | `operator/` | `go test ./...` | Go version/dependencies from `go.mod` |
| Capability unit tests | `capabilities/` | `go test ./...` | Go version/dependencies from `go.mod`; PDF parser subtests skip unless `poppler-utils` (`pdfinfo`, `pdfimages`, `pdftotext`) is installed |
| Operator integration | `operator/` | `make test-integration` | `TEST_DATABASE_URL` naming a disposable PostgreSQL 17 test database; Hasura is a local fake |
| Operator CRD schema | `operator/` | `make test-envtest` | Downloads envtest binaries (kube-apiserver, etcd) into `operator/bin/` |
| Operator controller lifecycle | `operator/` | `make test-controller` | envtest plus `TEST_DATABASE_URL`; Hasura is a fake modelling `export_metadata` |
| Operator on a real cluster | Root | `scripts/k8s/dev-cluster.sh up` then `demo` | Docker, kubectl, kind; real PostgreSQL and Hasura in a disposable kind cluster; no Keycloak, Kestra or capabilities |
| UI tests and build | `workspace-ui/` | `npm test` then `npm run build` | Node/npm and installed dependencies from `package.json`/lockfile |
| Combined available suites | Root | `make test` | Runs the groups supported by the checkout; skips UI when `workspace-ui/node_modules/` is missing, and fails instead when `CI=true` |

Inspect tests before attributing S01–S08 coverage. Neither a `make test` result nor
static DDL assertions establish live PostgreSQL/Hasura behavior, concurrency,
event delivery, or complete acceptance coverage. Integration verification must
identify the test, environment, and observed behavior for each affected group.
The capability module is Go; pytest does not run its Go tests.

## Continuous integration

`.github/workflows/ci.yml` runs on pull requests, pushes to `main`, manual
dispatch, and as the reusable workflow the release pipeline calls. Each job name
below is the check name to require in branch protection. The workflow has no
`paths` filters: a filtered required check stays pending and blocks a pull
request. Go jobs read the version from `go.mod` and set `GOTOOLCHAIN=local`, so a
toolchain bump fails instead of silently downloading another Go release.

| Job | What it runs | Services | Reproduce locally |
| --- | --- | --- | --- |
| `hygiene` | `make check-hygiene` | none | `python3 -m pip install pyyaml && make check-hygiene` |
| `go (operator)`, `go (capabilities)` | `go vet ./...`, `go test -race -count=1 ./...`, then `CGO_ENABLED=0 go build ./...` | none | `cd <module> && go vet ./... && go test -race -count=1 ./... && CGO_ENABLED=0 go build ./...` |
| `lint (operator)`, `lint (capabilities)` | golangci-lint v2 with the root `.golangci.yml`; pull requests report only issues the change introduces | none | `cd <module> && golangci-lint run --config ../.golangci.yml` |
| `python` | `python3 -m unittest discover -s tests -p 'test_*.py' -v` | none | `python3 -m pip install pyyaml && python3 -m unittest discover -s tests -p 'test_*.py' -v` |
| `ui` | `npm ci`, `npm test`, `npm run build` in `workspace-ui/` | none | `cd workspace-ui && npm ci && npm test && npm run build` |
| `integration` | `go test -tags=integration -p 1 -count=1 ./...` in both modules, then the same pair again against one server | `pgvector/pgvector:pg17`, superuser login, database name containing `test`, passwordless host auth for test roles | see below |
| `parser` | `go test -count=1 -v ./internal/knowledge/docir/...` on Alpine with the pinned `poppler-utils`; the job fails if a PDF check reports `--- SKIP` | none | `docker run --rm -v "$PWD:/src" -w /src golang:1.27.3-alpine3.23 sh -c 'apk add --no-cache poppler-utils=25.12.0-r0 git && cd capabilities && go test -count=1 -v ./internal/knowledge/docir/...'` |

The integration tests create and drop one database per test, so the login must be
a superuser, the URL must name a database containing `test` (the harness rejects
other names), and packages must run with `-p 1` because PostgreSQL roles are
cluster-global. Knowledge and model tests log in as the runtime roles they create,
which have no password, so the disposable server accepts passwordless host
connections. Reproduce the job with a disposable server:

```sh
docker run -d --name ci-pg -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=ci-local -e POSTGRES_DB=postgres_test \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  -p 5432:5432 pgvector/pgvector:pg17
export TEST_DATABASE_URL='postgres://postgres:ci-local@localhost:5432/postgres_test?sslmode=disable'
# The job's provisioning step: platform schemas, then the cluster runtime roles.
for f in platform/postgres/init/01_core_schema.sql \
         platform/postgres/init/02_capability_registry.sql \
         platform/postgres/init/03_app_registry.sql \
         platform/postgres/init/05_operator_deployments.sql \
         platform/postgres/init/07_app_definitions.sql \
         platform/postgres/capabilities/cap_hr.sql \
         platform/postgres/capabilities/cap_work.sql \
         platform/postgres/capabilities/cap_process.sql \
         platform/postgres/capabilities/cap_data.sql \
         platform/postgres/init/06_runtime_roles.sql; do
  psql -q -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$f"
done
(cd operator && go test -tags=integration -p 1 -count=1 ./...)
(cd capabilities && go test -tags=integration -p 1 -count=1 ./...)
```

Run `actionlint .github/workflows/*.yml` after editing a workflow; it checks
syntax, expressions, and the declared permissions. A local `make test` is not CI
evidence: the `go` job adds the race detector, and the `integration` and `parser`
jobs need their own environment.

## Recording results

Record the scope, command or review action, working directory, revision/artifact,
and result. Use these statuses:

- **PASS:** executed with evidence of success.
- **FAIL:** executed with an observed failure; include the error.
- **NOT_RUN:** applicable but not executed; include the reason or prerequisite.
- **NOT_APPLICABLE:** outside the scope; explain why.

After a relevant change, rerun the affected checks. Preserve the distinction
between static evidence and runtime behavior. A hand-off or PR describes the
change and rationale, affected consumers, results, remaining gaps, and open
decisions. Review findings include a location, failure scenario, impact, and fix
direction; distinguish contract defects from unimplemented requirements.

The optional model slice uses `scripts/test-rag.sh models` with real PostgreSQL
17/pgvector, Operator, File/scanner/Hasura and controlled HTTP model spies; see
[model qualification](../guides/knowledge/models.md#qualification). Run Go integration
packages with `-p 1` when fixtures share PostgreSQL cluster-global roles.
Deterministic fake responses establish boundary/accounting/citation behavior,
not real-provider groundedness or invoice qualification.
