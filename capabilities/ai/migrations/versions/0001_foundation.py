# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("REVOKE ALL ON SCHEMA cap_ai FROM PUBLIC")
    op.execute("GRANT USAGE ON SCHEMA cap_ai TO dxos_ai_api, dxos_ai_worker")
    op.execute("GRANT SELECT ON cap_ai.alembic_version TO dxos_ai_api, dxos_ai_worker")


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
