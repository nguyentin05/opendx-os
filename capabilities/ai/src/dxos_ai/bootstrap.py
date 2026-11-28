# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Create what the AI runtime needs before Alembic runs.

The default PostgreSQL stack creates the Core roles and capability rows from
`docker-entrypoint-initdb.d`, which runs only on an empty volume. An
installation that started without AI therefore has neither the AI connection
roles, the pgvector and PGMQ extensions, nor the broker role and queue. This
step runs on every `ai-migrate`, as the administrator, and is idempotent so a
second run changes nothing.
"""

from __future__ import annotations

import os

from psycopg import sql
from sqlalchemy import text
from sqlalchemy.engine import Connection

# Roles the Core init scripts also create. `dxos_ai_owner` owns `cap_ai` and
# has no login: the services connect as dxos_ai_api, dxos_ai_worker and
# dxos_ai_dispatcher, which 06_runtime_roles.sql grants, and the broker has its
# own login for PGMQ only.
READER_ROLES = ("dxos_ai_owner", "dxos_ai_api", "dxos_ai_worker", "dxos_ai_dispatcher")
BROKER_ROLE = "dxos_ai_broker"
QUEUE = "dxos_ai_ingest"

# cap-ai must appear in core.capabilities for dxos-authz to authorize it. The
# init script inserts it from ENABLED_CAPABILITIES; repeat that here for an
# installation whose capabilities were seeded without AI.
ENABLE_CAPABILITY = """
    INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
    VALUES ('cap-ai', 'AI Assistant & Knowledge', 'I', '/api/cap/ai', true)
    ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now()
"""

GRANTS = """
    GRANT USAGE ON SCHEMA core TO dxos_ai_owner;
    GRANT SELECT (id, status), UPDATE (display_name) ON core.users TO dxos_ai_owner;
    GRANT SELECT (user_id, role_name), UPDATE (granted_at) ON core.user_roles TO dxos_ai_owner;
    GRANT SELECT (name, app_id, cap_id) ON core.roles TO dxos_ai_owner;
    GRANT SELECT (app_id, status, spec, publication_generation) ON core.apps TO dxos_ai_owner;
    GRANT SELECT (cap_id, enabled) ON core.capabilities TO dxos_ai_owner;
    GRANT SELECT (app_id, cap_id) ON core.app_capabilities TO dxos_ai_owner;
    GRANT SELECT (id, app_id, file_name, sha256_hash, size_bytes, mime_type, scan_status,
        uploaded_by, read_policy) ON core.files TO dxos_ai_owner;
    GRANT REFERENCES (id) ON core.users, core.files TO dxos_ai_owner;
    GRANT REFERENCES (app_id) ON core.apps TO dxos_ai_owner;
"""

# The login roles get their passwords in 08_runtime_passwords.sh on a fresh
# volume; set them again so an existing installation can authenticate.
LOGIN_PASSWORDS = {
    "dxos_ai_api": "AI_DATABASE_PASSWORD",
    "dxos_ai_worker": "AI_WORKER_DATABASE_PASSWORD",
    "dxos_ai_dispatcher": "AI_DISPATCHER_DATABASE_PASSWORD",
}


def _set_password(connection: Connection, role: str, password: str) -> None:
    """ALTER ROLE ... PASSWORD rejects bind parameters, so compose it safely.

    psycopg quotes the identifier and the literal, and the statement goes
    straight to the driver, so the password never appears in a logged query.
    """
    raw = connection.connection.driver_connection
    raw.execute(sql.SQL("ALTER ROLE {} PASSWORD {}").format(
        sql.Identifier(role), sql.Literal(password)))


def bootstrap(connection: Connection) -> None:
    """Creates roles, extensions, grants and the broker queue if absent."""
    broker_password = os.environ.get("AI_BROKER_DATABASE_PASSWORD", "")
    if not broker_password:
        raise RuntimeError("AI_BROKER_DATABASE_PASSWORD is required")

    connection.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
    connection.execute(text("CREATE EXTENSION IF NOT EXISTS pgmq"))

    for role in READER_ROLES:
        connection.execute(text(f"""
            DO $$ BEGIN
                IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{role}') THEN
                    EXECUTE format('CREATE ROLE %I NOLOGIN NOSUPERUSER NOBYPASSRLS', '{role}');
                END IF;
            END $$;
        """))

    connection.execute(text(f"""
        DO $$ BEGIN
            IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{BROKER_ROLE}') THEN
                EXECUTE 'CREATE ROLE {BROKER_ROLE} LOGIN NOSUPERUSER NOBYPASSRLS
                    NOCREATEDB NOCREATEROLE NOINHERIT';
            END IF;
        END $$;
    """))
    _set_password(connection, BROKER_ROLE, broker_password)
    connection.execute(text(f"ALTER ROLE {BROKER_ROLE} SET statement_timeout = '5s'"))

    for role, variable in LOGIN_PASSWORDS.items():
        password = os.environ.get(variable, "")
        if password:
            _set_password(connection, role, password)

    connection.execute(text(ENABLE_CAPABILITY))
    for statement in GRANTS.strip().split(";"):
        if statement.strip():
            connection.execute(text(statement))

    connection.execute(text("REVOKE ALL ON SCHEMA pgmq FROM PUBLIC"))
    connection.execute(text(f"GRANT USAGE ON SCHEMA pgmq TO {BROKER_ROLE}"))
    connection.execute(text(f"GRANT SELECT ON pgmq.meta TO {BROKER_ROLE}"))
    connection.execute(text(f"""
        DO $$ BEGIN
            IF to_regclass('pgmq.q_{QUEUE}') IS NULL THEN
                PERFORM pgmq.create('{QUEUE}');
            END IF;
        END $$;
    """))
    connection.execute(text(
        f"GRANT SELECT, INSERT, UPDATE, DELETE ON pgmq.q_{QUEUE} TO {BROKER_ROLE}"))
    connection.execute(text(
        f"GRANT USAGE ON SEQUENCE pgmq.q_{QUEUE}_msg_id_seq TO {BROKER_ROLE}"))
