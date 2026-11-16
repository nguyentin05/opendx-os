# Working on OpenDX-OS

Work within the user's assigned scope. Choose an approach and checks appropriate
to the requested output; ordinary implementation choices do not require another
approval. Preserve unrelated edits and report unresolved product decisions.

Read `AGENTS.local.md` at the repository root if it exists.

## Read by task

Read relevant project documents and actual implementation before changing a part.
Follow links as needed; there is no requirement to load the entire set.

| Task involves | Project sources | Practical skill |
| --- | --- | --- |
| Architecture, infrastructure, tier boundaries | [Architecture](docs/architecture/index.md), [ADRs](docs/adr/index.md) | Use the relevant component skill below |
| Operator, DDL, Hasura, reconcile | [Manifest](docs/reference/manifest/index.md), [data contracts](docs/reference/data-contracts/index.md) | [go-operator](.agents/skills/go-operator/SKILL.md) |
| Commands, files, OT, BI, OCR/RAG | [Data contracts](docs/reference/data-contracts/index.md), [security/acceptance](docs/reference/security/index.md) | [hpdi-capability](.agents/skills/hpdi-capability/SKILL.md) |
| App Shell, schema rendering, context changes | [UI integration](docs/guides/integrating-the-ui.md), [architecture](docs/architecture/index.md), [data](docs/reference/data-contracts/index.md), [security](docs/reference/security/index.md) | [dynamic-ui-shell](.agents/skills/dynamic-ui-shell/SKILL.md) |
| App packages, manifests, workflows | [Adding an app](docs/guides/adding-an-app.md) | [declarative-app](.agents/skills/declarative-app/SKILL.md) |
| License metadata, hygiene, release artifacts | [Source hygiene](docs/contribute/source-hygiene.md), [releasing](docs/contribute/releasing.md) | [pof-compliance](.agents/skills/pof-compliance/SKILL.md) |
| Verification or hand-off | [Verification](docs/contribute/verification.md) | Select checks for the affected behavior |
| Documentation site, front matter, navigation | [Writing docs](docs/contribute/writing-docs.md) | Run `tests.test_docs` and a strict MkDocs build |

## Working principles

- Project requirements live in `docs/`; skills explain how to apply them. Define
  each detailed requirement once and link to it.
- For shared contract changes, identify affected producers/consumers and follow
  [decision and contract changes](docs/architecture/index.md#decisions-and-contract-changes).
  Distinguish intended behavior from partial implementation.
- Verify the requested output and report evidence, limits, and open decisions.
  A design review is not a runtime test result; use actual module runners.
- Follow runtime permissions and the user's scope for external actions. Project
  documentation does not itself authorize publishing or contacting others.
- Write project documentation in English, preserving technical names and API
  identifiers. English pages are the source; an optional Vietnamese translation
  (`page.vi.md`) never adds requirements, and English wins when they differ.
  Keep the root README focused on the software project.
