# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os

from alembic import context
from sqlalchemy import create_engine, text

from dxos_ai.bootstrap import bootstrap
from dxos_ai.database import postgres_url


def migrate() -> None:
    if context.is_offline_mode():
        raise RuntimeError("AI migration requires a live database for the baseline check")
    engine = create_engine(
        postgres_url(os.environ["AI_MIGRATION_DATABASE_URL"]),
        hide_parameters=True,
        connect_args={"connect_timeout": 5},
    )
    with engine.begin() as connection:
        connection.execute(text("SELECT pg_advisory_xact_lock(782634901)"))
        legacy = connection.scalar(text("""
            SELECT EXISTS (SELECT FROM core.schema_migrations WHERE scope = 'cap-ai')
                OR (to_regnamespace('cap_ai') IS NOT NULL
                    AND to_regclass('cap_ai.alembic_version') IS NULL)
        """))
        if legacy:
            raise RuntimeError(
                "Legacy AI baseline detected. Use a fresh database or coordinate an AI-only "
                "rebuild that preserves Core and original files."
            )
        # The Core init scripts run only on an empty volume, so an installation
        # that started without AI has no AI roles, extensions or queue. Create
        # them here, idempotently, before the schema needs them.
        bootstrap(connection)
        connection.execute(text("CREATE SCHEMA IF NOT EXISTS cap_ai AUTHORIZATION dxos_ai_owner"))
        connection.execute(text("SET LOCAL ROLE dxos_ai_owner"))
        context.configure(connection=connection, version_table_schema="cap_ai")
        with context.begin_transaction():
            context.run_migrations()


migrate()
