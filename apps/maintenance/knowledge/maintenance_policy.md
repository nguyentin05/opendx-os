# Equipment Maintenance Standard Operating Policy

## 1. Scope
This policy governs equipment alert response, preventive and corrective maintenance assignment, and final acceptance in the `maintenance` application.

## 2. OT Threshold & Work Creation Rules
- Over-temperature or vibration alerts emitted as `ot.event.accepted` are deduplicated by `(workspace_id, device_id, event_dedup_key)`.
- An open alert creates a shared `core.work_items` record and a corresponding `domain.maintenance_records` record in `OPEN` state.

## 3. Execution & Acceptance Rules
- Technicians holding `role_maintenance` start work via the `submit` command (`OPEN -> IN_PROGRESS`) and record findings in `resolution_note`.
- Only an authorized maintenance manager (`role_manager_maintenance`) may execute the `close` command (`IN_PROGRESS -> CLOSED`) after verifying the expected record `version`.
