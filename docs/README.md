# Documentation map

This file is for readers browsing the repository on GitHub. It is excluded from
the published site; the site entry point is [`index.md`](index.md).

## Published documentation

| Area | Purpose |
| --- | --- |
| [`get-started/`](get-started/index.md) | Tutorial path: run the stack, seed data, complete a first flow |
| [`guides/`](guides/index.md) | Task guides: add an app, integrate the UI, run workflows, configure knowledge |
| [`reference/`](reference/index.md) | Contracts: manifest, data model, security acceptance, API, CLI, RAG |
| [`architecture/`](architecture/README.md) | Three tiers, control plane, runtime, anti-patterns |
| [`adr/`](adr/index.md) | Accepted and proposed architecture decisions |
| [`operations/`](operations/installation.md) | Installation, configuration, backup and restore, upgrade |
| [`contribute/`](contribute/index.md) | Verification, source hygiene, release process, docs conventions |

## Internal documentation

`docs/_internal/` is excluded from the site by `exclude_docs` in `mkdocs.yml`.
It holds historical handoff notes, archived demo material, raw evidence, local
plans, and translations. Public pages must not link into it.

## Local checks

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
mkdocs build --strict
python3 -m unittest tests.test_docs -v
```
