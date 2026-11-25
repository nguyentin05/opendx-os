---
title: Release execution plan
description: How the v1.0.0 test plan and benchmark plan are actually carried out, and where this differs from them.
diataxis: how-to
audience: [platform-developer, operator, evaluator]
status: active
owner: platform
last_reviewed: 2026-11-25
---

# Release execution plan

The [release test plan](test-plan.md), [benchmark plan](benchmarks.md) and
[benchmark method](benchmark-method.md) describe the full campaign. This page
records what is actually done for v1.0.0 with one maintainer and one test week,
and where it differs from those pages. Where this page is silent, the original
pages apply unchanged.

## Timeline

The dates are the earliest plan, not deadlines: v1.0.0 is tagged as soon as the
exit criteria hold. The submission deadline is 2026-12-06 17:00.

| Date | Step |
| --- | --- |
| 2026-11-25 to 2026-11-26 | Pre-freeze fixes (round 0 defects below); feature freeze at the end of 11-26 |
| 2026-11-27 to 2026-11-30 | EKS deployment ([below](#eks)); `v1.0.0-rc.1` once the pre-freeze fixes are merged |
| 2026-11-30 | Weekly sprint release `v0.4.0` from main |
| 2026-12-01 to 2026-12-03 | Test round on the candidate, reduced benchmarks, screenshots and video |
| After the round passes, at the latest 2026-12-04 | Merge the documentation branch, tag `v1.0.0` |
| Until 2026-12-09 | The EKS cluster stays up for grading |

Every tag and release is created only with the maintainer's explicit approval.

## Changes to the rules

- **Freeze exemptions.** After the freeze, besides fixes, only the EKS deployment
  files (`deploy/`, #166, #168, #169, #170) and the UI fixes #167 and #164 may
  land. They add deployment configuration or fix behaviour; they add no product
  feature.
- **Documentation-only changes do not restart a round.** A change limited to
  `README.md`, `CHANGELOG.md`, `ROADMAP.md`, `docs/` and `assets/` after the last
  candidate does not require repeating the round. The documentation branch
  (README, presentation files, changelog and roadmap) is opened as a draft pull
  request early so CI checks it all week, and merged just before `v1.0.0`.
- **v0.x releases are sprint releases.** Only v1.0.0 is the official release; no
  upgrade path from a v0.x installation is tested or promised.

## Test layers

Same layers as the [test plan](test-plan.md#test-layers), with these changes:

| Change | Detail |
| --- | --- |
| Added: Tier 3 hot-add | On a running stack, copy an application package that is not installed into `apps/`. It reaches `Ready` (measured: under 30 s on Compose, 2026-11-24), its roles can be granted, and its records, commands, child rows, attachments and workflow effects work. Run on Compose and on EKS (as a `DXApplication`). The package used in the demo stays out of `apps/` until the demo |
| Added: second application in the UI script | One timesheet or maintenance role in addition to procurement |
| Added: live refresh | If #164 lands: a command in one browser shows in another within about 5 s, and a newly installed application appears without a reload |
| Removed: upgrade from v0.x | See above |
| Kubernetes | kind demo **and** EKS scenarios are both required (exit criterion 4 below) |

### Exit criteria

As in the [test plan](test-plan.md#exit-criteria), except criterion 4: the kind
demo and the EKS scenarios are both PASS. EKS exceptions are not accepted,
because the EKS cluster is the live demo environment.

## Round 0 defects

Known before the first candidate. Blocking ones are fixed before the freeze.

| Defect | Found | Blocking | Tracking |
| --- | --- | --- | --- |
| Numeric columns accept `NaN`; `CHECK (x > 0)` lets it through | 2026-11-24 fault run | Yes, data integrity | `fix/numeric-nan-check` |
| Lists and detail show user, file and device UUIDs | 2026-11-24 | No, but fixed before screenshots | #161 |
| Record detail page has no layout | 2026-11-24 | No, but fixed before screenshots | #162 |
| UI proxy waits about 14 s per request while the gateway is down | 2026-11-24 fault run | No | #163 |
| UI cannot sign in on any host other than localhost | 2026-11-25 | Yes for EKS | #167 |
| NUL byte cut from text input | 2026-11-24 fault run | No | Roadmap |

The 2026-11-24 run on the Compose stack (concurrency, malformed input, forged
roles, file abuse, GraphQL abuse, stopping or killing Kestra, ClamAV, authz,
Keycloak, PostgreSQL, cap-process and Hasura, 5,000 rows) is the baseline: each
check that passed there is repeated on the candidate.

## Benchmarks

Only these suites run for v1.0.0. W, O and Q are recorded as NOT_RUN with the
reason "not scheduled for v1.0.0".

| Suite | Scope for v1.0.0 |
| --- | --- |
| M | As planned: `go test -bench` for the planner, `CanSee`/`Matches` and DocIR chunking |
| L | One dataset (10,000 requests), one configuration, no 2/4-core sizing. Sign-in measured apart. Gateway limits raised only on a disposable stack, and stated |
| P | Organization, department and own scope on 100,000 rows, with `EXPLAIN (ANALYZE, BUFFERS)` |
| F | The 2026-11-24 fault scripts moved into the repository and run on the candidate; no network fault proxy |
| D | `validate`/`plan`/`apply` timings, archive install to first `Ready` on Compose, `kubectl apply` to `Ready` on EKS, Tier 3 hot-add time. No 50-resource scale run |

Method changes: M and L keep three repetitions; P, F and D run once and say so.
Results are kept as raw output under `bench/results/<tag>/` with one summary
table in the benchmark report; no report generator is written.

## EKS

The production and live demo environment, tracked in #165 to #170.

- **Beside, not instead.** `deploy/` is added next to `compose*.yaml` and
  `platform/k8s/dev`. No existing deployment file is moved or removed until the
  EKS deployment has passed its tests and the maintainer confirms.
- **Database.** A 1–2 hour check of RDS for PostgreSQL 17 (#165). If it is not
  compatible without weakening the role model, it is dropped and not put on the
  roadmap; PostgreSQL runs as a StatefulSet on EBS.
- **Domain.** The maintainer's domain on Cloudflare: `app.` for the UI and
  gateway, `auth.` for Keycloak (#168, #169). DNS-only while building; proxied
  with Full (strict) for the demo.
- **Public cluster.** Secrets outside Git, demo passwords different from the
  repository defaults, admin consoles private (#170).

## Screenshots and video

Recorded during the test week on the EKS cluster running the candidate, after
#161 and #162 are fixed. No token, password, account ID or admin URL in frame.
Videos are published outside Git and linked; images in the repository are
compressed.
