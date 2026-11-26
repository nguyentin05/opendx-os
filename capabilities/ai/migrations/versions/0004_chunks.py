# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0004"
down_revision = "0003"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE cap_ai.document_chunks (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            version_id uuid NOT NULL REFERENCES cap_ai.document_versions(id),
            ordinal int NOT NULL,
            body text NOT NULL,
            provenance jsonb NOT NULL,
            search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED,
            model_revision uuid,
            dimension int,
            embedding public.vector,
            CHECK ((embedding IS NULL) = (model_revision IS NULL)),
            CHECK ((embedding IS NULL) = (dimension IS NULL)),
            CHECK (embedding IS NULL OR dimension = public.vector_dims(embedding)),
            UNIQUE(version_id, ordinal)
        );
        CREATE INDEX chunks_text ON cap_ai.document_chunks USING gin(search_vector);
        CREATE INDEX ingestion_dispatch_due ON cap_ai.ingestion_jobs(next_dispatch_at)
            WHERE state IN ('PENDING','INDEXING');
        CREATE UNIQUE INDEX ingestion_delegation ON cap_ai.ingestion_jobs(delegation_hash)
            WHERE delegation_hash IS NOT NULL;
        GRANT SELECT ON cap_ai.document_chunks TO dxos_ai_api, dxos_ai_worker;
        GRANT INSERT ON cap_ai.document_chunks TO dxos_ai_worker;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
