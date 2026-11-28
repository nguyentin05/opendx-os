# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os
import time
from collections.abc import Iterator
from types import SimpleNamespace
from uuid import uuid4

import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from dxos_ai.config import settings
from dxos_ai.database import engine, postgres_url
from dxos_ai.main import create_app
from dxos_ai.security import identity
from dxos_ai.security.identity import Actor


@pytest.fixture(scope="session")
def admin_db() -> Iterator[Engine]:
    dsn = os.environ.get("AI_MIGRATION_DATABASE_URL")
    if not dsn:
        pytest.skip("requires the disposable AI Compose deployment")
    url = postgres_url(dsn)
    assert url.database and url.database.endswith("_test")
    test_db = create_engine(url)
    yield test_db
    test_db.dispose()


@pytest.fixture
def actors(admin_db) -> Iterator[dict[str, Actor]]:
    actors = {
        "admin": Actor(uuid4(), "role_operator", time.time() + 1800),
        "user": Actor(uuid4(), "role_auditor", time.time() + 1800),
        "other": Actor(uuid4(), "role_auditor", time.time() + 1800),
    }
    with admin_db.begin() as db:
        for actor in actors.values():
            db.execute(text("INSERT INTO core.users(id, email, display_name) VALUES (:id,:e,:e)"), {
                "id": actor.user_id, "e": f"{actor.user_id}@test.invalid",
            })
            db.execute(text("INSERT INTO core.user_roles (user_id, role_name) VALUES (:id,:r)"), {
                "id": actor.user_id, "r": actor.role,
            })
    yield actors


@pytest.fixture
def client(monkeypatch, actors) -> Iterator[TestClient]:
    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public_key = private_key.public_key()
    monkeypatch.setenv("OIDC_ISSUER_URL", "https://identity.test.invalid")
    monkeypatch.setenv("OIDC_JWKS_URL", "https://identity.test.invalid/jwks")
    settings.cache_clear()
    monkeypatch.setattr(identity, "jwks", lambda url: SimpleNamespace(
        get_signing_key_from_jwt=lambda token: SimpleNamespace(key=public_key),
    ))

    def headers(actor: Actor) -> dict[str, str]:
        token = jwt.encode({
            "sub": str(actor.user_id), "aud": settings().oidc_audience,
            "iss": settings().oidc_issuer_url, "exp": int(actor.expires_at),
        }, private_key, algorithm="RS256")
        return {"Authorization": f"Bearer {token}", "X-Hasura-Role": actor.role}

    with TestClient(create_app()) as api:
        api.actor_headers = headers
        yield api
    settings.cache_clear()
    engine.cache_clear()
