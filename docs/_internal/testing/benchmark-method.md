---
title: Benchmark method
description: Rules every OpenDX-OS benchmark follows for subject, environment, sampling, invariants, result records and acceptance.
diataxis: reference
audience: [platform-developer, evaluator]
status: proposed
owner: platform
last_reviewed: 2026-11-24
---

# Benchmark method

A benchmark result is evidence only when another person can tell exactly what
was measured, on what, and how, and can run it again. These rules apply to every
suite in the [benchmark plan](benchmarks.md). They follow the same evidence
vocabulary as [verification](../../contribute/verification.md#recording-results).

## Subject

- Measure one exact revision: a release candidate or release tag, identified by
  its full commit SHA, the archive SHA-256 and the image digests. Never measure
  a dirty tree, a branch tip that may move, or a pull request merge ref.
- Install from the release archive and the published images, the same way the
  [release test plan](test-plan.md) does.
- Record any configuration that differs from the shipped defaults. Raising the
  gateway rate limits for a load run is allowed only on a disposable benchmark
  stack and must be stated next to the result.

## Environment

- Use a dedicated host. Record the CPU model, logical CPU count, memory, disk
  type, kernel, Docker and Compose versions, and the cloud instance type when
  there is one.
- For sizing runs, pin the stack's containers to a CPU set (`cpuset`) of 2 or 4
  cores and run the load generator on other cores. Record the CPU frequency
  governor; use `performance` when the host allows it.
- Record the environment as a short digest (a hash of the recorded fields) so
  two results can be compared only when their environments match.

## Data

- Generate the dataset with a script and a fixed random seed. Record the row
  counts per table and a dataset digest (SHA-256 of the sorted row counts and
  the seed).
- Reset to the same dataset before each run. A run that starts from leftover
  data is invalid.

## Sampling

- **Warm-up, then measure.** Discard a warm-up period (load: 60 s; micro: the Go
  benchmark's own warm-up plus `-count=10`). Report only the measured window.
- **Repeat.** Run each measurement three times and report the median of the
  three runs, with the spread.
- **Report the distribution.** p50, p95, p99 and maximum latency, throughput,
  error rate. For micro benchmarks: ns/op, B/op and allocs/op.
- **Open model for load.** Drive load at a fixed or ramping arrival rate, not by
  a fixed number of looping users. A closed loop slows down with the system and
  hides queueing ("coordinated omission"). Report requests that could not be
  sent on time as dropped.
- **Measure the generator.** Record the load generator's own CPU use. A run
  where the generator saturates is invalid.

## Invariants

A performance number is only valid when the system stayed correct while it was
produced. After every load or fault run, check:

| Invariant | Check |
| --- | --- |
| No server errors | No `5xx` responses outside the injected fault window |
| Every accepted command is audited once | Audit rows for the run equal the successful commands |
| Every committed event is delivered | No outbox event left undelivered after the drain period |
| Every workflow effect happens once | Processed workflow steps equal the effects the commands require; no duplicates |
| No orphan work items | No open work item on a record whose state no longer expects one |

A violated invariant makes the run FAIL, whatever its latency.

## Thresholds

- **Service level for load suites:** a load level passes when p95 latency of all
  requests is below 500 ms and fewer than 1% of requests fail. p99 is reported,
  not gated. The capacity of a configuration is the highest arrival rate that
  still passes.
- **Micro benchmarks:** each records an allocation ceiling with the result.
  Absolute time thresholds are not used across different hardware.
- **Relative comparison (planned):** once a control revision is pinned, a change
  is compared against it on the same host in three alternating
  candidate/control pairs; a median p50 ratio above 1.5 or a throughput ratio
  below 1/1.5 is a regression. This is not yet part of CI.

## Result record

Each measurement produces one JSON record. Records are committed under
`bench/results/<tag>/` and the benchmark report is generated from them.

```json
{
  "schema": "opendx-bench-v1",
  "suite": "load",
  "target": "procurement workload, 4 cores pinned",
  "subject": {
    "tag": "v1.0.0-rc.1",
    "commit": "<40-character SHA>",
    "archive_sha256": "<sha256>",
    "images": {"opendx-cap-process": "sha256:<digest>"}
  },
  "environment_digest": "env:<hash>",
  "dataset_digest": "sha256:<hash>",
  "config_overrides": ["gateway limit-count raised"],
  "sampling": {"warmup_seconds": 60, "measure_seconds": 120, "repetitions": 3, "model": "ramping-arrival-rate"},
  "results": {
    "arrival_rate_per_second": 300,
    "latency_ms": {"p50": 0, "p95": 0, "p99": 0, "max": 0},
    "error_rate": 0.0,
    "dropped": 0,
    "generator_cpu_cores": 0.0
  },
  "invariants": {"server_errors": "PASS", "audit": "PASS", "outbox": "PASS", "effects_once": "PASS", "orphans": "PASS"},
  "status": "PASS"
}
```

`status` uses the verification statuses. A missing or zero-sample measurement is
FAIL or NOT_RUN, never PASS.

## Acceptance

1. Choose the subject tag and record its SHA, archive SHA-256 and image digests.
2. Record the environment and its digest.
3. Run every suite in the [benchmark plan](benchmarks.md); write one record per measurement.
4. Check every record: required fields present, invariants checked, samples non-zero.
5. Generate the report from the records. Each suite shows PASS, FAIL or NOT_RUN with its reason.
6. Link the report and the records from the release notes.

## Reporting rules

- State the limits next to the numbers: one host, synthetic data, the generator
  location, any changed configuration.
- Do not compare with numbers produced by another project's own method. Compare
  only results that share this method and an environment digest.
- Keep results that would expose an unfixed security issue out of public pages
  until the fix is released.
- A benchmark that cannot reach a required input must fail and say why.

## Prior art

These rules borrow from public benchmark practice:

- Pinned subject revisions, provenance in each result record, allocation
  ceilings and paired control comparisons, as in
  [synapse-ce](https://github.com/KKloudTarus/synapse-ce)'s performance and
  benchmark acceptance workflows.
- The open and closed load models in
  [k6](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/).
- Noise reduction for end-to-end runs from
  [Hasura](https://hasura.io/blog/decreasing-latency-noise-and-maximizing-performance-during-end-to-end-benchmarking).
- Counting completed rather than started work, from
  [Camunda](https://blog.bernd-ruecker.com/how-to-benchmark-your-camunda-8-cluster-48ada4b047b6).
