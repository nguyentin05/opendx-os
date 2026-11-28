# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""The AI bootstrap must be idempotent, so adding AI later needs no new volume.

Runs against a database named by TEST_DATABASE_URL (the CI PostgreSQL), with
the Core platform schemas provisioned and no AI roles present.
"""

import os

import pytest
from sqlalchemy import create_engine, text

from dxos_ai.bootstrap import BROKER_ROLE, QUEUE, bootstrap
from dxos_ai.database import postgres_url


@pytest.fixture
def database():
    dsn = os.environ.get("AI_BOOTSTRAP_TEST_DATABASE_URL") or os.environ.get("TEST_DATABASE_URL")
    if not dsn:
        pytest.skip("requires TEST_DATABASE_URL")
    url = postgres_url(dsn)
    assert url.database and url.database.endswith("_test"), "requires an isolated test database"
    engine = create_engine(url, isolation_level="AUTOCOMMIT")
    with engine.connect() as connection:
        available = set(connection.scalars(text(
            "SELECT name FROM pg_available_extensions WHERE name IN ('pgmq', 'vector')")))
    if available != {"pgmq", "vector"}:
        engine.dispose()
        pytest.skip("requires PostgreSQL with the pgvector and PGMQ extensions")
    try:
        yield engine
    finally:
        engine.dispose()


def test_bootstrap_is_idempotent(database, monkeypatch) -> None:
    monkeypatch.setenv("AI_BROKER_DATABASE_PASSWORD", "broker-secret")
    monkeypatch.setenv("AI_DATABASE_PASSWORD", "api-secret")

    with database.begin() as connection:
        # Start from an installation that never had AI: no roles, extensions or
        # capability row. Grants must go before the roles that hold them.
        connection.execute(text("DROP EXTENSION IF EXISTS pgmq CASCADE"))
        connection.execute(text("DROP EXTENSION IF EXISTS vector CASCADE"))
        for role in ("dxos_ai_api", "dxos_ai_worker", "dxos_ai_dispatcher", BROKER_ROLE):
            exists = connection.scalar(text(
                "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = :name)"), {"name": role})
            if exists:
                connection.execute(text(f"DROP OWNED BY {role}"))
                connection.execute(text(f"DROP ROLE {role}"))
        connection.execute(text(
            "UPDATE core.capabilities SET enabled = false WHERE cap_id = 'cap-ai'"))

    for _ in range(2):
        with database.begin() as connection:
            bootstrap(connection)

    with database.connect() as connection:
        extensions = dict(connection.execute(text(
            "SELECT extname, extversion FROM pg_extension WHERE extname IN ('pgmq', 'vector')"
        )).all())
        assert extensions == {"pgmq": "1.13.2", "vector": "0.8.7"}
        assert connection.scalar(text(
            "SELECT count(*) FROM pg_roles WHERE rolname IN ('dxos_ai_owner',"
            "'dxos_ai_api','dxos_ai_worker','dxos_ai_dispatcher','dxos_ai_broker')"
        )) == 5
        assert connection.scalar(text(
            "SELECT enabled AND base_route = '/api/cap/ai' FROM core.capabilities "
            "WHERE cap_id = 'cap-ai'"
        )) is True
        assert connection.scalar(text(
            "SELECT to_regclass('pgmq.q_" + QUEUE + "') IS NOT NULL"
        )) is True
        assert connection.scalar(text(
            f"SELECT has_table_privilege('{BROKER_ROLE}', 'pgmq.q_{QUEUE}', 'INSERT')"
        )) is True


def test_bootstrap_requires_the_broker_password(database, monkeypatch) -> None:
    monkeypatch.delenv("AI_BROKER_DATABASE_PASSWORD", raising=False)
    with database.begin() as connection, pytest.raises(
        RuntimeError, match="AI_BROKER_DATABASE_PASSWORD"
    ):
        bootstrap(connection)
