---
title: Writing docs
description: Diataxis page types, front matter, navigation, and docs checks.
diataxis: how-to
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Writing docs

Documentation is part of the product. It is built with MkDocs Material from
`docs/`, checked in CI, and published from `main`.

## Choose a page type

Each page has one Diataxis type:

| Type | Use it for | Example |
| --- | --- | --- |
| `tutorial` | A guided first experience with a known end state | [Run the local stack](../get-started/local-stack.md) |
| `how-to` | Steps to accomplish a specific task | [Adding an application](../guides/adding-an-app.md) |
| `reference` | Required behavior, fields, routes, and contracts | [Manifest schema](../reference/manifest/schema.md) |
| `explanation` | Why the system is shaped this way | [Three tiers](../architecture/tiers.md) |

Do not mix all four in one page. Link to the canonical page for the other types
instead of copying it.

## Add front matter

Every published page starts with:

```yaml
---
title: Data contracts
description: One sentence that explains what the page covers.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---
```

| Field | Allowed values |
| --- | --- |
| `diataxis` | `tutorial`, `how-to`, `reference`, `explanation` |
| `status` | `draft`, `proposed`, `active`, `deprecated`, `historical` |
| `audience` | `evaluator`, `operator`, `app-author`, `platform-developer`, `security-reviewer` |
| `owner` | A team or role responsible for the page |
| `last_reviewed` | `YYYY-MM-DD` |

`draft`, `proposed`, `deprecated`, and `historical` pages get a banner from
`hooks/status_banner.py`. Do not claim more than the implementation supports.

## Add the page to navigation

Add every published page to `nav` in `mkdocs.yml`. A page that is not reachable
from navigation fails the docs test. Public pages must never link into
`docs/_internal/`; put history, raw evidence, plans, and translations there.

## Write links

- Use relative links between pages, including the `.md` extension.
- Link to the canonical page, not a duplicate section.
- Repository files outside `docs/` are not part of the site; name them in code
  style instead of linking to them.
- After moving a page, add a redirect in `mkdocs.yml` and update references in
  `AGENTS.md`, the root `README.md`, and `contracts/`.

## Run the checks

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs build --strict
python3 -m unittest tests.test_docs -v
```

`tests/test_docs.py` checks front matter, navigation, relative links, internal
exclusion, ADR coverage, and stale data-model terms. See
[Verification](verification.md) for evidence statuses.
