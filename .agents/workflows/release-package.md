# Workflow — Packaging and release verification

Use when preparing a release artifact. If the task only designs the process, describe inputs/outputs and the checks required; do not publish or build future systems.

1. Determine the version, revision, project root, and verification environment. Read [pof-compliance](../skills/pof-compliance/SKILL.md) and check against the rules the project provides; record any part with no confirmed source.
2. Run the [release gate](../hooks/pre-pr-gate.md): the S01–S08 groups and artifact checks must have passing evidence. NOT_RUN is not sufficient to announce readiness.
3. Complete the changelog, license metadata, dependency inventory, and SBOM **before** packaging. Create `dist/dxos-open-core-X.Y.Z.tar.gz` and `SHA256SUMS` with the actual packager; any artifact change after this step requires repackaging and re-checking the checksum.
4. Check the archive contents, root directory name, and excluded paths per [Rule 04](../rules/04-pof-50-compliance.md). Do not check only the source checkout.
5. Create a separate test directory with `mktemp -d` and extract the archive there; from the extracted source directory run `./scripts/install.sh <target-dir>` into another directory. Verify the runtime/config needed from the target without relying on the original checkout. Clean up only the temporary directory created by this run, after keeping the necessary evidence; do not delete a fixed shared `/tmp` path.
6. Hand off the artifact, checksum, and verification report. When the task already includes publishing to an external service, create the tag/release on the exact revision that was verified; if that is not assigned, stop at a reviewable package. Ask for more only when authority/scope for the external step is still missing.

Release is complete when the artifact is handed off within the correct scope and the recipient can check the provenance, checksum, installation, and acceptance results of that same artifact.
