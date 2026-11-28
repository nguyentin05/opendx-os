# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0007"
down_revision = "0006"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE FUNCTION cap_ai.grant_eligible(document uuid, member uuid)
        RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT EXISTS (
                SELECT FROM cap_ai.documents d
                JOIN LATERAL cap_ai.package_source(d.app_id,d.source_id) s ON true
                JOIN core.users u ON u.id=member AND u.status='ACTIVE'
                JOIN core.user_roles ur ON ur.user_id=u.id
                JOIN core.roles r ON r.name=ur.role_name AND r.app_id=d.app_id
                WHERE d.id=document AND d.deleted_at IS NULL AND d.source_id IS NOT NULL
                  AND s #>> '{declaration,access,mode}'='RESTRICTED'
                  AND (s #> '{declaration,access,allowedRoles}') ? r.name
            )
        $$;

        REVOKE ALL ON FUNCTION cap_ai.grant_eligible(uuid,uuid) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION cap_ai.grant_eligible(uuid,uuid) TO dxos_ai_api;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
