---
title: Releasing
description: Release gates, artifacts, container images, and acceptance evidence.
diataxis: how-to
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-18
---

# Preparing a release

Select the version, revision, target environment, and applicable official PoF
rules. Project hygiene checks support release verification; they do not establish
a competition score.

## 1. Prepare the revision

1. Run [verification](verification.md) for the release revision, including all
   [S01–S08 groups](../reference/security/acceptance.md#eight-mandatory-acceptance-groups).
   A mandatory check marked `NOT_RUN` prevents a readiness conclusion. The
   release workflow does not repeat these checks on the tag, so tag only a
   revision whose evidence is already recorded.
2. Complete `LICENSE`, `NOTICE`, `COPYRIGHT.txt`, `README.md`, `CHANGELOG.md`,
   `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `Makefile`,
   `.env.example`, installation instructions, and CycloneDX SBOMs in
   `sbom/*.cdx.json`. Dependency inventory, attribution, license compatibility,
   and SBOM content must match actual manifests, lockfiles, and shipped content.
   Original project code uses Apache-2.0; third-party metadata follows
   [source hygiene](source-hygiene.md).
   Verify the full Apache-2.0 text in `LICENSE`, licensing purpose and attribution
   in `NOTICE`, and the third-party inventory with OSI-approved licenses in
   `COPYRIGHT.txt`; report any dependency whose license has not been verified.

## 2. Draft the release notes

`.github/workflows/release-drafter.yml` runs on every push to `main` and keeps a
draft release for the next semantic version. `.github/release-drafter.yml`
autolabels pull requests from their conventional-commit title prefixes and
groups them into Added, Fixed, Security, Docs, Maintenance, and Breaking
sections. A breaking change below 1.0 bumps the minor version; a feature bumps
the minor version; everything else bumps the patch version.

Confirm the draft contains the changes for the revision you verified. The draft
tag is the `vX.Y.Z` to use in the next step. The autolabeler reads the
repository labels `feature`, `fix`, `docs`, `maintenance`, `breaking`, and
`security`; create them first in a fork that does not have them.

Keep `CHANGELOG.md` aligned with the draft before creating the tag: move the
entries under `[Unreleased]` into a new `## [X.Y.Z] - YYYY-MM-DD` section, group
them under the Keep a Changelog headings (Added, Changed, Deprecated, Removed,
Fixed, Security), and update the compare links at the bottom of the file. The
GitHub release notes may carry more detail; the changelog is the
repository-level summary.

## 3. Tag and run the release workflow

Create an annotated tag on the verified `main` commit and push it:

```sh
git switch main
git pull --ff-only
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

Only the repository owner may create, move, or delete a tag: the `protect tags`
repository ruleset covers every `refs/tags/*` ref and bypasses only that account.
The release workflow runs only on tag pushes; it has no manual dispatch. It
also rejects a run started or re-run by another account.

`.github/workflows/release.yml` then:

1. validates that the tag exists, matches `vX.Y.Z`, and is an ancestor of
   `main` (a lightweight tag only produces a warning);
2. packs `dist/dxos-open-core-X.Y.Z.tar.gz` with `scripts/package_release.sh`,
   runs `scripts/release/verify-archive.sh` so the archive is refused when it
   carries local-only material or lacks a component the installer needs, and
   writes `dist/SHA256SUMS`;
3. generates `dist/opendx-os-X.Y.Z.cdx.json` with `anchore/sbom-action`;
4. attests the tarball and SBOM with `actions/attest-build-provenance`;
5. uploads the assets to the existing draft release, or creates a draft with
   `--generate-notes` when no release exists for the tag. The workflow never
   publishes a release.

When a run fails, re-run it from the Actions page or with
`gh run rerun <run-id> --failed`; a re-run uses the same tag. To release a fix,
create a new tag instead of moving an existing one.

## 4. Review the generated assets

The draft release must contain the tarball, `SHA256SUMS`, the CycloneDX SBOM,
and the packager's `.tar.gz.sha256` file. Verify the checksums and the build
provenance before publishing:

```sh
gh release download vX.Y.Z --pattern 'dxos-open-core-*.tar.gz' \
  --pattern 'opendx-os-*.cdx.json' --pattern 'SHA256SUMS'
sha256sum -c SHA256SUMS
gh attestation verify dxos-open-core-X.Y.Z.tar.gz --repo nguyentin05/opendx-os
gh attestation verify opendx-os-X.Y.Z.cdx.json --repo nguyentin05/opendx-os
```

The Images workflow publishes `ghcr.io/nguyentin05/<image>` for each supported
image when `main` or a `v*` tag is pushed:

| Image | Build context | Dockerfile | Build argument |
| --- | --- | --- | --- |
| `opendx-operator` | `operator` | `operator/Dockerfile` | |
| `opendx-dxos-controller` | `operator` | `operator/Dockerfile.controller` | |
| `opendx-dxos-authz` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=dxos-authz` |
| `opendx-cap-process` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=cap-process` |
| `opendx-cap-hr` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=cap-hr` |
| `opendx-cap-data` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=cap-data` |
| `opendx-cap-ai` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=cap-ai` |
| `opendx-cap-ai-worker` | `capabilities` | `capabilities/Dockerfile` | `SERVICE=cap-ai-worker` |
| `opendx-cap-ai-parser` | `capabilities` | `capabilities/Dockerfile.parser` | |
| `opendx-workspace-ui` | `workspace-ui` | `workspace-ui/Dockerfile` | |

Tag pushes publish `X.Y.Z` and `X.Y` tags; `main` pushes publish `main` and a
`sha-*` tag. Pull requests build every image but never push. Verify an image's
provenance with
`gh attestation verify oci://ghcr.io/nguyentin05/opendx-operator:X.Y.Z --repo nguyentin05/opendx-os`.

## 5. Inspect and install the archive

Extract the archive into a fresh temporary directory, then run the extracted
`scripts/install.sh <target-dir>` into a separate target. Verify the target's
required runtime/configuration without relying on the original checkout. The
installer copies a source scaffold and attempts builds; it does not establish a
fully configured production deployment. Record those limits. Keep evidence
before cleaning only temporary directories created for this run.

Keep project contracts, architecture, and development documentation, including
shared designs and integration handoffs, in the distribution. The packager
excludes only user-local material (`.scratch/`, `.claude/`), agent guidance
(`.agents/`, `AGENTS.md`, `AGENTS.local.md`, `CLAUDE.md`), the private
`docs/superpowers/` workspace, and the internal planning notes in
`docs/_internal/`; do not exclude a documentation directory solely because its
name sounds like design or handoff. A `.gitattributes` `export-ignore` entry
does not prove exclusion when another packager is used, so inspect the archive
produced by the actual packager. Artifact changes require repackaging and
checksum verification.

Check the archive contents and the installation, not the source tree alone.
Record missing rules, unverified dependencies, and unavailable checks
explicitly; a list of expected files is not proof of compliance.

## 6. Publish

Review the draft, its notes, assets, checksums, and attestations, then publish it
in the GitHub UI or with `gh release edit vX.Y.Z --draft=false`. Confirm
`CHANGELOG.md` already has the matching version section. Publishing is a
separate action within the assigned scope, on the exact verified revision. Hand
off the artifact, checksum, provenance, and evidence with the release.
