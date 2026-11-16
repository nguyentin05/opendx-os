# Documentation map

This file is for readers browsing the repository on GitHub. It is excluded from
the published site; the site entry point is the hand-written landing page [`index.html`](index.html).

## Published documentation

| Area | Purpose |
| --- | --- |
| [`get-started/`](get-started/index.md) | Tutorial path: run the stack and complete a first flow |
| [`guides/`](guides/index.md) | Task guides: add an app, integrate the UI, run workflows, configure knowledge |
| [`reference/`](reference/index.md) | Contracts: runtime rules, manifest, data model, security, API, CLI, RAG |
| [`architecture/`](architecture/index.md) | Tiers, control plane, runtime, security model, anti-patterns |
| [`adr/`](adr/index.md) | Accepted and proposed architecture decisions |
| [`operations/`](operations/index.md) | Installation, backup and restore, recovery |
| [`contribute/`](contribute/index.md) | Verification, source hygiene, release process, docs conventions |

## Internal documentation

`docs/_internal/` is excluded from the site by `exclude_docs` in `mkdocs.yml`. It
holds archived demo material and handoff notes, local plans, raw evidence, and
translations. Public pages must not link into it.

## Local checks

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
mkdocs build --strict
python3 -m unittest tests.test_docs -v
```
