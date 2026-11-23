# Documentation map

This file is for readers browsing the repository on GitHub. It is excluded from
the published site; the site entry point is the Vietnamese landing page [`index.html`](index.html).

## Published documentation

| Area | Purpose |
| --- | --- |
| [`get-started/`](get-started/index.md) | Tutorial path: run the stack and complete a first flow |
| [`guides/`](guides/index.md) | Task guides: add an app, integrate the UI, run workflows, configure knowledge |
| [`reference/`](reference/index.md) | Contracts: runtime rules, manifest, data model, security, API, CLI, RAG |
| [`architecture/`](architecture/index.md) | Tiers, control plane, runtime, security model, anti-patterns |
| [`adr/`](adr/index.md) | Architecture decisions and their status |
| [`operations/`](operations/index.md) | Installation, backup and restore, recovery |
| [`contribute/`](contribute/index.md) | Verification, source hygiene, release process, docs conventions |

## Internal documentation

`docs/_internal/` is excluded from the site by `exclude_docs` in `mkdocs.yml` and
from release archives. It holds archived demo material and handoff notes, local
plans and raw evidence. Public pages must not link into it. Vietnamese
translations of published pages live next to them as `page.vi.md`.

## Local checks

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve                                  # Vietnamese site (mkdocs.yml)
mkdocs build --strict -f mkdocs.en.yml        # English site
python3 -m unittest tests.test_docs -v
```
