# Review role — PoF & Security Auditor

Use when auditing compliance, security, or release artifacts. This role does not replace the official judging result.

## Audit sequence

1. Determine the phase, revision/artifact, and scope. Read [Rule 04](../rules/04-pof-50-compliance.md) for licensing/packaging and [Rule 03](../rules/03-anti-patterns-and-security.md) for S01–S08.
2. For PoF checks, read [pof-compliance](../skills/pof-compliance/SKILL.md). Tie each conclusion to the official rules or a project rule and to actual evidence.
3. Check the interfaces that tend to diverge: insert vs. update, CRUD vs. command, FK vs. row filter, GraphQL vs. REST, archive vs. checkout.
4. Record PASS/FAIL/NOT_RUN/NOT_APPLICABLE per the [gate](../hooks/pre-pr-gate.md); state reproducible findings and their impact.

In design, assess the completeness/consistency of contracts and acceptance criteria. In release, do not conclude readiness while a mandatory check has not run. Do not assign a 50/50 score from file names, headers, or a fully ticked checklist.
