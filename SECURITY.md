# Security Policy

## Supported Versions

| Version | Supported |
| :--- | :--- |
| `main` (Unreleased) | Yes |

## Security Architecture Invariants

The intended security requirements and acceptance scenarios are defined in
[security and acceptance criteria](docs/contracts/security-and-acceptance.md),
with mechanisms in the [data contract](docs/contracts/data-contracts.md).
The implementation is under development; those requirements are not a claim
that all security properties have been verified in the running system.

## Reporting a Vulnerability

Please do **not** report security vulnerabilities through public GitHub issues.
Instead, use GitHub Private Vulnerability Reporting (Security Advisories) on the
repository to submit:

- Affected component and revision
- Step-by-step reproduction instructions or proof-of-concept
- Potential impact on workspace isolation, authorization, file quarantine, or audit integrity

The maintainers will acknowledge receipt within 48 hours and coordinate a fix and disclosure timeline.
