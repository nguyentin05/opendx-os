-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Hold current administrative authority through PERSONAL approval mutations.
-- The narrow row-lock privileges are supplied by migration 0011.
CREATE OR REPLACE FUNCTION cap_ai.agent_processing_admin(actor uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog,cap_ai AS $$
DECLARE active boolean;
BEGIN
  IF session_user <> 'dxos_authz' OR current_setting('role') NOT IN ('none','dxos_authz')
    OR EXISTS (SELECT FROM pg_roles WHERE rolname=session_user AND (rolsuper OR rolbypassrls OR rolcreaterole)) THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE='42501';
  END IF;
  SELECT u.status='ACTIVE' INTO active FROM core.users u WHERE u.id=actor FOR SHARE;
  IF NOT FOUND OR NOT coalesce(active,false) THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE='42501';
  END IF;
  PERFORM 1 FROM core.user_roles ur
    WHERE ur.user_id=actor AND ur.role_name='role_operator' FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE='42501';
  END IF;
END $$;
