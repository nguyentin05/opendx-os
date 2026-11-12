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

-- Behavioral SQL assertions for cap-hr and cap-work. Requires the core
-- contract fixtures (core_contract.sql) and both capabilities loaded.
CREATE TABLE domain.contract_scoped_rows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by UUID NOT NULL REFERENCES core.users(id),
    org_unit_id UUID NOT NULL REFERENCES cap_hr.org_units(id),
    status TEXT NOT NULL DEFAULT 'DRAFT',
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER t1_owner BEFORE INSERT ON domain.contract_scoped_rows
    FOR EACH ROW EXECUTE FUNCTION core.stamp_record_owner();
CREATE TRIGGER t2_org_unit BEFORE INSERT ON domain.contract_scoped_rows
    FOR EACH ROW EXECUTE FUNCTION cap_hr.stamp_record_org_unit();
CREATE TRIGGER t3_version BEFORE UPDATE ON domain.contract_scoped_rows
    FOR EACH ROW EXECUTE FUNCTION core.bump_domain_record_version();

DO $$
DECLARE
    user_an UUID := '22222222-2222-4222-8222-222222222222';
    user_chi UUID := '77777777-7777-4777-8777-777777777777';
    user_new UUID := '99999999-9999-4999-8999-999999999999';
    unit_kho UUID;
    unit_ketoan UUID;
    unit_team UUID;
    row_a UUID;
    v_unit UUID;
BEGIN
    -- 1. Department tree must not contain cycles
    INSERT INTO cap_hr.org_units (code, name) VALUES ('KHO', 'Phong Kho') RETURNING id INTO unit_kho;
    INSERT INTO cap_hr.org_units (code, name) VALUES ('KETOAN', 'Phong Ke toan') RETURNING id INTO unit_ketoan;
    INSERT INTO cap_hr.org_units (code, name, parent_id) VALUES ('KHO_NHAP', 'To Nhap hang', unit_kho) RETURNING id INTO unit_team;
    BEGIN
        UPDATE cap_hr.org_units SET parent_id = unit_team WHERE id = unit_kho;
        RAISE EXCEPTION 'department cycle was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%own ancestor%' THEN RAISE; END IF;
    END;

    -- 2. One department per user
    INSERT INTO cap_hr.staff (user_id, org_unit_id, position) VALUES (user_an, unit_kho, 'Nhan vien kho');
    INSERT INTO cap_hr.staff (user_id, org_unit_id, position) VALUES (user_chi, unit_ketoan, 'Ke toan truong');
    BEGIN
        INSERT INTO cap_hr.staff (user_id, org_unit_id) VALUES (user_an, unit_ketoan);
        RAISE EXCEPTION 'second department for a user was not rejected';
    EXCEPTION WHEN unique_violation THEN
        -- Expected
    END;
    IF cap_hr.user_org_unit(user_chi) IS DISTINCT FROM unit_ketoan THEN
        RAISE EXCEPTION 'user_org_unit returned the wrong department';
    END IF;

    -- 3. A department-scoped record belongs to its creator's department
    INSERT INTO domain.contract_scoped_rows (created_by) VALUES (user_an) RETURNING id, org_unit_id INTO row_a, v_unit;
    IF v_unit IS DISTINCT FROM unit_kho THEN
        RAISE EXCEPTION 'record department was not taken from its creator: %', v_unit;
    END IF;

    INSERT INTO core.users (id, email, display_name) VALUES (user_new, 'new@example.local', 'New') ON CONFLICT DO NOTHING;
    BEGIN
        INSERT INTO domain.contract_scoped_rows (created_by) VALUES (user_new);
        RAISE EXCEPTION 'record by a user without a department was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%has no department%' THEN RAISE; END IF;
    END;

    BEGIN
        UPDATE domain.contract_scoped_rows SET org_unit_id = unit_ketoan WHERE id = row_a;
        RAISE EXCEPTION 'moving a record to another department was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%immutable%' THEN RAISE; END IF;
    END;
    UPDATE domain.contract_scoped_rows SET status = 'SUBMITTED' WHERE id = row_a;

    -- 4. Work items: need an origin and an actor, can be assigned to anyone
    BEGIN
        INSERT INTO cap_work.work_items (title, created_by) VALUES ('No origin', user_an);
        RAISE EXCEPTION 'work item without an application or source was not rejected';
    EXCEPTION WHEN check_violation THEN
        -- Expected
    END;

    BEGIN
        INSERT INTO cap_work.work_items (app_id, title) VALUES ('contract_app', 'No actor');
        RAISE EXCEPTION 'work item without an actor was not rejected';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM NOT LIKE '%missing authenticated actor context%' THEN RAISE; END IF;
    END;

    INSERT INTO cap_work.work_items (app_id, title, assignee_id, created_by)
    VALUES ('contract_app', 'Kiem tra ngan sach', user_chi, user_an);
    INSERT INTO cap_work.work_items (title, source_type, source_id, created_by)
    VALUES ('Sua may nen khi', 'cap_data.device', gen_random_uuid(), user_an);
END;
$$;

DROP TABLE domain.contract_scoped_rows;
