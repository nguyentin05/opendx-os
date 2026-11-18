-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- These narrow functions expose only current chat identity and source tuples.
CREATE FUNCTION cap_ai.agent_chat_user_active(actor uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
 SELECT EXISTS (SELECT FROM core.users WHERE id = actor AND status = 'ACTIVE')
$$;

CREATE FUNCTION cap_ai.agent_authorized_scopes(actor uuid) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE result jsonb; total bigint;
BEGIN
 IF NOT cap_ai.agent_chat_user_active(actor) THEN
   RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
 END IF;
 WITH eligible AS (
   SELECT a.app_id, r.name AS acting_role, a.spec
   FROM core.users u JOIN core.user_roles ur ON ur.user_id = u.id
   JOIN core.roles r ON r.name = ur.role_name
   JOIN core.apps a ON a.app_id = r.app_id
   WHERE u.id = actor AND u.status = 'ACTIVE' AND a.status = 'Ready'
     AND EXISTS (SELECT FROM core.app_capabilities ac JOIN core.capabilities c ON c.cap_id = ac.cap_id WHERE ac.app_id = a.app_id AND c.cap_id = 'cap-ai' AND c.enabled)
     AND NOT EXISTS (SELECT FROM core.app_capabilities ac JOIN core.capabilities c ON c.cap_id = ac.cap_id WHERE ac.app_id = a.app_id AND NOT c.enabled)
 ), scopes AS (
   SELECT 'APP'::text AS kind, app_id, acting_role FROM eligible
   UNION ALL
   SELECT 'PERSONAL', app_id, acting_role FROM eligible
   WHERE spec #>> '{knowledge,version}' = '1' AND spec #>> '{knowledge,personalLibrary,enabled}' = 'true'
     AND (spec #> '{knowledge,personalLibrary,allowedRoles}') ? acting_role
 )
 SELECT count(*), coalesce(jsonb_agg(jsonb_build_object('kind', kind, 'app_id', app_id, 'acting_role', acting_role) ORDER BY kind, app_id, acting_role), '[]'::jsonb)
 INTO total, result FROM (SELECT * FROM scopes ORDER BY kind, app_id, acting_role LIMIT 257) bounded;
 IF total > 256 THEN RAISE EXCEPTION 'too many authorized scopes' USING ERRCODE = 'P0400'; END IF;
 RETURN result;
END $$;
ALTER FUNCTION cap_ai.agent_chat_user_active(uuid) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_authorized_scopes(uuid) OWNER TO dxos_ai_owner;
REVOKE ALL ON FUNCTION cap_ai.agent_chat_user_active(uuid), cap_ai.agent_authorized_scopes(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cap_ai.agent_chat_user_active(uuid), cap_ai.agent_authorized_scopes(uuid) TO dxos_ai_api;
