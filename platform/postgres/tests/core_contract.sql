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

-- Behavioral SQL assertions for the Tier 1 kernel and cap-process.
-- Model: one organization per installation; data is labelled by application;
-- departments live in cap_hr (see capabilities_contract.sql).
DO $$
DECLARE
    user_an UUID := '22222222-2222-4222-8222-222222222222';
    user_chi UUID := '77777777-7777-4777-8777-777777777777';
    entity_a UUID := '33333333-3333-4333-8333-333333333333';
    file_a UUID := '44444444-4444-4444-8444-444444444444';
    event_a UUID;
    v_hash TEXT;
    v_prev TEXT;
    v_seq BIGINT;
BEGIN
    -- 1. Users: id comes from the identity provider, email is unique
    BEGIN
        INSERT INTO core.users (email, display_name) VALUES ('nobody@example.local', 'Nobody');
        RAISE EXCEPTION 'user without an OIDC subject was not rejected';
    EXCEPTION WHEN not_null_violation THEN
        -- Expected
    END;

    INSERT INTO core.users (id, email, display_name) VALUES (user_an, 'an@example.local', 'An') ON CONFLICT DO NOTHING;
    INSERT INTO core.users (id, email, display_name) VALUES (user_chi, 'chi@example.local', 'Chi') ON CONFLICT DO NOTHING;

    BEGIN
        INSERT INTO core.users (id, email, display_name) VALUES (gen_random_uuid(), 'AN@example.local', 'Duplicate');
        RAISE EXCEPTION 'case-insensitive duplicate email was not rejected';
    EXCEPTION WHEN unique_violation THEN
        -- Expected
    END;

    -- 2. Applications are never deleted
    INSERT INTO core.apps (app_id, display_name, version, spec, status)
    VALUES ('contract_app', 'Contract App', '0.1.0', '{"role":"role_contract_buyer"}', 'Ready') ON CONFLICT DO NOTHING;

    BEGIN
        DELETE FROM core.apps WHERE app_id = 'contract_app';
        RAISE EXCEPTION 'application delete was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%status to Removed%' THEN RAISE; END IF;
    END;

    -- 3. Roles have at most one owner; assignments are unique
    BEGIN
        INSERT INTO core.roles (name, app_id, cap_id) VALUES ('role_two_owners', 'contract_app', 'cap-process');
        RAISE EXCEPTION 'role with two owners was not rejected';
    EXCEPTION WHEN check_violation THEN
        -- Expected
    END;

    INSERT INTO core.roles (name, app_id) VALUES ('role_contract_buyer', 'contract_app') ON CONFLICT DO NOTHING;
    INSERT INTO core.user_roles (user_id, role_name) VALUES (user_an, 'role_contract_buyer');
    BEGIN
        INSERT INTO core.user_roles (user_id, role_name) VALUES (user_an, 'role_contract_buyer');
        RAISE EXCEPTION 'duplicate role assignment was not rejected';
    EXCEPTION WHEN unique_violation THEN
        -- Expected
    END;

    -- 4. Applications only use enabled capabilities, which then stay enabled
    INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
    VALUES ('cap-contract-off', 'Disabled capability', 'D', '/api/cap/off', false) ON CONFLICT DO NOTHING;
    BEGIN
        INSERT INTO core.app_capabilities (app_id, cap_id) VALUES ('contract_app', 'cap-contract-off');
        RAISE EXCEPTION 'dependency on a disabled capability was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%not enabled%' THEN RAISE; END IF;
    END;

    INSERT INTO core.app_capabilities (app_id, cap_id) VALUES ('contract_app', 'cap-process');
    BEGIN
        UPDATE core.capabilities SET enabled = false WHERE cap_id = 'cap-process';
        RAISE EXCEPTION 'disabling a capability in use was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%used by installed applications%' THEN RAISE; END IF;
    END;

    -- 5. Files belong to a known application and keep immutable metadata
    BEGIN
        INSERT INTO core.files (app_id, bucket, object_key, file_name, sha256_hash, uploaded_by, mime_type, size_bytes)
        VALUES ('missing_app', 'dxos-bucket', 'missing/file', 'x.pdf', repeat('b', 64), user_an, 'application/pdf', 10);
        RAISE EXCEPTION 'file of an unknown application was not rejected';
    EXCEPTION WHEN foreign_key_violation THEN
        -- Expected
    END;

    INSERT INTO core.files (id, app_id, bucket, object_key, file_name, sha256_hash, uploaded_by, mime_type, size_bytes)
    VALUES (file_a, 'contract_app', 'dxos-bucket', 'contract_app/file-a', 'quote.pdf', repeat('b', 64), user_an, 'application/pdf', 1024);

    BEGIN
        UPDATE core.files SET sha256_hash = repeat('c', 64) WHERE id = file_a;
        RAISE EXCEPTION 'file hash mutation was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%immutable%' THEN RAISE; END IF;
    END;
    UPDATE core.files SET scan_status = 'CLEAN', scanned_at = now() WHERE id = file_a;

    -- 6. Outbox and cap-process receipts are scoped per application
    INSERT INTO core.outbox_events (app_id, event_type, entity_type, entity_id, entity_version)
    VALUES ('contract_app', 'submitted', 'domain.contract_rows', entity_a, 1) RETURNING id INTO event_a;

    INSERT INTO core.apps (app_id, display_name, version, spec, status)
    VALUES ('contract_other', 'Other App', '0.1.0', '{}', 'Ready') ON CONFLICT DO NOTHING;
    INSERT INTO cap_process.command_receipts (actor_id, app_id, entity_id, command, idempotency_key, payload_hash, response_status, response_body)
    VALUES (user_an, 'contract_app', entity_a, 'submit', 'key-1', repeat('a', 64), 200, '{}'::jsonb);
    INSERT INTO cap_process.command_receipts (actor_id, app_id, entity_id, command, idempotency_key, payload_hash, response_status, response_body)
    VALUES (user_an, 'contract_other', entity_a, 'submit', 'key-1', repeat('a', 64), 200, '{}'::jsonb);
    INSERT INTO cap_process.processed_workflow_steps (event_id, step_name, app_id, result, outcome)
    VALUES (event_a, 'notify', 'contract_app', '{}'::jsonb, 'done');

    -- 7. Audit: one hash chain per application, a separate core chain
    BEGIN
        INSERT INTO core.audit_logs (app_id, actor_id, action, entity_type, entity_id)
        VALUES ('contract_app', gen_random_uuid(), 'TEST', 'domain.contract_rows', entity_a);
        RAISE EXCEPTION 'audit row with an unknown actor was not rejected';
    EXCEPTION WHEN foreign_key_violation THEN
        -- Expected
    END;

    INSERT INTO core.audit_logs (app_id, actor_id, acting_role, action, entity_type, entity_id, entity_version)
    VALUES ('contract_app', user_an, 'role_contract_buyer', 'SUBMIT', 'domain.contract_rows', entity_a, 1);
    INSERT INTO core.audit_logs (app_id, actor_id, acting_role, action, entity_type, entity_id, entity_version)
    VALUES ('contract_app', user_chi, 'role_auditor', 'REVIEW', 'domain.contract_rows', entity_a, 2);
    INSERT INTO core.audit_logs (actor_id, action, entity_type, entity_id)
    VALUES (user_chi, 'UPDATE', 'cap_hr.staff', user_an);

    SELECT row_hash INTO v_hash FROM core.audit_logs WHERE chain = 'contract_app' AND seq = 1;
    SELECT prev_hash INTO v_prev FROM core.audit_logs WHERE chain = 'contract_app' AND seq = 2;
    IF length(v_hash) <> 64 OR v_prev IS DISTINCT FROM v_hash THEN
        RAISE EXCEPTION 'application audit chain is not linked';
    END IF;
    SELECT seq INTO v_seq FROM core.audit_logs WHERE chain = 'core' AND entity_type = 'cap_hr.staff';
    IF v_seq <> 1 THEN
        RAISE EXCEPTION 'changes outside an application must start the core chain, got seq %', v_seq;
    END IF;

    BEGIN
        DELETE FROM core.audit_logs WHERE chain = 'contract_app';
        RAISE EXCEPTION 'audit deletion was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%append-only%' THEN RAISE; END IF;
    END;

    -- 8. Runtime actor is only read from trusted connections
    PERFORM set_config('dxos.actor_id', user_an::text, true);
    IF core.session_actor_id() IS NOT NULL THEN
        RAISE EXCEPTION 'session actor accepted from an untrusted connection';
    END IF;
END;
$$;
