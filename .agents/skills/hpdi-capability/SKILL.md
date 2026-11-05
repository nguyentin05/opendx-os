---
name: hpdi-capability
description: Use when designing, implementing, or reviewing H-P-D-I capabilities in capabilities/, including commands/files, OT/export/BI, and OCR/RAG.
---

# Shared capabilities

Read the relevant [data contract](../../../docs/contracts/data-contracts.md) and
[acceptance groups](../../../docs/contracts/security-and-acceptance.md).

Use the [capability interfaces](../../../docs/contracts/data-contracts.md#9-capability-interfaces)
to locate the relevant handler and schema before connecting callers.

1. Identify affected acceptance groups and read the app's configured fields,
   state machine, attachments, and workflow bindings.
2. Keep permission checks, domain updates, receipts, audit, and durable events in
   the required transaction. Check non-DB effect deduplication separately.
3. Trace OT through standard events to declared workflows; trace BI through the
   full source identity and published export batches.
4. Exercise caller-scoped file/RAG access and unavailable-capability behavior,
   including callers that bypass the UI.

Useful cases: concurrent commands with the same version allow one transition;
committed retries return the recorded result after a current-permission check;
two entity types sharing a UUID stay distinct in BI. Use Go tests and the
integration checks described in [verification](../../../docs/development/verification.md).
