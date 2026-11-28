# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os
from subprocess import run
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text

from dxos_ai.database import postgres_url
from dxos_ai.main import create_app


def test_fresh_and_legacy_bootstrap() -> None:
    migration_url = os.environ.get("AI_MIGRATION_DATABASE_URL")
    if not migration_url:
        pytest.skip("requires the disposable AI Compose deployment")

    url = postgres_url(migration_url)
    assert url.database and url.database.endswith("_test"), "requires an isolated test database"
    with TestClient(create_app()) as client:
        assert client.get("/healthz").status_code == 200
        assert client.get("/readyz").status_code == 200

    engine = create_engine(url, isolation_level="AUTOCOMMIT")
    legacy = "ai_legacy_test_" + uuid4().hex
    with engine.connect() as admin:
        assert dict(admin.execute(text("""
            SELECT extname, extversion FROM pg_extension WHERE extname IN ('vector','pgmq')
        """)).all()) == {"pgmq": "1.13.2", "vector": "0.8.7"}
        admin.execute(text(f"CREATE DATABASE {legacy}"))

        try:
            legacy_url = url.set(database=legacy)
            legacy_engine = create_engine(legacy_url)
            with legacy_engine.begin() as conn:
                conn.execute(text("CREATE SCHEMA core"))
                conn.execute(text("CREATE TABLE core.schema_migrations(scope text)"))
                conn.execute(text("INSERT INTO core.schema_migrations VALUES ('cap-ai')"))
                conn.execute(text("CREATE TABLE core.files(marker text)"))
                conn.execute(text("INSERT INTO core.files VALUES ('original')"))
            result = run(
                ["alembic", "upgrade", "head"],
                env={
                    **os.environ,
                    "AI_MIGRATION_DATABASE_URL": legacy_url.render_as_string(hide_password=False),
                },
                capture_output=True,
                text=True,
                check=False,
            )
            assert result.returncode and "Legacy AI baseline detected" in result.stderr
            with legacy_engine.connect() as conn:
                assert conn.scalar(text("SELECT to_regnamespace('cap_ai')")) is None
                assert conn.scalar(text("SELECT marker FROM core.files")) == "original"
        finally:
            legacy_engine.dispose()
            admin.execute(text(f"DROP DATABASE {legacy}"))
