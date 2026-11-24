---
title: Benchmark plan
description: The micro, load, permission, workflow, deployment, fault, operations and quality suites planned for v1.0.0.
diataxis: reference
audience: [platform-developer, operator, evaluator]
status: proposed
owner: platform
last_reviewed: 2026-11-24
---

# Benchmark plan

This page lists the benchmark suites planned for v1.0.0, what each one measures
and why. Every suite follows the [benchmark method](benchmark-method.md). No
suite has been run yet: until a result record exists, a suite's status is
NOT_RUN.

The suites answer four questions:

1. **How fast are the building blocks?** Micro benchmarks of the generators and
   authorization code, independent of any running service.
2. **How much load does one installation carry, and on how many cores?** Load,
   permission and workflow suites.
3. **How quickly can an application be deployed and repaired?** Deployment suite,
   on the CLI and on Kubernetes.
4. **Does the platform stay correct under load and failure?** Fault suite, plus
   the invariants checked after every run.

## Suites

| ID | Suite | Runs on | Priority |
| --- | --- | --- | --- |
| M | [Micro](#m-micro) | Any machine, no stack | Required |
| L | [Load and sizing](#l-load-and-sizing) | Benchmark host | Required |
| P | [Permission overhead](#p-permission-overhead) | Benchmark host | Required |
| W | [Workflow throughput](#w-workflow-throughput) | Benchmark host | Recommended |
| D | [Deployment](#d-deployment) | Benchmark host, EKS | Required |
| F | [Correctness under faults](#f-correctness-under-faults) | Benchmark host | Required |
| O | [Operations](#o-operations) | Benchmark host | Recommended |
| Q | [Quality](#q-quality) | Benchmark host, CI | Optional |

## M: Micro

Go benchmarks (`go test -bench`) that need neither a database nor a running
service, so anyone can repeat them from the source tree.

| Target | Inputs | Reported |
| --- | --- | --- |
| Operator planner: `BuildPlan`, `GenerateDDL`, `GenerateHasuraOperations` | Generated manifests with 5, 20 and 50 tables | ns/op, B/op, allocs/op |
| Capability authorization: `CanSee`, `Matches` | Rules with own, department and organization scope; nested record filters | ns/op, B/op, allocs/op |
| Knowledge extraction: DocIR chunking and the `unicode-rune-v1` tokenizer | The gold fixtures in `capabilities/internal/ai/knowledge/docir/testdata/` | ns/op per page, B/op |

Each result records an allocation ceiling. These benchmarks are the first
candidates for a CI regression gate.

## L: Load and sizing

The main capacity measurement through the gateway, as a signed-in user would
use the platform.

- **Workload.** Each iteration: create a procurement request, list open
  requests with a filter, run the next command of the state machine, read the
  record history, list the caller's own requests. Requesters and approvers act
  with their own tokens.
- **Dataset.** 10,000 and 100,000 requests, reset before each level.
- **Load.** Ramping arrival rate until the service level in the
  [method](benchmark-method.md#thresholds) fails.
- **Sizing.** Repeat with the stack pinned to 2 and to 4 cores. Report the
  highest passing arrival rate and the peak memory of each service.
- **Sign-in apart.** Measure password sign-ins per second and token refreshes
  per second separately; sign-in cost is dominated by password hashing in
  Keycloak. Compare with Keycloak's published
  [sizing guidance](https://www.keycloak.org/high-availability/single-cluster/concepts-memory-and-cpu-sizing).

## P: Permission overhead

Authorization lives in the database and in generated Hasura permissions. This
suite shows what it costs.

- The same list query as a role with organization scope, department scope and
  own scope, on 10,000, 100,000 and 1,000,000 rows.
- Report p50 and p95 per scope and size, and keep the `EXPLAIN (ANALYZE, BUFFERS)`
  plan for each, to show which index serves each filter.
- A scope whose latency grows with the table size, rather than with the rows it
  returns, is a finding.

## W: Workflow throughput

Commands publish events through the outbox to the workflow engine, which calls
back into cap-process for its effects.

- Count **completed** effects per second, not started executions.
- Measure the lag from command commit to the visible work item (p50, p95).
- Send a burst well above the sustained rate; record the outbox backlog and the
  time to drain it.
- After the run, the [invariants](benchmark-method.md#invariants) must hold,
  in particular no orphan work items after submit-then-cancel pairs.

## D: Deployment

How quickly an application definition becomes a working application, and how
quickly drift is repaired.

| Measurement | How |
| --- | --- |
| `dxos-operator validate`, `plan`, `apply` | Time each command for the procurement manifest, first apply and an unchanged re-apply |
| Install to first ready application | From an empty VM: archive download and check, `install.sh`, stack healthy, procurement `Ready`; with and without cached images |
| Kubernetes apply to `Ready` | `kubectl apply` of a `DXApplication` until `Ready`, on EKS |
| Drift repair | Change a grant or Hasura permission by hand; time until restored |
| Scale | 1, 10 and 50 `DXApplication` resources: time to all `Ready`, controller CPU and memory |

The controller exposes controller-runtime metrics when started with
`--metrics-bind-address`; collect `controller_runtime_reconcile_time_seconds`
and `workqueue_depth` during the scale runs.

## F: Correctness under faults

The platform promises at-least-once delivery with exactly-once effects. This
suite checks the promise while things fail.

| Fault | Injected during a steady load run |
| --- | --- |
| Workflow engine restart | Stop and start Kestra |
| Capability restart | Kill cap-process mid-command |
| Controller restart | Delete the controller pod mid-reconcile (Kubernetes) |
| Slow or broken network | Add latency and connection resets between cap-process and Kestra, and between services and PostgreSQL, with a proxy such as Toxiproxy |

After recovery and the drain period, every
[invariant](benchmark-method.md#invariants) must hold. Report the recovery time
and the number of retried deliveries.

## O: Operations

| Measurement | How |
| --- | --- |
| Soak | Two hours at half the measured capacity; memory slope per service, PostgreSQL connections, outbox and audit growth |
| Backup and restore drill | With 100,000 requests and their files: time `scripts/backup.sh` and a restore on an empty host (the recovery time); compare row counts, audit state and file checksums before and after; state the recovery point the procedure gives |
| Idle footprint | CPU and memory per service at rest; image sizes |
| Hosting cost | Monthly price of a VM that meets the measured sizing, with the date the price was checked |

## Q: Quality

| Measurement | How |
| --- | --- |
| Knowledge retrieval | Recall@k and MRR of personal-library search on a labelled Vietnamese question set built from the gold fixtures; lexical baseline reported beside it |
| Workspace UI | Lighthouse performance and accessibility scores (median of several runs) and an axe scan of the main pages; bundle size |
| Open-source posture | OpenSSF Scorecard result; OpenSSF Best Practices self-assessment |
| Security scans | Trivy image scan results for every image; an OWASP ZAP baseline scan of the UI and gateway of a disposable installation |

## Out of scope for v1.0.0

- Comparisons with other products, which would need the same method and
  environment on both sides.
- A CI regression gate with a pinned control revision (see
  [thresholds](benchmark-method.md#thresholds)); planned after v1.0.0.
- Multi-node and high-availability deployments.

## Layout

Scripts live in `bench/`, one directory per suite, each with a README that gives
the exact command. Result records go to `bench/results/<tag>/`; the report for a
release is generated from them and linked from its release notes.
