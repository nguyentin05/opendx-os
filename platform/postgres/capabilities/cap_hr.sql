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
