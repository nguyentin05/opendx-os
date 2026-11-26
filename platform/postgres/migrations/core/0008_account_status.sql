-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- dxos-authz mirrors the identity provider: an account disabled or deleted in
-- Keycloak becomes DISABLED here (never deleted: records and audit refer to it),
-- and dxos-authz refuses it; an account enabled again becomes ACTIVE. Platform
-- service accounts (@dxos.internal, created by 03_app_registry.sql, not all of
-- them in Keycloak) are never changed. Each change is audited with the account
-- itself as actor and the identity provider as acting role.
CREATE OR REPLACE FUNCTION core.mirror_account_status(p_user UUID, p_enabled BOOLEAN)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
DECLARE
    v_status TEXT := CASE WHEN p_enabled THEN 'ACTIVE' ELSE 'DISABLED' END;
    v_rows INTEGER;
BEGIN
    IF session_user <> 'dxos_authz' THEN
        RAISE EXCEPTION 'untrusted database connection for account status';
    END IF;
    UPDATE core.users SET status = v_status
     WHERE id = p_user AND status <> v_status AND lower(email) NOT LIKE '%@dxos.internal';
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows = 1 THEN
        INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
        VALUES (p_user, 'identity_provider', CASE WHEN p_enabled THEN 'ENABLE_ACCOUNT' ELSE 'DISABLE_ACCOUNT' END,
                'core.users', p_user, jsonb_build_object('status', v_status));
    END IF;
    RETURN v_rows = 1;
END;
$$;
REVOKE ALL ON FUNCTION core.mirror_account_status(UUID, BOOLEAN) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'dxos_authz') THEN
        GRANT EXECUTE ON FUNCTION core.mirror_account_status(UUID, BOOLEAN) TO dxos_authz;
    END IF;
END $$;
