---
name: hpdi-capability
description: Use when designing, implementing, or reviewing H-P-D-I capabilities in capabilities/, including commands/files, OT/export/BI, and OCR/RAG.
---

# Shared H-P-D-I capabilities

Read [architecture](../../memory/architecture.md), the [data contracts](../../memory/data-contracts.md), and the relevant acceptance groups in [Rule 03](../../rules/03-anti-patterns-and-security.md). Each capability has a health endpoint, registry metadata, and identity/permission checks at the service layer.

## Interface and contract sources

| Capability | Logical interface | Contract parts to apply |
| --- | --- | --- |
| `cap-process` | `POST /api/cap/process/files/upload`, `POST /api/cap/process/commands/{app_id}/{command}` | Field ownership, version/command/audit/retry, file lifecycle |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export`, `GET /api/cap/data/lakehouse/analytics` | Device identity, standard events, terminal states, reconciled batches, and the full source key |
| `cap-ai` | `POST /api/cap/ai/extract`, `POST /api/cap/ai/rag` | File permissions, clean status, OCR schema, workspace/app/caller permissions for RAG sources |

The routes above are a design contract. During implementation, settle the request/response schema and service identity before connecting consumers; do not treat the route table as a running API.

## How to do it

1. Identify the capability, the assigned behavior, and the affected S01–S08 groups. Read business fields, state machine, attachments, and workflow bindings from the app configuration.
2. Keep permission checks and data writes in a transaction that matches the contract. Commands let the trigger increment the version; receipt, audit, and durable events commit together. Non-DB side effects must be deduplicated on retry.
3. OT emits standard events for app workflows to process; BI uses the full workspace/entity type/ID/version. Do not add app-specific branches or reduce the source key to just an ID.
4. When a capability is disabled, return a clear unavailable status to consumers; independent parts of the system keep working. OCR/RAG must not use the service's permissions to exceed the caller's permissions.

## Verification example

Two commands with different keys but the same `expected_version`: only one transition succeeds. Retrying with the exact key/payload of a committed command returns the old result without another version/audit/effect; current permissions are still checked. Add a BI case with two entity types sharing a UUID to prove they are not merged.

Hand off the contract and verification scenarios during design; during implementation, add actual test results per the [gate](../../hooks/pre-pr-gate.md). Do not use an HTTP call after commit as evidence that an event was durably delivered.
