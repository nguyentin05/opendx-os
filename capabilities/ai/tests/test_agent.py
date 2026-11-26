# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
from uuid import uuid4

from sqlalchemy import text
from test_knowledge_access import seed_app
from test_retrieval import active_chunk

from dxos_ai.models.gateway import ChatResult, ModelError, ModelGateway

CONVERSATIONS = "/api/cap/ai/conversations"


def create(client, actor):
    response = client.post(CONVERSATIONS, headers=client.actor_headers(actor), json={
        "title": "Private questions",
    })
    assert response.status_code == 201
    return response.json()["id"]


def ask(client, actor, conversation, query="question", key=None):
    response = client.post(
        f"{CONVERSATIONS}/{conversation}/messages",
        headers=client.actor_headers(actor) | {"Idempotency-Key": str(key or uuid4())},
        json={"query": query},
    )
    assert response.status_code == 200, response.text
    return response.json()


def final(mode, answer="", ids=()):
    return ChatResult(json.dumps({
        "mode": mode, "answer": answer, "citation_ids": list(ids),
    }), [], None, None)


def tool(query="maintenance"):
    return ChatResult(None, [{
        "id": "tool-1", "function": {
            "name": "search_knowledge", "arguments": json.dumps({"query": query}),
        },
    }], None, None)


def test_general_chat(client, actors, monkeypatch):
    calls = []

    def generate(self, messages, context, **kwargs):
        calls.append(context.operation_key)
        return final("GENERAL", "Paris is the capital of France.")

    monkeypatch.setattr(ModelGateway, "generate", generate)
    actor = actors["user"]
    conversation = create(client, actor)
    key = uuid4()
    result = ask(client, actor, conversation, key=key)
    assert result["mode"] == "GENERAL" and result["citations"] == []
    assert ask(client, actor, conversation, key=key) == result
    assert len(calls) == 1


def test_agent_uses_knowledge_tool(client, actors, admin_db, monkeypatch):
    library = seed_app(admin_db, actors["user"], actors["other"], with_package=False)
    doc = active_chunk(client, admin_db, library.app, library.owner, "maintenance safety valve")

    embed_contexts = []

    def embed(self, texts, context):
        embed_contexts.append(context.sources)
        raise ModelError("MODEL_UNAVAILABLE")

    def generate(self, messages, context, **kwargs):
        if messages[-1]["role"] != "tool":
            return tool()
        assert doc["chunk_id"] in messages[-1]["content"]
        if sum(message["role"] == "tool" for message in messages) == 1:
            return tool("safety valve")
        return final("GROUNDED", "Inspect the valve.", [doc["chunk_id"]])

    monkeypatch.setattr(ModelGateway, "embed", embed)
    monkeypatch.setattr(ModelGateway, "generate", generate)
    actor = actors["user"]
    conversation = create(client, actor)
    result = ask(client, actor, conversation)
    assert result["mode"] == "GROUNDED"
    assert len(embed_contexts) == 2 and not embed_contexts[0]
    assert str(embed_contexts[1][0].document_id) == doc["document_id"]
    [cite] = result["citations"]
    assert cite["id"] == doc["chunk_id"]
    page = client.get(cite["url"], headers=client.actor_headers(actor))
    assert page.status_code == 200 and "safety valve" in page.json()["snippet"]


def test_internal_question_without_evidence_abstains(client, actors, monkeypatch):
    answers = iter([tool("internal payroll handbook"), final("GENERAL", "Unsupported claim")])
    monkeypatch.setattr(ModelGateway, "generate", lambda *args, **kwargs: next(answers))
    actor = actors["user"]
    conversation = create(client, actor)
    result = ask(client, actor, conversation)
    assert result["status"] == "NO_EVIDENCE" and not result["citations"]


def test_conversation_owner_only_and_revoked_memory(client, actors, admin_db, monkeypatch):
    library = seed_app(admin_db, actors["user"], actors["other"], with_package=False)
    doc = active_chunk(client, admin_db, library.app, library.owner, "maintenance safety valve")
    answers = iter([tool(), final("GROUNDED", "Private policy answer", [doc["chunk_id"]])])

    def generate(self, messages, context, **kwargs):
        if messages[-1]["content"] == "new question":
            assert "Private policy answer" not in json.dumps(messages)
            assert not context.sources
            return final("GENERAL", "Safe answer")
        return next(answers)

    monkeypatch.setattr(ModelGateway, "generate", generate)
    actor = actors["user"]
    conversation = create(client, actor)
    result = ask(client, actor, conversation)
    assert result["mode"] == "GROUNDED"
    [cite] = result["citations"]
    foreign = client.actor_headers(actors["admin"])
    assert client.get(f"{CONVERSATIONS}/{conversation}", headers=foreign).status_code == 404
    assert client.get(cite["url"], headers=foreign).status_code == 404
    with admin_db.begin() as db:
        db.execute(text("""
            DELETE FROM core.user_roles WHERE user_id=:user AND role_name=:role
        """), {"user": library.owner.user_id, "role": library.owner.role})
    assert client.get(cite["url"], headers=client.actor_headers(actor)).status_code == 404
    assert ask(client, actor, conversation, query="new question")["answer"] == "Safe answer"


def test_invalid_citation_rejected(client, actors, monkeypatch):
    answers = iter([tool(), final("GROUNDED", "Fabricated citation", [str(uuid4())])])
    monkeypatch.setattr(ModelGateway, "generate", lambda *args, **kwargs: next(answers))
    actor = actors["user"]
    assert ask(client, actor, create(client, actor))["status"] == "NO_EVIDENCE"
