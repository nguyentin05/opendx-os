# Rule 04 — Licensing, source hygiene, and PoF evidence

Applies when creating/editing source files, distribution metadata, or preparing a release. Apache-2.0 is the license choice for the project's self-written code. "50/50" is the PoF goal; scoring requires the official rules, the applicable version, and corresponding evidence, and must not be inferred from the existence of a checklist.

## Headers by format

| Self-written file type | Rule |
| --- | --- |
| `.go`, `.ts`, `.tsx` | `// SPDX-License-Identifier: Apache-2.0` comment and a copyright line at the top of the file; keep valid prologues/build directives |
| `.py`, `.sh` | Keep the shebang on line 1 if present; keep a valid encoding declaration; place the `# SPDX-License-Identifier: Apache-2.0` comment and copyright immediately after the prologue |
| `.sql` | Use `--` comments for SPDX and copyright |
| `.yaml`, `.yml`, `Makefile`, `Dockerfile` | Use `#` comments for SPDX and copyright |
| JSON, lockfiles, Markdown, `SKILL.md`, binaries/images | Do not insert an inline header; keep the format/parser valid and record the license scope/attribution through appropriate distribution metadata |

Copyright for self-written code: `Copyright 2026 DX-OS Open-Core Contributors`. The full license text lives in `LICENSE`; there is no need to copy it into every file. Generated or third-party files keep the metadata from their source/generator and must not be relicensed as the project's copyright. `SKILL.md` keeps YAML frontmatter at the top of the file.

## Hygiene and artifacts

- Text files use UTF-8 and LF. Do not change the line endings of binaries. Dependencies are restored from manifests/lockfiles; do not commit `node_modules/`, `vendor/`, or `.venv/`, and do not copy library code into the project source.
- Before releasing, check `LICENSE`, `NOTICE`, `COPYRIGHT.txt`, README, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, SBOM, installation instructions, and sample configuration. The content must match the actual dependencies/artifact; do not claim every license has been verified when it has not been checked.
- When excluding internal documents from the archive, configure `.gitattributes` using the **actual paths** of the guidance package. Check the generated archive contents; do not assume `export-ignore` is enough if the packager does not use `git archive`.
- The archive `dxos-open-core-X.Y.Z.tar.gz` extracts into the `dxos-open-core-X.Y.Z/` directory, with checksums. Test installing from that extracted archive into a target other than the source tree.

During the design phase, record which artifacts/checks are expected and the criteria to verify. When no PoF rules are defined, report only the project rules that were checked; do not conclude that a competition score has been achieved. Detailed processes: [pof-compliance](../skills/pof-compliance/SKILL.md), [release-package](../workflows/release-package.md).
