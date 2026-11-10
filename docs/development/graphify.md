# Optional: Graphify

[Graphify](https://github.com/Graphify-Labs/graphify) builds a local, queryable
knowledge graph to help coding agents explore the repository. It is optional;
normal development and verification do not require it.

From the repository root, with Python 3.10+ and
[uv](https://docs.astral.sh/uv/) installed, create and activate a project-local
virtual environment (bash/zsh):

```bash
uv venv .venv
source .venv/bin/activate
uv pip install "graphifyy[sql]"

# Choose your assistant; --project keeps the integration in this checkout.
graphify install --project --platform codex
# Alternatively, for Antigravity:
# graphify install --project --platform antigravity
```

The SQL extra enables SQL schema parsing for this repository. Activate the
virtual environment again in each new terminal before running `graphify`, and
launch your assistant from that terminal so its subprocesses inherit the local
CLI on `PATH`. Run `deactivate` when finished.

The PyPI package is named `graphifyy`; its command is `graphify`. Use the
[upstream platform guide](https://github.com/Graphify-Labs/graphify#readme) for
other agents and platform-specific setup; retain `--project` for local installs. Start a new assistant session after
installing the skill, then invoke `/graphify .` from the repository root.
Invocation syntax may vary by assistant.

Once a graph exists, query it from the repository root:

```bash
graphify query "Reconcile" --budget 1500
graphify explain "Runner"
```

Use the upstream skill's `--update` workflow after source changes, or run
`graphify watch .` to update code relationships while working. Code AST
extraction runs locally; semantic analysis of documentation, PDFs, and images
uses the assistant's model or a configured model backend. See the upstream
instructions before enabling semantic extraction.

The `.venv/`, generated graphs, caches, interpreter markers, and Graphify skill
folders for Codex and Antigravity are ignored by Git. Antigravity's generated
Graphify rule and workflow are also ignored. Other maintained project skills
remain tracked.

Project-scoped installers can also edit existing instruction/configuration files
such as `AGENTS.md` and `.codex/config.toml`. Ignoring a skill directory does not
hide edits to tracked files. Review `git diff` after installation and keep only
the installer's personal Graphify additions out of commits, preserving unrelated
settings. Check graph findings against source and the contracts in `docs/`.

These local installation commands follow the upstream
[project-scope installer](https://github.com/Graphify-Labs/graphify/blob/v8/graphify/install.py).

