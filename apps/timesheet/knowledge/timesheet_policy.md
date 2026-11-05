# Employee Timesheet & Daily Hours Policy

## 1. Contextual Privacy & Isolation
- Users holding `role_timesheet` may view `core.employees(id, workspace_id, full_name, department, work_shift)` and traverse the `employee -> timesheet_entries` relationship.
- `role_timesheet` has no access to `domain.maintenance_records` or `domain.procurement_requests`.

## 2. Poka-Yoke Validation Rules
- Every timesheet entry (`domain.timesheet_entries`) must satisfy `logged_hours > 0 AND logged_hours <= 24`.
- Total approved/submitted hours per employee per `work_date` across a workspace must not exceed `24.00` hours.
- Timesheet approval (`SUBMITTED -> APPROVED`) is independent of procurement or maintenance status transitions.
