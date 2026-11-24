# Internal documentation

Everything under `docs/_internal/` is **not published**. The MkDocs configuration
excludes this directory through `exclude_docs`; a docs test also checks that
public pages never link here.

| Directory | Contents |
| --- | --- |
| `archive/` | Historical demo scenarios and handoff notes kept for context |
| `evidence/` | Raw verification evidence: logs, JSON, screenshots, run records |
| `testing/` | Release test plan, benchmark plan and benchmark method (not yet published) |
| `plans/` | Local implementation plans (also ignored by Git) |
| `translations/` | Local translations; English remains canonical |

When a historical page is still useful, keep it here and add a short public
summary under `reference/` or `operations/` instead of publishing stale detail.
