# Historical Native RAG design package — 2026-11-13

This directory preserves the user-provided source package. Its proposals and remote baseline are historical inputs, not accepted runtime contracts. Python/YAML input bytes are preserved under `.txt` reference suffixes (no project copyright is asserted); Markdown links to those files are adjusted; flat and nested manifest examples intentionally remain as historical evidence of the conflict.

Current decisions: [ADR 0007](../../../adr/0007-native-rag-identity-and-source-boundaries.md).
The reference validator can be run with `python3 scripts/validate_docs.py.txt`; parse the two YAML reference files explicitly with PyYAML.

Canonical proposed wire contract: [RAG API](../../../contracts/rag-api.proposed.md).
Canonical policy: [authorization](../../../architecture/rag-authorization-proposal.md).
Current evidence: [baseline](../../../architecture/rag-baseline.md).
The user approved a registered, owner-only personal library with no hosted processing, nested manifest serialization, conservative data preservation and current-DB membership checks. These decisions supersede conflicting suggestions in this archived package. No parser/provider selection is implied.
