---
title: Verification
description: Available runners, evidence, and status vocabulary.
diataxis: how-to
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
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
| Repository hygiene | Root | `make check-hygiene` | Python 3; required metadata files, SPDX headers, and text encoding |
| Tier boundaries | Root | `make check-architecture` | Python 3; static check that Tier 1 and Tier 2 runtime code does not name Tier 3 applications |
| Static contracts | Root | `python3 -m unittest discover -s tests -v` | Python 3 and PyYAML; source/fixture checks, not live database authorization |
| Operator unit tests | `operator/` | `go test ./...` | Go version/dependencies from `go.mod` |
| Capability unit tests | `capabilities/` | `go test ./...` | Go version/dependencies from `go.mod`; PDF parser subtests skip unless `poppler-utils` (`pdfinfo`, `pdfimages`, `pdftotext`) is installed |
| Operator integration | `operator/` | `make test-integration` | `TEST_DATABASE_URL` naming a disposable PostgreSQL 17 test database; Hasura is a local fake |
| Operator CRD schema | `operator/` | `make test-envtest` | Downloads envtest binaries (kube-apiserver, etcd) into `operator/bin/` |
| Operator controller lifecycle | `operator/` | `make test-controller` | envtest plus `TEST_DATABASE_URL`; Hasura is a fake modelling `export_metadata` |
| Operator on a real cluster | Root | `scripts/k8s/dev-cluster.sh up` then `demo` | Docker, kubectl, kind; real PostgreSQL and Hasura in a disposable kind cluster; no Keycloak, Kestra or capabilities |
| Keycloak conformance | Root | `python3 scripts/integration/keycloak_conformance.py` | A disposable Keycloak serving `platform/keycloak/realm-export.json`; the workflow starts it with `docker run` |
| UI tests and build | `workspace-ui/` | `npm test` then `npm run build` | Node/npm and installed dependencies from `package.json`/lockfile |
| Combined available suites | Root | `make test` | Runs the groups supported by the checkout; skips UI when `workspace-ui/node_modules/` is missing, and fails instead when `CI=true` |

`.github/workflows/k8s-integration.yml` runs the envtest and kind checks above:
`make -C operator test-envtest`, `test-controller` against PostgreSQL, and
`scripts/k8s/dev-cluster.sh up`/`demo` on a disposable kind cluster.

Inspect tests before attributing S01–S08 coverage. Neither a `make test` result nor
static DDL assertions establish live PostgreSQL/Hasura behavior, concurrency,
event delivery, or complete acceptance coverage. Integration verification must
identify the test, environment, and observed behavior for each affected group.
The capability module is Go; pytest does not run its Go tests.

## Continuous integration

`.github/workflows/ci.yml` runs on pull requests and pushes to `main`. Each job
name below is the check name to require in branch protection. The workflow has no
`paths` filters: a filtered required check stays pending and blocks a pull
request. Go jobs read the version from `go.mod` and set `GOTOOLCHAIN=local`, so a
toolchain bump fails instead of silently downloading another Go release.

| Job | What it runs | Services | Reproduce locally |
| --- | --- | --- | --- |
| `hygiene` | `make check-hygiene` | none | `make check-hygiene` |
| `architecture` | `make check-architecture` | none | `make check-architecture` |
| `backend (operator)`, `backend (capabilities)` | `go vet ./...`, `go test -race -count=1 -covermode=atomic ./...` with coverage written to `$RUNNER_TEMP`, then `CGO_ENABLED=0 go build ./...` | none | `cd <module> && go vet ./... && go test -race -count=1 ./... && CGO_ENABLED=0 go build ./...` |
| `lint (operator)`, `lint (capabilities)` | golangci-lint v2.15.0 with the root `.golangci.yml` (errcheck, govet, ineffassign, staticcheck, unused) over the whole module | none | `cd <module> && golangci-lint run --config ../.golangci.yml` |
| `unit-test` | `python3 -m unittest discover -s tests -p 'test_*.py' -v` | none | `python3 -m pip install pyyaml && python3 -m unittest discover -s tests -p 'test_*.py' -v` |
| `ui` | `npm ci`, `npm test`, `npm run build` in `workspace-ui/` | none | `cd workspace-ui && npm ci && npm test && npm run build` |
| `integration` | `go test -tags=integration -p 1 -count=1 ./...` in both modules, then the same pair again against one server | `pgvector/pgvector:pg17`, superuser login, database name containing `test`, passwordless host auth for test roles | see below |
| `parser` | `go test -count=1 -v ./internal/ai/knowledge/docir/...` on Alpine with the pinned `poppler-utils`; the job fails if a PDF check reports `--- SKIP` | none | `docker run --rm -v "$PWD:/src" -w /src golang:1.27.4-alpine3.23 sh -c 'apk add --no-cache poppler-utils=25.12.0-r0 git && cd capabilities && go test -count=1 -v ./internal/ai/knowledge/docir/...'` |

The Go jobs publish a coverage total in the job summary. Every job that runs a
suite also runs `.github/actions/assert-clean-tree`, which fails if the suite
modified tracked or untracked files.

Other workflows cover external services and security scans:

