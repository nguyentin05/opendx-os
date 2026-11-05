# Working on OpenDX-OS

Work within the user's assigned scope. Choose an approach and checks appropriate
to the requested output; ordinary implementation choices do not require another
approval. Preserve unrelated edits and report unresolved product decisions.

## Read by task

Read relevant project documents and actual implementation before changing a part.
Follow links as needed; there is no requirement to load the entire set.

| Task involves | Project sources | Practical skill |
| --- | --- | --- |
| Architecture, infrastructure, tier boundaries | [Architecture and ADRs](docs/architecture/README.md) | Use the relevant component skill below |
| Operator, DDL, Hasura, reconcile | [Data/manifest contract](docs/contracts/data-contracts.md) | [go-operator](.agents/skills/go-operator/SKILL.md) |
| Commands, files, OT, BI, OCR/RAG | [Data](docs/contracts/data-contracts.md), [security/acceptance](docs/contracts/security-and-acceptance.md) | [hpdi-capability](.agents/skills/hpdi-capability/SKILL.md) |
| App Shell, schema rendering, context changes | [Architecture](docs/architecture/README.md), [data](docs/contracts/data-contracts.md), [security](docs/contracts/security-and-acceptance.md) | [dynamic-ui-shell](.agents/skills/dynamic-ui-shell/SKILL.md) |
| App packages, manifests, workflows | [Adding an app](docs/development/adding-an-app.md) | [declarative-app](.agents/skills/declarative-app/SKILL.md) |
| License metadata, hygiene, release artifacts | [Source hygiene](docs/development/source-hygiene.md), [releasing](docs/development/releasing.md) | [pof-compliance](.agents/skills/pof-compliance/SKILL.md) |
| Verification or hand-off | [Verification](docs/development/verification.md) | Select checks for the affected behavior |

## Working principles

- Project requirements live in `docs/`; skills explain how to apply them. Define
  each detailed requirement once and link to it.
- For shared contract changes, identify affected producers/consumers and follow
  [decision and contract changes](docs/architecture/README.md#decisions-and-contract-changes).
  Distinguish intended behavior from partial implementation.
- Verify the requested output and report evidence, limits, and open decisions.
  A design review is not a runtime test result; use actual module runners.
- Follow runtime permissions and the user's scope for external actions. Project
  documentation does not itself authorize publishing or contacting others.
- Write project documentation in English, preserving technical names and API
  identifiers. Keep the root README focused on the software project.
