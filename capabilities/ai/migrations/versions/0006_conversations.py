# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0006"
down_revision = "0005"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE cap_ai.conversations (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            owner_id uuid NOT NULL REFERENCES core.users(id),
            title text NOT NULL CHECK (length(title) BETWEEN 1 AND 160),
            created_at timestamptz NOT NULL DEFAULT now(),
            deleted_at timestamptz,
            summary text NOT NULL DEFAULT '',
            summary_sources jsonb NOT NULL DEFAULT '[]'
        );
        CREATE INDEX conversations_owner ON cap_ai.conversations(owner_id,id)
            WHERE deleted_at IS NULL;
        CREATE TABLE cap_ai.messages (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            conversation_id uuid NOT NULL REFERENCES cap_ai.conversations(id),
            seq int NOT NULL CHECK (seq>0),
            role text NOT NULL CHECK (role IN ('user','assistant')),
            content text NOT NULL,
            status text NOT NULL,
            client_turn_id uuid,
            lease_until timestamptz,
            response jsonb,
            sources jsonb NOT NULL DEFAULT '[]',
            UNIQUE (conversation_id,seq),
            UNIQUE (conversation_id,client_turn_id),
            CHECK ((role='user')=(client_turn_id IS NOT NULL)),
            CHECK ((role='assistant')=(response IS NOT NULL)),
            CHECK (lease_until IS NULL OR (role='user' AND status='RUNNING'))
        );
        CREATE UNIQUE INDEX running_conversation_turn ON cap_ai.messages(conversation_id)
            WHERE status='RUNNING';

        GRANT SELECT, INSERT ON cap_ai.conversations, cap_ai.messages TO dxos_ai_api;
        GRANT UPDATE (deleted_at,summary,summary_sources) ON cap_ai.conversations TO dxos_ai_api;
        GRANT UPDATE (status,lease_until) ON cap_ai.messages TO dxos_ai_api;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
