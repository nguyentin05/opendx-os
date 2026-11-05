# Security Policy

## Supported Versions

| Version | Supported |
| :--- | :--- |
| `main` (Unreleased) | Yes |

## Security Architecture Invariants

OpenDX-OS enforces defense-in-depth across all tiers:

1. **Authenticated Context & Least Privilege:**
   - Identity, `workspace_id`, and effective role permissions are verified by backend services from authenticated tokens; client-supplied headers or UI state never grant access.
   - Database connections use least-privilege roles rather than `superuser` or `BYPASSRLS`.
2. **Same-Workspace Isolation (`S01`):**
   - Every workspace-scoped entity uses composite foreign keys `(workspace_id, <foreign_id>) REFERENCES <table>(workspace_id, id)` to prevent cross-workspace linking at the database layer.
3. **Atomic Version-Guarded State Transitions (`S03`, `S04`, `S05`):**
   - Domain tables increment `version` server-side via a `BEFORE UPDATE` trigger.
   - Ordinary roles cannot set or mutate `status` or `version` via CRUD; state transitions execute atomically through the Command API after verifying `expected_version` and idempotency keys.
4. **Quarantine-First File Handling (`S06`):**
   - Uploaded files start in `QUARANTINED` status and are scanned by `ClamAV` before transitioning to `CLEAN` or `INFECTED`.
   - Download, AI OCR extraction, and command submission reject any file whose status is not `CLEAN`.
5. **Serialized Audit Hash-Chains & External Checkpoints:**
   - Workspace audit entries serialize hash computation under a per-workspace transaction lock and anchor signed checkpoints in **OpenBao** outside PostgreSQL.

## Reporting a Vulnerability

Please do **not** report security vulnerabilities through public GitHub issues.
Instead, use GitHub Private Vulnerability Reporting (Security Advisories) on the
repository to submit:

- Affected component and revision
- Step-by-step reproduction instructions or proof-of-concept
- Potential impact on workspace isolation, authorization, file quarantine, or audit integrity

The maintainers will acknowledge receipt within 48 hours and coordinate a fix and disclosure timeline.
