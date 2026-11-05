# Pre-change checklist

This is a checklist to read and carry out, not a registered lifecycle hook.

1. Determine the output, the phase, and the implementation project's logical paths from the task. Do not treat the location of the guidance package as the project root.
2. Identify the affected component per [Rule 01](../rules/01-architecture-and-boundaries.md). For contract changes, read [data-contracts](../memory/data-contracts.md), identify the producer/consumer, and pick the matching skill from [AGENTS](../../AGENTS.md).
3. For data/permissions/workflows, choose the relevant S01–S08 group in [Rule 03](../rules/03-anti-patterns-and-security.md). In design, write down the scenario and expected result; in implementation, determine how the check will actually run.
4. For new files or prologue edits, choose the format-correct handling per [Rule 04](../rules/04-pof-50-compliance.md). Preserve content the user has already edited and the assigned scope.

The checklist is complete once you know which source of truth to edit, which consumers are affected, and how the change will be proven. There is no need to build future components just to finish a documentation change.
