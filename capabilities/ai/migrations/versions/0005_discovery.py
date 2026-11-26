# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0005"
down_revision = "0004"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE FUNCTION cap_ai.actor_active(actor uuid, lock_user boolean)
        RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
        BEGIN
            IF lock_user THEN
                PERFORM 1 FROM core.users WHERE id=actor AND status='ACTIVE' FOR SHARE;
            ELSE
                PERFORM 1 FROM core.users WHERE id=actor AND status='ACTIVE';
            END IF;
            RETURN FOUND;
        END $$;

        CREATE FUNCTION cap_ai.can_search_source(actor uuid, role text, doc uuid, ver uuid)
        RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT CASE WHEN role IS NOT NULL THEN cap_ai.source_access(actor,role,doc,ver,false)
            ELSE EXISTS (
                SELECT FROM core.user_roles r
                WHERE r.user_id=actor AND cap_ai.source_access(actor,r.role_name,doc,ver,false)
            ) END
        $$;
        REVOKE ALL ON FUNCTION cap_ai.actor_active(uuid,boolean),
            cap_ai.can_search_source(uuid,text,uuid,uuid) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION cap_ai.actor_active(uuid,boolean),
            cap_ai.can_search_source(uuid,text,uuid,uuid) TO dxos_ai_api, dxos_ai_worker;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
