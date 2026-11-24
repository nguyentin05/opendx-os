---
title: Release test plan
description: How the v1.0.0 release candidate is tested, what evidence is recorded and when it may ship.
diataxis: how-to
audience: [platform-developer, operator, evaluator]
status: active
owner: platform
last_reviewed: 2026-11-24
---

# Release test plan

This plan covers the test campaign for v1.0.0. It tests one release candidate
end to end, records evidence for every check, and decides whether the candidate
may be tagged as the release. Performance and capacity are measured separately
in the [benchmark plan](benchmarks.md), using the
[benchmark method](benchmark-method.md).

## Rules

- **Test the artifact, not the checkout.** Every check runs against one release
  candidate tag (`v1.0.0-rc.N`): the archive attached to its draft release and
  the images published for that tag. A local checkout, a branch or a pull
  request merge ref is not evidence for the release.
- **One subject per round.** Record the tag, its commit SHA, the archive
  SHA-256 and the image digests before the first check. A new candidate starts a
  new round; results never carry over silently.
- **Use the verification statuses.** Each check is PASS, FAIL, NOT_RUN or
  NOT_APPLICABLE as defined in [verification](../../contribute/verification.md#recording-results).
  A skipped or partially run check is NOT_RUN with its reason, never PASS.
- **Every defect fixed gets a regression check.** The fix adds a test that fails
  before it, and the reproduction is repeated on the next candidate.
- **After the feature freeze only fixes land.** New behaviour waits for the next
  minor release.

## Timeline

| Date | Step |
| --- | --- |
| 2026-11-26 | Feature freeze; open fixes merged |
| 2026-11-27 | `v1.0.0-rc.1` tagged; round 1 starts |
| 2026-11-27 to 2026-11-29 | Installation, stack, acceptance and UI checks |
| 2026-11-29 to 2026-12-01 | Kubernetes lifecycle and benchmarks |
| 2026-12-02 to 2026-12-03 | Fixes, `v1.0.0-rc.2`, repeat of failed and affected checks, demo rehearsal |
| 2026-12-04 | `v1.0.0` tagged when the exit criteria hold |

## Test layers

| Layer | What it proves | How | Where |
| --- | --- | --- | --- |
| Static and unit | Contracts, generators and handlers behave as written | The `ci.yml` jobs listed in [verification](../../contribute/verification.md#continuous-integration) | CI, on the candidate commit |
| Database integration | Triggers, roles, migrations and services against real PostgreSQL 17 | `integration` and `parser` jobs | CI |
| Archive installation | The release builds from source and runs outside the source tree | Download the archive, verify `SHA256SUMS` and the attestation, run `install.sh` into an empty directory, then `scripts/test-installation.sh` | Clean Linux VM |
| Platform stack | The Compose stack starts and its services answer correctly | `scripts/test-compose.sh`, `test-identity.sh`, `test-platform.sh`, `test-supporting-services.sh`, `test-rag.sh` | Clean Linux VM |
| Demo data | The procurement application works with realistic records | `scripts/seed-demo.py` and its refusal cases | Clean Linux VM |
| Acceptance | S01 to S08 | See [acceptance matrix](#acceptance-matrix) | Clean Linux VM |
| Kubernetes lifecycle | The controller manages `DXApplication` resources on a real cluster | `scripts/k8s/dev-cluster.sh up` and `demo` on kind, then the [EKS scenarios](#kubernetes-scenarios) | kind, EKS |
| User interface | Each role can do its work and is refused what it may not do | The [manual script](#user-interface-script) in a real browser | Clean Linux VM |
| Security regression | Findings reported during QA stay fixed | Repeat each finding's reproduction on the candidate | Clean Linux VM |
| Benchmarks | Capacity, latency, deployment speed and correctness under load and faults | [Benchmark plan](benchmarks.md) | Fixed benchmark host, EKS |

## Acceptance matrix

Copy this table into the campaign's tracking issue for each round and fill one
row per group. The required evidence for each group is defined in
[acceptance](../../reference/security/acceptance.md#eight-mandatory-acceptance-groups).

| ID | Check used | Status | Evidence |
| --- | --- | --- | --- |
| S01 | Integration tests for scope and resource ACL; Hasura probes as each demo role; revocation with a still-valid token | | |
| S02 | Needs the cap-data service (#111) | NOT_RUN | Scheduled for v1.1.0 |
| S03 | Command and effect idempotency tests; retry of a committed command; workflow step retry | | |
| S04 | Stale version `409`; two concurrent commands on one version | | |
| S05 | Two applications sharing a work item and an employee | | |
| S06 | Quarantined and infected file paths; no self-written `CLEAN` | | |
| S07 | Needs the cap-data service (#111) | NOT_RUN | Scheduled for v1.1.0 |
| S08 | Needs the cap-data service (#111) | NOT_RUN | Scheduled for v1.1.0 |

## Kubernetes scenarios

The controller image is published for `linux/amd64` only; use x86 nodes. Deploy
it with `make -C operator deploy IMG=ghcr.io/nguyentin05/opendx-dxos-controller:<version>`
after creating the `dxos-operator-config` Secret in `dxos-operator-system` with
`DATABASE_URL`, `HASURA_URL` and `HASURA_ADMIN_SECRET`, plus `KESTRA_URL`,
`KESTRA_USER` and `KESTRA_PASSWORD` when workflows must be deployed. The database
needs the platform schema and the capabilities the application requires enabled
in `core.capabilities`.

| Scenario | Expected result |
| --- | --- |
| Apply a `DXApplication` | `Ready` with every condition true and `status.activeVersion` set |
| Edit Hasura metadata or a grant by hand | The next reconcile restores it and records the repair |
| Raise the manifest version with an allowed schema change | The change is applied and the active version moves |
| Raise the version with a refused schema change | The old version stays active and the condition explains why |
| `spec.suspend`, `spec.interval`, annotation `dxos.io/reconcileAt` | Reconcile stops, follows the interval, runs on demand |
| Delete the resource | `core.apps` marks the application `Removed`; data is kept |
| Delete the controller pod during a reconcile | The new pod finishes the work without duplicate changes |
| Two replicas with `--leader-elect` | Only the leader reconciles; failover takes over |
| Controller RBAC | The service account can do only what the controller needs |

## User interface script

Run with the demo accounts created by `scripts/seed-demo.py seed`, one browser
profile per account. For each step, write the expected result before running it.

| Role | Steps |
| --- | --- |
| `role_procurement_requester` | Sign in; create a request with lines and an attachment; submit; try to edit a line after submitting (refused with a clear message); cancel a draft; read the record history; sign out and confirm the next sign-in asks for a password |
| `role_procurement_manager` | See only the requests the role may read; approve one (it moves to budget review); try to approve a request you created yourself (refused); reject one with and without a reason (refused without); confirm the review work item is closed |
| `role_procurement_finance` | Approve the budget of a request in budget review; reject another; confirm the requester sees each new state |
| Any role | Switch application and role; confirm stale lists disappear and refused actions are not offered |

## Defects

File each defect as an issue with the candidate tag, the reproduction, the
expected and observed result, and its impact. Report a vulnerability privately
as described in `SECURITY.md`, not as a public issue. A defect blocks the release
when it breaks an acceptance group, data integrity, authorization, installation
or the demo path; other defects go to the roadmap with a note in the release
notes.

## Exit criteria

`v1.0.0` may be tagged when, on the same candidate:

1. Every required CI job is green on the candidate commit.
2. Archive installation, the platform stack scripts and the demo data checks are PASS.
3. S01, S03, S04, S05 and S06 are PASS; S02, S07 and S08 are recorded as NOT_RUN with their reason.
4. The kind demo and the EKS scenarios are PASS, or each exception is written in the release notes.
5. No open blocking defect remains.
6. The benchmark report lists every suite with its status and is linked from the release notes.
7. The demo has been rehearsed on the candidate.

The tag, the filled matrix and links to the evidence go into the release notes.
