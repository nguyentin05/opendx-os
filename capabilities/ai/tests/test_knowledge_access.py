# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
import time
from hashlib import sha256
from types import SimpleNamespace
from uuid import uuid4

import pytest
from sqlalchemy import text

from dxos_ai.config import settings
from dxos_ai.security.identity import Actor

PREFIX = "/api/cap/ai"
PRIVATE = b"# Personal notes\n\nOnly authorized owners can open this document.\n"
PACKAGE = b"# Handbook\n\nMaintenance tickets require an operator review.\n"


def seed_app(admin_db, owner, other, *, mode="RESTRICTED", with_package=True, data=PACKAGE):
    app = "library_" + uuid4().hex
    read_role = "role_reader_" + app
    manage_role = "role_manage_" + app
    incarnation = str(uuid4())
    digest = sha256(data).hexdigest()
    artifact = f"{app}/handbook/{digest}"
    source = {
        "id": "handbook", "path": "knowledge/handbook.md", "mediaType": "text/markdown",
        "indexingProfile": "standard-text-v1",
        "access": {"mode": mode, "allowedRoles": [read_role], "grantsManagedBy": "delegates"},
    }
    pub = {
        "source_id": "handbook", "incarnation": incarnation, "declaration_hash": "1" * 64,
        "authorization_generation": 1, "artifact_ref": artifact, "sha256": digest,
        "source_path": "knowledge/handbook.md", "media_type": "text/markdown",
        "size_bytes": len(data), "parser_profile": "standard-text-v1", "safety_state": "APPROVED",
    }
    spec = {
        "knowledge": {
            "version": 1, "personalLibrary": {"enabled": True, "allowedRoles": [read_role]},
            "sources": [source] if with_package else [],
            "grantPolicies": {"delegates": {"allowedRoles": [manage_role]}},
        },
        "_publication": {
            "generation": 1, "spec_digest": "a" * 64, "package_digest": "b" * 64,
            "sources": [pub] if with_package else [],
        },
    }
    with admin_db.begin() as db:
        db.execute(text("""
            INSERT INTO core.apps(app_id,display_name,version,spec,status,publication_generation)
            VALUES (:app,:app,'1',CAST(:spec AS jsonb),'Ready',1)
        """), {"app": app, "spec": json.dumps(spec)})
        db.execute(text(
            "INSERT INTO core.app_capabilities(app_id,cap_id) VALUES (:app,'cap-ai')"
        ), {"app": app})
        for actor, role in [(owner, read_role), (other, manage_role)]:
            db.execute(text("INSERT INTO core.roles(name,app_id) VALUES (:role,:app)"), {
                "role": role, "app": app,
            })
            db.execute(text(
                "INSERT INTO core.user_roles(user_id,role_name) VALUES (:actor,:role)"
            ), {"actor": actor.user_id, "role": role})
    return SimpleNamespace(
        app=app, artifact=artifact, owner=Actor(owner.user_id, read_role, owner.expires_at),
        manager=Actor(other.user_id, manage_role, other.expires_at), spec=spec,
    )


def seed_file(admin_db, app, actor, data=PRIVATE, mime_type="text/markdown"):
    file_id = uuid4()
    with admin_db.begin() as db:
        db.execute(text("""
            INSERT INTO core.files (id,app_id,bucket,object_key,file_name,read_policy,
                scan_status,sha256_hash,uploaded_by,mime_type,size_bytes)
            VALUES (:id,:app,'private',:key,'private-notes','OWNER_ONLY',
                'CLEAN',:digest,:user,:mime,:size)
        """), {
            "id": file_id, "app": app, "key": str(file_id), "digest": sha256(data).hexdigest(),
            "user": actor.user_id, "size": len(data), "mime": mime_type,
        })
    return file_id


def register_file(client, owner, file_id):
    response = client.post(f"{PREFIX}/documents", headers=client.actor_headers(owner), json={
        "file_id": str(file_id),
    })
    assert response.status_code == 201, response.text
    return response.json()


def publish(client, app, monkeypatch, generation=1):
    subject = uuid4()
    monkeypatch.setenv("RAG_DISPATCHER_SERVICE_SUBJECT", str(subject))
    settings.cache_clear()
    service = Actor(subject, "role_auditor", time.time() + 1000)
    headers = client.actor_headers(service)
    del headers["X-Hasura-Role"]
    return client.post("/internal/v1/package-sources/reconcile", headers={
        **headers, "Idempotency-Key": str(uuid4()),
    }, json={
        "schema_version": 1, "app_id": app, "publication_generation": generation,
        "spec_digest": "a" * 64,
    })


@pytest.fixture
def library(admin_db, actors):
    return seed_app(admin_db, actors["user"], actors["other"])


def test_personal_owner_only_including_admin(client, actors, library, admin_db):
    file = seed_file(admin_db, library.app, library.owner)
    doc = register_file(client, library.owner, file)["document_id"]
    own = client.actor_headers(library.owner)
    for subject in (actors["admin"], actors["other"]):
        headers = client.actor_headers(subject)
        assert client.get(f"{PREFIX}/documents", headers=headers).json()["documents"] == []
        assert client.get(f"{PREFIX}/documents/{doc}/original", headers=headers).status_code == 404
    documents = client.get(f"{PREFIX}/documents", headers=own).json()["documents"]
    assert documents[0]["document_id"] == doc
    response = client.get(f"{PREFIX}/documents/{doc}/original", headers=own, follow_redirects=False)
    assert response.status_code == 307
    assert response.headers["location"].endswith(str(file))


def test_app_role_and_restricted_grant(client, library, monkeypatch):
    assert publish(client, library.app, monkeypatch).status_code == 204
    owner = client.actor_headers(library.owner)
    manager = client.actor_headers(library.manager)
    route = f"{PREFIX}/app-knowledge?app_id={library.app}"
    assert client.get(route, headers=owner).json()["sources"] == []
    [item] = client.get(route, headers=manager).json()["sources"]
    assert item["manageable"] and not item["readable"] and item["title"] is None
    path = f"{PREFIX}/documents/{item['document_id']}"
    assert client.get(path + "/original", headers=manager).status_code == 404
    metadata = client.get(path + "/grant-management", headers=manager).json()
    result = client.post(path + "/grants", headers=manager | {
        "If-Match": f'"{metadata["revision"]}"',
    }, json={"user_id": str(library.owner.user_id)})
    assert result.status_code == 204, result.text
    assert client.get(route, headers=owner).json()["sources"][0]["readable"]
    assert client.get(route, headers=manager).json()["sources"][0]["readable"] is False


def test_revoke_and_unpublish(client, actors, admin_db, monkeypatch):
    app = seed_app(admin_db, actors["user"], actors["other"], mode="ROLE")
    assert publish(client, app.app, monkeypatch).status_code == 204
    owner = client.actor_headers(app.owner)
    route = f"{PREFIX}/app-knowledge?app_id={app.app}"
    assert client.get(route, headers=owner).json()["sources"][0]["readable"]
    with admin_db.begin() as db:
        db.execute(text(
            "UPDATE core.apps SET status='Removed' WHERE app_id=:app"
        ), {"app": app.app})
    assert client.get(route, headers=owner).status_code == 403
    assert publish(client, app.app, monkeypatch, generation=2).status_code == 409


def test_repeated_registration_is_idempotent(client, library, admin_db):
    file = seed_file(admin_db, library.app, library.owner)
    first = register_file(client, library.owner, file)
    second = register_file(client, library.owner, file)
    assert (first["version_id"], first["job_id"]) == (second["version_id"], second["job_id"])
