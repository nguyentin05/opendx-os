# Preparing a release

Select the version, revision, target environment, and applicable official PoF
rules. Project hygiene checks support release verification; they do not establish
a competition score. The release process below defines required evidence; a
complete packager and release verification environment are still needed.

1. Run [verification](verification.md) for the release revision, including all
   [S01–S08 groups](../contracts/security-and-acceptance.md#eight-mandatory-acceptance-groups).
   A mandatory check marked `NOT_RUN` prevents a readiness conclusion.
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
3. Package `dist/dxos-open-core-X.Y.Z.tar.gz`, extracting into
   `dxos-open-core-X.Y.Z/`, and produce `SHA256SUMS`. Use the actual packager;
   `scripts/package_release.sh` is not present in the current checkout.
   Artifact changes require repackaging and checksum verification.
4. Inspect the archive itself. Exclude internal agent guidance (`.agents/`,
   `AGENTS.md`, and `CLAUDE.md` if present) using actual paths and the packager's
   exclusion mechanism. Existing `.gitattributes` excludes `.agents/` and
   `AGENTS.md` for `git archive`. Keep project contracts, architecture, and
   development documentation in the distribution. An `export-ignore` entry
   does not prove exclusion when another packager is used.
5. Extract into a fresh temporary directory, then run the extracted
   `scripts/install.sh <target-dir>` into a separate target. Verify the target's
   required runtime/configuration without relying on the original checkout.
   The current installer copies a source scaffold and attempts builds; it does
   not establish a fully configured production deployment. Record those limits.
   Keep evidence before cleaning only temporary directories created for this run.
6. Hand off the artifact, checksum, provenance, and evidence. Publishing is a
   separate action within the assigned scope, on the exact verified revision.

Check archive contents and installation rather than relying on the source tree
alone. Record missing rules, unverified dependencies, and unavailable checks
explicitly; a list of expected files is not proof of compliance.
