---
name: pof-compliance
description: Rules and verification checks to guarantee 50/50 Points of FAIL (PoF) in OLP PMNM 2026.
---

# Skill: FOSS PoF 50/50 Compliance (`pof-compliance`)

## 1. Mandatory Repository Artifacts
- `LICENSE`: Full Apache-2.0 text.
- `NOTICE`: Explicit statement of licensing purpose and attribution.
- `COPYRIGHT.txt`: Complete inventory of third-party open-source dependencies and their OSI-approved licenses.
- `README.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`.
- `sbom/*.cdx.json`: CycloneDX SBOM files matching actual dependency manifests.
- `Makefile` & `.env.example`.

## 2. File-Level Rules
1. **Apache-2.0 SPDX Header:** Required at the top of every `.go`, `.py`, `.ts`, `.tsx`, `.sql`, `.sh`, `.yaml`, `.yml`, `Makefile`, and `Dockerfile`.
2. **Line Endings:** Strictly `LF` (`\n`) enforced via `.gitattributes` and checked by `scripts/check_pof.py`.
3. **No Bundled Dependencies:** Never commit `node_modules/`, `vendor/`, `.venv/`, or pre-bundled third-party source files.
4. **Clean Release Tarball (`export-ignore`):**
   - Configure `.gitattributes` so `git archive` / `scripts/package_release.sh` excludes internal agent configs (`.agents/`, `AGENTS.md`, `CLAUDE.md`) from the official `dxos-open-core-X.Y.Z.tar.gz` release archive.
