# Contributing to OpenDX-OS

Contributions should follow the [architecture and accepted decisions](docs/architecture/README.md)
and the contracts relevant to the change:

- [Data and manifests](docs/contracts/data-contracts.md)
- [Security and acceptance criteria](docs/contracts/security-and-acceptance.md)
- [Licensing and source hygiene](docs/development/source-hygiene.md)
- [Verification and evidence](docs/development/verification.md)
- [Adding an app](docs/development/adding-an-app.md) and [releasing](docs/development/releasing.md)

For a shared interface change, identify affected producers and consumers, update
the canonical contract and examples, and describe compatibility or migration
needs. Follow the architecture document's decision-change process when changing
an accepted design.

Keep the change focused and preserve unrelated work. Use a branch or worktree
when isolation helps; link a related issue when one exists. Describe the problem,
resulting behavior, relevant checks, and unverified parts in a hand-off or PR.
Clear commit messages should name the affected part and purpose, for example
`feat(operator): reconcile excess Hasura column permissions`.

Original contributions are licensed under [Apache-2.0](LICENSE). See the source
hygiene document for format-specific headers and third-party exceptions.
