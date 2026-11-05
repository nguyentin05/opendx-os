# Changelog

All notable changes to **OpenDX-OS (DX-OS Open-Core)** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Architectural rules, data contracts, security acceptance matrix (`S01`–`S08`), and FOSS hygiene rules under `.agents/` and `AGENTS.md`.
- Three-tier composable architecture specification separating Tier 1 Headless PaaS (`platform/`, `operator/`), Tier 2 H-P-D-I capabilities and dynamic UI shell (`capabilities/`, `workspace-ui/`), and Tier 3 declarative domain packages (`apps/`).
- Foundational open-source distribution metadata (`LICENSE`, `LICENSES/LICENSE`, `NOTICE`, `COPYRIGHT.txt`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `.editorconfig`, `.gitattributes`, `.gitignore`, and root `Makefile`).
