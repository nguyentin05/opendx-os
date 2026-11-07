# Material Procurement & Vendor Quote Policy

## 1. Quote Attachment & Malware Quarantine Rules
- All vendor quotes uploaded to `core.file_objects` start in `QUARANTINED` state.
- A procurement request (`domain.procurement_requests`) cannot be submitted (`submit`) or sent to AI OCR unless its attached `quote_file_id` has been scanned by ClamAV and marked `CLEAN`.

## 2. Version-Guarded Approval Rules
- Editing `vendor_name`, `item_description`, or `quoted_amount` in `DRAFT` state increments `version` via the database trigger.
- Managers holding `role_manager_procurement` must supply `expected_version` when executing `approve` or `reject`. If the record was modified after the manager opened it, the command is rejected with `409 Conflict`.
- Self-approval (requester approving their own request) is prohibited.

## Backend demo limits

C0 commands accept an empty payload only; rejection_reason is not mutated by reject.
Requester identity is preset from signed user ID. The quote column is nullable during draft creation;
submit/approve require a present CLEAN quote in the implemented runtime. AI OCR, policy summaries and BI are
unavailable; cap-ai/cap-data are intentionally not dependencies of this demo.
The review workflow uses the configured signed workflow principal, persisted
source event and declared create_review mapping, never caller-supplied fields.
