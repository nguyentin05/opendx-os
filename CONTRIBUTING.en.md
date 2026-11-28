# Contributing to OpenDX-OS

[Tiếng Việt](CONTRIBUTING.md) | **English**

Contributions should follow the [architecture and accepted decisions](docs/architecture/index.md)
and the contracts relevant to the change:

- [Data and manifests](docs/reference/data-contracts/index.md)
- [Security and acceptance criteria](docs/reference/security/index.md)
- [Licensing and source hygiene](docs/contribute/source-hygiene.md)
- [Verification and evidence](docs/contribute/verification.md)
- [Adding an app](docs/guides/adding-an-app.md) and [releasing](docs/contribute/releasing.md)

For a shared interface change, identify affected producers and consumers, update
the canonical contract and examples, and describe compatibility or migration
needs. Follow the architecture document's decision-change process when changing
an accepted design.

Keep the change focused and preserve unrelated work. Use a branch or worktree
when isolation helps; link a related issue when one exists. Describe the problem,
resulting behavior, relevant checks, and unverified parts in a hand-off or PR.
Clear commit messages should name the affected part and purpose, for example
`feat(operator): reconcile excess Hasura column permissions`.

Project documentation is written in English, and English is the source. The
community files at the repository root (`README`, `CONTRIBUTING`,
`CODE_OF_CONDUCT`, `SECURITY`, `ROADMAP`) come in two versions: `X.en.md` is the
English source and `X.md` its Vietnamese translation. When you change one,
change the other in the same pull request.

Original contributions are licensed under [Apache-2.0](LICENSE). See the source
hygiene document for format-specific headers and third-party exceptions.
