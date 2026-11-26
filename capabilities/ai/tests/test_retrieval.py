# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from uuid import UUID

import pytest
from sqlalchemy import text
from test_knowledge_access import register_file, seed_app, seed_file
from test_model_gateway import HOSTED, register

from dxos_ai.config import settings
from dxos_ai.models.gateway import EmbeddingResult, ModelError, ModelGateway
from dxos_ai.models.registry import ModelPurpose

PREFIX = "/api/cap/ai"


def active_chunk(client, admin_db, app, actor, body, page=1):
    file = seed_file(admin_db, app, actor, body.encode())
    doc = register_file(client, actor, file)
    with admin_db.begin() as db:
        db.execute(text("""
            UPDATE cap_ai.documents SET active_version=:version WHERE id=:doc
        """), {"doc": doc["document_id"], "version": doc["version_id"]})
        chunk = db.scalar(text("""
            INSERT INTO cap_ai.document_chunks(version_id,ordinal,body,provenance)
            VALUES (:version,0,:body,jsonb_build_object(
                'pages',jsonb_build_array(CAST(:page AS int)), 'section_path','["Safety"]'::jsonb,
                'element_ids','["#/texts/1"]'::jsonb
            )) RETURNING id
        """), {
            "version": doc["version_id"], "body": body, "page": page,
        })
    return doc | {"chunk_id": str(chunk)}


@pytest.fixture
def private_evidence(client, admin_db, actors):
    library = seed_app(admin_db, actors["user"], actors["other"], with_package=False)
    doc = active_chunk(client, admin_db, library.app, library.owner, "maintenance safety guide")
    return library, doc


def query(client, actor, text="maintenance", retrieval="LEXICAL", limit=10):
    response = client.post(f"{PREFIX}/search", headers=client.actor_headers(actor), json={
        "query": text, "scope": {"kind": "PERSONAL"}, "retrieval": retrieval, "limit": limit,
    })
    assert response.status_code == 200, response.text
    return response.json()["results"]


def citation_path(doc):
    path = f"{PREFIX}/documents/{doc['document_id']}/citations/{doc['chunk_id']}"
    return path + "?version_id=" + doc["version_id"]


def test_authorized_candidates_before_ranking(client, admin_db, private_evidence, actors):
    library, doc = private_evidence
    other = seed_app(admin_db, actors["other"], actors["user"], with_package=False)
    for _ in range(34):
        active_chunk(client, admin_db, other.app, other.owner, "maintenance maintenance")
    hits = query(client, library.owner, limit=1)
    assert [hit["chunk_id"] for hit in hits] == [doc["chunk_id"]]
    assert query(client, actors["admin"]) == []


def test_revoked_citation_denied(client, private_evidence, admin_db, actors):
    library, doc = private_evidence
    path = citation_path(doc)
    headers = client.actor_headers(actors["user"])
    assert client.get(path, headers=headers).status_code == 200
    with admin_db.begin() as db:
        db.execute(text("""
            DELETE FROM core.user_roles WHERE user_id=:actor AND role_name=:role
        """), {"actor": library.owner.user_id, "role": library.owner.role})
    assert client.get(path, headers=headers).status_code == 404


def test_embedding_revision_mismatch_uses_fts(
    client, private_evidence, admin_db, actors, monkeypatch,
):
    library, doc = private_evidence
    auth = client.actor_headers(actors["admin"])
    monkeypatch.setenv("AI_HOSTED_MODEL_ENDPOINTS", f'["{HOSTED}"]')
    settings.cache_clear()
    _, first = register(client, auth, ModelPurpose.EMBEDDING)
    _, current = register(client, auth, ModelPurpose.EMBEDDING)
    old = active_chunk(client, admin_db, library.app, library.owner, "unrelated stale vector")
    with admin_db.begin() as db:
        db.execute(text("""
            UPDATE cap_ai.document_chunks
            SET embedding='[0,1,0]',dimension=3,model_revision=:revision WHERE id=:chunk
        """), {"revision": first, "chunk": old["chunk_id"]})
    monkeypatch.setattr(ModelGateway, "embed", lambda *args: EmbeddingResult(
        UUID(current), 2, [[0.0, 1.0]],
    ))
    assert [row["chunk_id"] for row in query(
        client, library.owner, retrieval="HYBRID",
    )] == [doc["chunk_id"]]


def test_optional_reranker_unavailable(client, private_evidence, monkeypatch):
    library, doc = private_evidence
    calls = []

    def unavailable(*args):
        calls.append(True)
        raise ModelError("MODEL_UNAVAILABLE")

    monkeypatch.setattr(ModelGateway, "rerank", unavailable)
    hits = query(client, library.owner, retrieval="HYBRID")
    assert calls == [True]
    assert hits[0]["chunk_id"] == doc["chunk_id"]


def test_valid_citation_provenance(client, private_evidence):
    library, doc = private_evidence
    reply = client.get(citation_path(doc), headers=client.actor_headers(library.owner))
    assert reply.status_code == 200
    assert reply.json()["positions"] == [{"page_no": 1, "provenance": "PAGE"}]
    assert reply.json()["section_path"] == ["Safety"]
