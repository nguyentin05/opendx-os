# Verification gate before hand-off or PR

Applies even when only files are handed off and there is no Git/PR yet. This is guidance for choosing and recording evidence; it does not run when a file is saved.

## Choosing the verification level

| Phase | Checks to perform | Completion condition |
| --- | --- | --- |
| Design/documentation | Contract consistency, consumer impact, links, JSON/YAML/frontmatter syntax, and examples that can be checked independently | State the decisions, acceptance scenarios, and parts not yet implemented; a future system's test runner is not required |
| Implementation | Build/static checks and tests relevant to the changed part; affected S01–S08 groups and necessary integration | Evidence that the implemented part passes; a required check that was not run is a gap that must be reported, and integration readiness must not be concluded |
| Release | Implementation gate, all S01–S08 groups, PoF/artifact, and installation from the archive | Mandatory checks pass on the release revision/artifact; if a mandatory check is missing, the release is not ready |

## Choosing commands and working directory

Use commands from the module's actual README, Makefile, or dependency manifest. Check the runner and prerequisites before invoking anything. Do not create a runner, install dependencies, or start services just to turn a documentation task into a system test.

The following example is for an **implementation project that already has all four runners**, run from an identified project root. Each `cd` stays inside a subshell; `set -e` stops the chain as soon as one group fails:

```bash
set -e
python3 scripts/check_pof.py
(cd operator && go test ./... -v)
pytest capabilities/ tests/ -v
(cd workspace-ui && npm test && npm run build)
```

If the actual runner differs from the example, use the actual runner and record the command. Before release, verify that the test suite really covers [S01–S08](../rules/03-anti-patterns-and-security.md); do not infer coverage from the name `make test` alone.

## Recording results

Each check has: phase/scope, command or action, working directory, the subject under test, and one status:

- **PASS:** ran and produced evidence of success.
- **FAIL:** ran and an observed failure; record the specific error.
- **NOT_RUN:** applicable but not executed; record the missing prerequisite or reason.
- **NOT_APPLICABLE:** outside the scope/phase; record why.

Fix the root cause within the task scope, then re-run the affected part. Do not reuse old results after changing the part under test, do not turn FAIL/NOT_RUN into PASS to get past the gate, and do not treat static analysis results as runtime test evidence.
