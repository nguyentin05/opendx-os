# Workflow — Development and hand-off

Use when creating/editing design, documentation, or source code; the Git/PR steps apply when a repository exists and the task requires them.

1. **Determine scope:** Choose the phase per [AGENTS](../../AGENTS.md) and read the relevant skill and sources of truth. Record open decisions that affect the result; resolve ordinary details yourself within the assigned scope.
2. **Before changing:** Run [pre-change](../hooks/pre-change.md). If the change needs isolation in Git, use a branch/worktree appropriate to the current state. Create an issue when the process/task requires it; it is not a precondition for every edit.
3. **Implement:** For design, edit the source of truth and the documents that point to it; describe the acceptance scenarios. For source-code behavior, write a test that can detect the defect first, implement, and run the relevant checks. Do not add tests merely to match the wording of the documentation.
4. **Verify:** Run [post-change](../hooks/post-change.md) and the [hand-off gate](../hooks/pre-pr-gate.md). Update the evidence status by phase.
5. **Hand off:** Summarize the change, the rationale, the producer/consumer impact, and the verification limits. When creating a PR, link the issue if one exists and propose reviewers by ownership; contract changes need both sides' perspectives. Do not send messages or publish outside the assigned scope.

Complete when the requested output exists and the recipient can tell what has been verified from what still needs implementation or checking. Opening a PR or merging must not be inferred from the workflow name alone.
