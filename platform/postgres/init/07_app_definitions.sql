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

-- Which Tier 2 capabilities each application uses (many-to-many). Runs after
-- 04_capabilities.sh so capabilities are registered. An application can only
-- declare an enabled capability, and a capability cannot be disabled while an
-- installed application still uses it.
CREATE TABLE IF NOT EXISTS core.app_capabilities (
    app_id TEXT NOT NULL REFERENCES core.apps(app_id),
    cap_id TEXT NOT NULL REFERENCES core.capabilities(cap_id),
    PRIMARY KEY (app_id, cap_id)
);

CREATE INDEX IF NOT EXISTS idx_app_capabilities_cap ON core.app_capabilities (cap_id);

CREATE OR REPLACE FUNCTION core.require_enabled_capability()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM core.capabilities WHERE cap_id = NEW.cap_id AND enabled) THEN
        RAISE EXCEPTION 'application % requires capability %, which is not enabled', NEW.app_id, NEW.cap_id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_app_capabilities_enabled ON core.app_capabilities;
CREATE TRIGGER trg_app_capabilities_enabled
BEFORE INSERT OR UPDATE ON core.app_capabilities
FOR EACH ROW EXECUTE FUNCTION core.require_enabled_capability();

CREATE OR REPLACE FUNCTION core.prevent_disabling_used_capability()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_apps TEXT;
BEGIN
    IF OLD.enabled AND NOT NEW.enabled THEN
        SELECT string_agg(ac.app_id, ', ' ORDER BY ac.app_id) INTO v_apps
          FROM core.app_capabilities ac
          JOIN core.apps a ON a.app_id = ac.app_id
         WHERE ac.cap_id = NEW.cap_id AND a.status <> 'Removed';
        IF v_apps IS NOT NULL THEN
            RAISE EXCEPTION 'capability % is used by installed applications: %', NEW.cap_id, v_apps;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_capabilities_in_use ON core.capabilities;
CREATE TRIGGER trg_capabilities_in_use
BEFORE UPDATE OF enabled ON core.capabilities
FOR EACH ROW EXECUTE FUNCTION core.prevent_disabling_used_capability();

-- 06_runtime_roles.sql runs before this file, so its schema-wide grants do not
-- cover the table created here.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dxos_process')
       AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dxos_reader') THEN
        GRANT SELECT ON core.app_capabilities TO dxos_process, dxos_reader;
    END IF;
END $$;
