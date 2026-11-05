---
name: pof-compliance
description: Use when checking project licensing, source hygiene, PoF evidence, or release artifacts.
---

# Licensing and release evidence

Apply [source hygiene](../../../docs/development/source-hygiene.md) and
[release requirements](../../../docs/development/releasing.md).

1. Identify the revision/artifact and applicable official PoF rules. Separate
   project requirements from claims that need official judging criteria.
2. Inspect file formats before changing headers; check third-party attribution
   against actual dependency manifests, lockfiles, and shipped content.
3. Run available hygiene checks, then inspect the archive, SBOM, checksum, and
   out-of-tree installation for a release task. Static checks alone do not prove
   artifact completeness or all dependency licenses.
4. Report reproducible findings and [evidence statuses](../../../docs/development/verification.md#recording-results).
   Identify missing rules/checks instead of assigning an unsupported score.
