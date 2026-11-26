-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- An operator creates an account through dxos-authz (POST /api/auth/users). The
-- identity provider creates it first; this records it at once, so it can hold
-- roles and a department before its first sign-in, and audits who created it.
CREATE OR REPLACE FUNCTION core.create_user(p_actor UUID, p_acting_role TEXT, p_user UUID, p_email TEXT, p_name TEXT)
RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
BEGIN
    IF session_user <> 'dxos_authz' THEN
        RAISE EXCEPTION 'untrusted database connection for account administration';
    END IF;
    -- The identity provider keeps emails unique, so another row with this
    -- address is a stale mirror (as in dxos-authz Mirror); release it.
    UPDATE core.users SET email = id::text || '@released.invalid'
     WHERE lower(email) = lower(p_email) AND id <> p_user;
    INSERT INTO core.users (id, email, display_name) VALUES (p_user, p_email, p_name);
    INSERT INTO core.audit_logs (actor_id, acting_role, action, entity_type, entity_id, payload)
    VALUES (p_actor, p_acting_role, 'CREATE_USER', 'core.users', p_user, jsonb_build_object('email', p_email));
END;
$$;
REVOKE ALL ON FUNCTION core.create_user(UUID, TEXT, UUID, TEXT, TEXT) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'dxos_authz') THEN
        GRANT EXECUTE ON FUNCTION core.create_user(UUID, TEXT, UUID, TEXT, TEXT) TO dxos_authz;
    END IF;
END $$;
