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

-- Tier 2 capability: cap-hr ([H])
-- Organization structure shared by every application: departments and the
-- department and position of each user. One set for the whole organization;
-- applications that need "own department" scoping declare cap-hr and read it.

CREATE SCHEMA IF NOT EXISTS cap_hr;

-- 1. Departments ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cap_hr.org_units (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES cap_hr.org_units(id) ON DELETE RESTRICT,
    code TEXT NOT NULL UNIQUE CHECK (length(trim(code)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_org_units_not_own_parent CHECK (parent_id IS DISTINCT FROM id)
);

CREATE INDEX IF NOT EXISTS idx_org_units_parent ON cap_hr.org_units (parent_id);

CREATE OR REPLACE FUNCTION cap_hr.prevent_org_unit_cycle()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NOT NULL AND EXISTS (
        WITH RECURSIVE ancestors(id) AS (
            SELECT NEW.parent_id
            UNION
            SELECT u.parent_id FROM cap_hr.org_units u JOIN ancestors a ON u.id = a.id
             WHERE u.parent_id IS NOT NULL
        )
        SELECT 1 FROM ancestors WHERE id = NEW.id
    ) THEN
        RAISE EXCEPTION 'department % cannot be its own ancestor', NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_org_units_no_cycle ON cap_hr.org_units;
CREATE TRIGGER trg_org_units_no_cycle
BEFORE INSERT OR UPDATE OF parent_id ON cap_hr.org_units
FOR EACH ROW EXECUTE FUNCTION cap_hr.prevent_org_unit_cycle();

-- 2. Staff ------------------------------------------------------------------
-- Each user belongs to exactly one department and holds one position.
CREATE TABLE IF NOT EXISTS cap_hr.staff (
    user_id UUID PRIMARY KEY REFERENCES core.users(id) ON DELETE CASCADE,
    org_unit_id UUID NOT NULL REFERENCES cap_hr.org_units(id) ON DELETE RESTRICT,
    position TEXT CHECK (position IS NULL OR length(trim(position)) > 0),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_staff_org_unit ON cap_hr.staff (org_unit_id);

-- 3. Department of a record ---------------------------------------------------
-- Department of a user, for "own department" scoping and record defaults.
CREATE OR REPLACE FUNCTION cap_hr.user_org_unit(p_user UUID)
RETURNS UUID LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, cap_hr AS $$
    SELECT org_unit_id FROM cap_hr.staff WHERE user_id = p_user
$$;

-- Applications that declare cap-hr get an org_unit_id column on their domain
-- tables; the operator attaches this trigger after core.stamp_record_owner so
-- a new record belongs to its creator's department unless the caller names one.
CREATE OR REPLACE FUNCTION cap_hr.stamp_record_org_unit()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_hr, core AS $$
BEGIN
    IF NEW.org_unit_id IS NULL THEN
        NEW.org_unit_id := cap_hr.user_org_unit(NEW.created_by);
    END IF;
    IF NEW.org_unit_id IS NULL THEN
        RAISE EXCEPTION 'user % has no department; assign one in cap_hr.staff', NEW.created_by;
    END IF;
    RETURN NEW;
END;
$$;

-- 4. Register cap-hr ----------------------------------------------------------
INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-hr', 'Organization & HR', 'H', '/api/cap/hr', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();

-- 5. Administration -----------------------------------------------------------
-- The role that maintains departments and staff. It belongs to this capability:
-- it is in force only while cap-hr is enabled.
INSERT INTO core.roles (name, cap_id, description)
VALUES ('role_hr_admin', 'cap-hr', 'Maintains departments and staff')
ON CONFLICT (name) DO NOTHING;

-- cap-hr changes departments and staff only through these functions, so its
-- connection needs no write access to the tables and every change is audited
-- in the 'core' chain in the same transaction. p_actor is the administrator
-- and p_acting_role the role they act as.
CREATE OR REPLACE FUNCTION cap_hr.assert_service_connection()
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    IF session_user <> 'dxos_hr' THEN
        RAISE EXCEPTION 'untrusted database connection for HR administration';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION cap_hr.create_org_unit(p_actor UUID, p_acting_role TEXT, p_code TEXT, p_name TEXT, p_parent UUID)
RETURNS UUID LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_hr, core, public AS $$
DECLARE
    v_id UUID;
BEGIN
    PERFORM cap_hr.assert_service_connection();
    INSERT INTO cap_hr.org_units (code, name, parent_id) VALUES (trim(p_code), trim(p_name), p_parent) RETURNING id INTO v_id;
    INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
    VALUES (p_actor, p_acting_role, 'CREATE_ORG_UNIT', 'cap_hr.org_units', v_id,
            jsonb_build_object('code', trim(p_code), 'name', trim(p_name), 'parent_id', p_parent));
    RETURN v_id;
END;
$$;

-- Replaces a department's code, name and parent; a NULL parent makes it a root.
CREATE OR REPLACE FUNCTION cap_hr.update_org_unit(p_actor UUID, p_acting_role TEXT, p_id UUID, p_code TEXT, p_name TEXT, p_parent UUID)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_hr, core, public AS $$
DECLARE
    v_rows INTEGER;
BEGIN
    PERFORM cap_hr.assert_service_connection();
    UPDATE cap_hr.org_units SET code = trim(p_code), name = trim(p_name), parent_id = p_parent
     WHERE id = p_id AND (code, name, parent_id) IS DISTINCT FROM (trim(p_code), trim(p_name), p_parent);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows = 1 THEN
        INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
        VALUES (p_actor, p_acting_role, 'UPDATE_ORG_UNIT', 'cap_hr.org_units', p_id,
                jsonb_build_object('code', trim(p_code), 'name', trim(p_name), 'parent_id', p_parent));
    END IF;
    RETURN v_rows = 1;
END;
$$;

-- Places a user in a department with a position. A user has one department,
-- so assigning again moves them; records they created keep their department.
CREATE OR REPLACE FUNCTION cap_hr.assign_staff(p_actor UUID, p_acting_role TEXT, p_user UUID, p_org_unit UUID, p_position TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_hr, core, public AS $$
DECLARE
    v_previous UUID;
    v_position TEXT := nullif(trim(coalesce(p_position, '')), '');
    v_rows INTEGER;
BEGIN
    PERFORM cap_hr.assert_service_connection();
    SELECT org_unit_id INTO v_previous FROM cap_hr.staff WHERE user_id = p_user FOR UPDATE;
    INSERT INTO cap_hr.staff (user_id, org_unit_id, position) VALUES (p_user, p_org_unit, v_position)
    ON CONFLICT (user_id) DO UPDATE SET org_unit_id = EXCLUDED.org_unit_id, position = EXCLUDED.position
     WHERE (cap_hr.staff.org_unit_id, cap_hr.staff.position) IS DISTINCT FROM (EXCLUDED.org_unit_id, EXCLUDED.position);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows = 1 THEN
        INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
        VALUES (p_actor, p_acting_role, 'ASSIGN_STAFF', 'cap_hr.staff', p_user,
                jsonb_build_object('org_unit_id', p_org_unit, 'position', v_position, 'previous_org_unit_id', v_previous));
    END IF;
    RETURN v_rows = 1;
END;
$$;

CREATE OR REPLACE FUNCTION cap_hr.remove_staff(p_actor UUID, p_acting_role TEXT, p_user UUID)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_hr, core, public AS $$
DECLARE
    v_previous UUID;
BEGIN
    PERFORM cap_hr.assert_service_connection();
    DELETE FROM cap_hr.staff WHERE user_id = p_user RETURNING org_unit_id INTO v_previous;
    IF v_previous IS NULL THEN
        RETURN false;
    END IF;
    INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
    VALUES (p_actor, p_acting_role, 'REMOVE_STAFF', 'cap_hr.staff', p_user, jsonb_build_object('previous_org_unit_id', v_previous));
    RETURN true;
END;
$$;

REVOKE ALL ON FUNCTION cap_hr.create_org_unit(UUID, TEXT, TEXT, TEXT, UUID) FROM PUBLIC;
REVOKE ALL ON FUNCTION cap_hr.update_org_unit(UUID, TEXT, UUID, TEXT, TEXT, UUID) FROM PUBLIC;
REVOKE ALL ON FUNCTION cap_hr.assign_staff(UUID, TEXT, UUID, UUID, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION cap_hr.remove_staff(UUID, TEXT, UUID) FROM PUBLIC;