| Workflow | What it verifies |
| --- | --- |
| `keycloak-conformance.yml` | Imports the shipped realm into a disposable Keycloak and checks discovery, JWKS, the `dxos-runtime` audience mapper, token claims from `dxos-portal`, and wrong-password rejection |
| `release-gate.yml` | Negative tests for the archive gate, then packages a real archive, verifies it, and checks its checksum and install script |
| `security.yml` | Trivy `fs` and `config` scans that fail on HIGH or CRITICAL findings (accepted findings are listed with a reason in `.trivyignore.yaml`), gitleaks, and govulncheck for both Go modules, which fails on a reachable vulnerability |
| `images.yml` | Trivy scan of every built image before it is pushed ([images and release](#images-and-release)) |
| `codeql.yml` | CodeQL analysis of the Go, JavaScript/TypeScript and Python code |

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
scripts/ci/provision-test-db.sh
(cd operator && go test -tags=integration -p 1 -count=1 ./...)
(cd capabilities && go test -tags=integration -p 1 -count=1 ./...)
```

Run `actionlint .github/workflows/*.yml` after editing a workflow; it checks
syntax, expressions, and the declared permissions. A local `make test` is not CI
evidence: the `go` job adds the race detector, and the `integration` and `parser`
jobs need their own environment.

## Images and release

`.github/workflows/images.yml` builds the nine image targets in
[releasing](releasing.md#3-tag-and-run-the-release-workflow). Each image is
scanned with Trivy before anything is pushed, and the job fails on a HIGH or
CRITICAL finding that has a fixed version (`.trivyignore.yaml` applies). Pull
requests build and scan without pushing; pushes to `main` and `v*` tags publish
to `ghcr.io/nguyentin05/<image>` and attest the image digest.
`.github/workflows/release.yml` runs only when a `v*` tag is pushed. It
rejects a tag that is not an ancestor of `main`, warns when the tag is
lightweight, packs the source tree, runs the archive gate, generates a CycloneDX
SBOM and `SHA256SUMS`, attests both, and attaches the assets to a release. It
never publishes the release.

Reproduce the local parts:

```sh
actionlint .github/workflows/*.yml
scripts/package_release.sh 0.2.0-local
scripts/release/verify-archive.sh dist/dxos-open-core-0.2.0-local.tar.gz
scripts/release/test-verify-archive.sh
docker buildx build --platform linux/amd64 -f capabilities/Dockerfile \
  --build-context legal=. --build-arg SERVICE=cap-ai -t opendx-cap-ai:local capabilities
docker buildx build --platform linux/amd64 -f workspace-ui/Dockerfile \
  --build-context legal=. -t opendx-workspace-ui:local workspace-ui
```

Dispatch the image workflow, push a tag for the release workflow, and verify a
downloaded artifact:

```sh
gh workflow run images.yml
gh attestation verify dxos-open-core-X.Y.Z.tar.gz --repo nguyentin05/opendx-os
gh attestation verify oci://ghcr.io/nguyentin05/opendx-operator:X.Y.Z --repo nguyentin05/opendx-os
```

A local image build proves the Dockerfile target resolves; it does not prove the
registry push, attestation, SBOM, or release upload. Review the assets of the
draft release before publishing it.

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
The coordinated deployment path has its own runner mode,
`TEST_DISPOSABLE_DATABASE=yes scripts/test-rag.sh ai-up`, which drives the real
`scripts/ai-up.sh` and `scripts/rag-up.sh` on a disposable Compose project with the
pinned proxy and a synthetic local endpoint. It refuses the development project and
reports a missing Docker/Compose prerequisite as `NOT_RUN`. The suite is hermetic:
each run exports `AI_UP_STATE_DIR` and `AI_UP_MODEL_ENV` (equivalently
`--state-dir`/`--model-env`) into its own temporary directory and asserts afterwards
that the checkout's `.env.models` and `build/ai-up` are unchanged, so running it
never rewrites or locks a live installation's deployment state.
After provisioning the disposable `TEST_DATABASE_URL`, run
`(cd capabilities && go test -tags=integration -p 1 ./internal/ai/agent -run TestAgentEndToEnd -count=1)`
for signed identity, mixed-source turns, revocation and missing approval.
These tests install parsed fixture chunks. The full `models` gate separately
requires actual worker/parser processing, three authenticated user fixtures,
two loopback adapter spies and browser state. Its preflight prints each missing
environment variable and exits 2 before starting the test.
Deterministic fake responses establish boundary, operation-state, token and
citation behavior, not real-provider groundedness or provider invoicing. A fake
LOCAL case exercises the shared dispatch boundary; it is not evidence that a
LOCAL serving adapter ships.

The model runner asserts the current baseline rather than the retired one: no
budget, reservation or usage-ledger setup is performed or required, a successful
call must be observed as a `SUCCEEDED` operation with its recorded token counters,
and an ambiguous call must be observed as a durable `UNCERTAIN` operation with
null counters, no public usage and no second invocation. Schema presence, absence
of the retired relations, migration checksum refusal and restricted-role
privileges are qualified by the dedicated Go integration cases instead, so a
missing stack prerequisite is reported as `NOT_RUN` and never as a pass.

A qualification run also has to record the rollout gate separately from the test
suite: fresh bootstrap, checksum refusal against an old applied model checksum,
database privileges, standalone capability behavior and the full-stack fake
gate. The named-database rebuild for an installation that applied the earlier
model checksums is an owner-run action with a human execution gate
([installation](../operations/installation.md#rebuilding-an-existing-model-installation));
no qualification run performs it, and a skipped rebuild stays pending rather than
counting as verified.

The separate `scripts/test-rag.sh real-models` mode requires explicit consent
through `RAG_REAL_MODELS_OPT_IN=true`, a disposable test database, independently
reviewed active hosted profiles and PERSONAL approvals, and the actual ingestion
services. Follow [real hosted qualification](../guides/knowledge/models.md#explicit-real-hosted-qualification)
for fixture setup, approval UUIDs, protected CLI identity and the approval
withdrawal performed by the gate. Run its offline guards with
`python3 -m unittest discover -s scripts/integration -p test_rag_real_models.py -v`.
Record live results under `.scratch/`, including commit, command, qualified
provider/model pair and any `NOT_RUN` live revision-switch criterion. This mode
is excluded from default CI and `make rag-up`.
