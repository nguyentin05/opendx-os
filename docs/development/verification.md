# Verification and evidence

Choose checks for the requested output and the affected behavior. Identify the
contract and its producers/consumers before editing; afterwards check scope,
consistency, syntax, links, and [source hygiene](source-hygiene.md). Use focused
behavior checks for code changes rather than tests that repeat implementation text.

| Work | Evidence needed |
| --- | --- |
| Design or documentation | Consistent contracts and examples, consumer impact, links, relevant parser/format checks, acceptance scenarios, and open implementation decisions |
| Implementation | Relevant build/static checks, behavior tests, affected [S01–S08](../contracts/security-and-acceptance.md#eight-mandatory-acceptance-groups), and necessary integration checks |
| Release | Implementation checks, all S01–S08 groups, artifact checks, and installation from the archive on the release revision |

A documentation change does not require building future services. A required
implementation check that was not run is an evidence gap; a missing mandatory
release check prevents a readiness conclusion.

## Available runners

Run commands from the repository root unless a working directory is shown. Check
the current [Makefile](../../Makefile), module manifests, and prerequisites first.

| Check | Working directory | Command | Prerequisites and limits |
| --- | --- | --- | --- |
| Repository hygiene | Root | `make check-hygiene` | Python 3 and PyYAML; metadata, text/header checks, static boundary/manifest checks |
| Static contracts | Root | `python3 -m unittest discover -s tests -v` | Python 3 and PyYAML; source/fixture checks, not live database authorization |
| Operator unit tests | `operator/` | `go test ./...` | Go version/dependencies from `go.mod` |
| Capability unit tests | `capabilities/` | `go test ./...` | Go version/dependencies from `go.mod` |
| Operator integration | `operator/` | `make test-integration` | `TEST_DATABASE_URL` naming a disposable PostgreSQL 17 test database; Hasura is a local fake |
| Operator CRD schema | `operator/` | `make test-envtest` | Downloads envtest binaries (kube-apiserver, etcd) into `operator/bin/` |
| Operator controller lifecycle | `operator/` | `make test-controller` | envtest plus `TEST_DATABASE_URL`; Hasura is a fake modelling `export_metadata` |
| Operator on a real cluster | Root | `scripts/k8s/dev-cluster.sh up` then `demo` | Docker, kubectl, kind; real PostgreSQL and Hasura in a disposable kind cluster; no Keycloak, Kestra or capabilities |
| UI tests and build | `workspace-ui/` | `npm test` then `npm run build` | Node/npm and installed dependencies from `package.json`/lockfile |
| Combined available suites | Root | `make test` | Runs the groups supported by the checkout; currently skips UI when `node_modules/` is missing |

Inspect tests before attributing S01–S08 coverage. Neither a `make test` result nor
static DDL assertions establish live PostgreSQL/Hasura behavior, concurrency,
event delivery, or complete acceptance coverage. Integration verification must
identify the test, environment, and observed behavior for each affected group.
The capability module is Go; pytest does not run its Go tests.

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
