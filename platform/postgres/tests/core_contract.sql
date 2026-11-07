-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- Behavioral SQL assertions for core schema and capability contracts (Task B1)
DO $$
DECLARE
    ws_a UUID := '11111111-1111-4111-8111-111111111111';
    ws_b UUID := '55555555-5555-4555-8555-555555555555';
    emp_a UUID := '22222222-2222-4222-8222-222222222222';
    wi_a UUID := '33333333-3333-4333-8333-333333333333';
    file_a UUID := '44444444-4444-4444-8444-444444444444';
BEGIN
    INSERT INTO core.workspaces (id, code, name) VALUES (ws_a, 'ws-a', 'Workspace A') ON CONFLICT DO NOTHING;
    INSERT INTO core.workspaces (id, code, name) VALUES (ws_b, 'ws-b', 'Workspace B') ON CONFLICT DO NOTHING;
    INSERT INTO core.employees (id, workspace_id, full_name, email, department)
    VALUES (emp_a, ws_a, 'Alice', 'alice@ws-a.local', 'Ops') ON CONFLICT DO NOTHING;

    -- 1. Cross-workspace FK must be rejected
    BEGIN
        INSERT INTO core.work_items (id, workspace_id, title, description, priority, assigned_employee_id)
        VALUES (wi_a, ws_b, 'Cross WS', 'Must fail', 'MEDIUM', emp_a);
        RAISE EXCEPTION 'cross-workspace FK was not rejected';
    EXCEPTION WHEN foreign_key_violation THEN
        -- Expected
    END;

    -- 2. Valid same-workspace work item and receipt scope distinction across actors/apps
    INSERT INTO core.work_items (id, workspace_id, title, description, priority, assigned_employee_id)
    VALUES (wi_a, ws_a, 'Valid WS', 'Same workspace', 'MEDIUM', emp_a) ON CONFLICT DO NOTHING;

    INSERT INTO core.command_receipts (workspace_id, actor_id, app_id, entity_id, command, idempotency_key, payload_hash, response_status, response_body)
    VALUES (ws_a, emp_a::text, 'app_one', wi_a, 'submit', 'key-1', repeat('a', 64), 200, '{}'::jsonb);

    INSERT INTO core.command_receipts (workspace_id, actor_id, app_id, entity_id, command, idempotency_key, payload_hash, response_status, response_body)
    VALUES (ws_a, emp_a::text, 'app_two', wi_a, 'submit', 'key-1', repeat('a', 64), 200, '{}'::jsonb);

    -- 3. Immutable file metadata enforcement
    INSERT INTO core.file_objects (id, workspace_id, app_id, bucket, object_key, file_name, scan_status, sha256_hash, uploaded_by, mime_type, size_bytes)
    VALUES (file_a, ws_a, 'app_one', 'dxos-bucket', 'ws-a/app_one/file-a', 'quote.pdf', 'QUARANTINED', repeat('b', 64), emp_a, 'application/pdf', 1024);

    BEGIN
        UPDATE core.file_objects SET sha256_hash = repeat('c', 64) WHERE id = file_a;
        RAISE EXCEPTION 'file_objects sha256_hash mutation was not rejected';
    EXCEPTION WHEN raise_exception THEN
        -- Expected
    END;

    -- 4. Audit log append-only immutability
    INSERT INTO core.audit_logs (workspace_id, actor_id, action, entity_type, entity_id, entity_version, payload)
    VALUES (ws_a, emp_a::text, 'TEST', 'core.work_items', wi_a, 1, '{}'::jsonb);

    BEGIN
        DELETE FROM core.audit_logs WHERE workspace_id = ws_a;
        RAISE EXCEPTION 'audit_logs deletion was not rejected';
    EXCEPTION WHEN raise_exception THEN
        -- Expected
    END;
END;
$$;
