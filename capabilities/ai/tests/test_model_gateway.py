# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread
from uuid import uuid4

import pytest
from cryptography.fernet import Fernet
from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.config import settings
from dxos_ai.database import engine
from dxos_ai.models import gateway
from dxos_ai.models.gateway import ChatResult, ModelError, ModelGateway, ProviderResult
from dxos_ai.models.registry import ModelPurpose, binding
from dxos_ai.security.processing import ProcessingContext, hosted_allowed

PREFIX = "/api/cap/ai/admin"
HOSTED = "https://models.test.invalid/v1"


@pytest.fixture(autouse=True)
def registry(admin_db, monkeypatch):
    with admin_db.begin() as db:
        db.execute(text("UPDATE cap_ai.model_slots SET revision=NULL"))
        db.execute(text("DELETE FROM cap_ai.processing_policies"))
    monkeypatch.setenv("AI_MODEL_MASTER_KEY", Fernet.generate_key().decode())
    monkeypatch.setenv("AI_HOSTED_MODEL_ENDPOINTS", json.dumps([HOSTED]))
    settings.cache_clear()


def register(client, auth, purpose, *, endpoint=HOSTED, execution="HOSTED", key=None):
    response = client.post(f"{PREFIX}/providers", headers=auth, json={
        "name": str(uuid4()),
        "endpoint": endpoint,
        "execution": execution,
        "protocol": "infinity" if purpose == ModelPurpose.RERANKER else "openai",
        "api_key": key,
    })
    assert response.status_code == 201, response.text
    provider = response.json()["id"]
    response = client.post(f"{PREFIX}/models", headers=auth, json={
        "provider_id": provider,
        "purpose": purpose,
        "name": "fake",
        "dimension": 2 if purpose == ModelPurpose.EMBEDDING else None,
    })
    assert response.status_code == 201, response.text
    revision = response.json()["revision"]
    assert client.put(f"{PREFIX}/slots/{purpose}", headers=auth, json={
        "revision": revision,
    }).status_code == 204
    return provider, revision


def policy(client, auth, purpose, revision, data_class="QUERY", decision="ALLOW"):
    result = client.put(f"{PREFIX}/policy", headers=auth, json={
        "revision": revision, "data_class": data_class, "purpose": purpose, "decision": decision,
    })
    assert result.status_code == 200, result.text


def test_admin_only_and_write_only_secret(client, admin_db, actors):
    auth = client.actor_headers(actors["admin"])
    regular = client.actor_headers(actors["user"])
    assert client.get(f"{PREFIX}/registry", headers=regular).status_code == 403
    assert client.get(f"{PREFIX}/registry", headers=regular | {
        "X-Hasura-Role": "role_operator",
    }).status_code == 403
    provider, _ = register(client, auth, ModelPurpose.LLM, key="private-test-credential")
    page = client.get(f"{PREFIX}/registry", headers=auth)
    assert page.status_code == 200
    assert "private-test-credential" not in page.text and "ciphertext" not in page.text
    assert next(item for item in page.json()["providers"] if item["id"] == provider)["has_api_key"]
    with admin_db.connect() as db:
        secret = db.scalar(text(
            "SELECT api_key_ciphertext FROM cap_ai.model_providers WHERE id=:id"
        ), {"id": provider})
        assert b"private-test-credential" not in secret
    bad_endpoint = client.post(f"{PREFIX}/providers", headers=auth, json={
        "name": "metadata", "endpoint": "http://169.254.169.254",
        "protocol": "openai", "execution": "LOCAL",
    })
    assert bad_endpoint.status_code == 422


@pytest.mark.parametrize("purpose", list(ModelPurpose))
def test_policy_denies_before_dispatch(client, actors, monkeypatch, purpose):
    register(client, client.actor_headers(actors["admin"]), purpose)
    calls = []
    monkeypatch.setattr(gateway, "invoke", lambda *args: calls.append(args))
    context = ProcessingContext(actors["user"], str(uuid4()))
    with pytest.raises(ModelError, match="MODEL_PROCESSING_DENIED"):
        ModelGateway().call(purpose, {}, context)
    assert not calls


def test_destination_change_requires_review(client, actors, monkeypatch):
    auth = client.actor_headers(actors["admin"])
    _, first = register(client, auth, ModelPurpose.LLM)
    policy(client, auth, ModelPurpose.LLM, first)
    endpoint = "https://alternate.test.invalid/v1"
    monkeypatch.setenv("AI_HOSTED_MODEL_ENDPOINTS", json.dumps([HOSTED, endpoint]))
    settings.cache_clear()
    register(client, auth, ModelPurpose.LLM, endpoint=endpoint)
    calls = []
    monkeypatch.setattr(gateway, "invoke", lambda *args: calls.append(args))
    with pytest.raises(ModelError, match="MODEL_PROCESSING_DENIED"):
        ModelGateway().generate([], ProcessingContext(actors["user"], str(uuid4())))
    assert not calls


def test_mixed_source_restrictions(client, actors):
    auth = client.actor_headers(actors["admin"])
    _, revision = register(client, auth, ModelPurpose.LLM)
    policy(client, auth, ModelPurpose.LLM, revision)
    policy(client, auth, ModelPurpose.LLM, revision, "APPLICATION")
    with Session(engine()) as session:
        model = binding(session, ModelPurpose.LLM)
        classes = {("QUERY", None), ("APPLICATION", "library")}
        assert hosted_allowed(session, model, classes)
        assert not hosted_allowed(session, model, classes | {("PERSONAL", None)})


def test_uncertain_call_not_resent(client, actors, monkeypatch):
    auth = client.actor_headers(actors["admin"])
    _, revision = register(client, auth, ModelPurpose.LLM)
    policy(client, auth, ModelPurpose.LLM, revision)
    calls = []

    def ambiguous(model, payload, before_send):
        before_send()
        calls.append(model.revision)
        raise TimeoutError

    monkeypatch.setattr(gateway, "invoke", ambiguous)
    context = ProcessingContext(actors["user"], str(uuid4()))
    for _ in range(2):
        with pytest.raises(ModelError, match="MODEL_CALL_UNCERTAIN"):
            ModelGateway().generate([], context)
    assert len(calls) == 1


def test_slot_switch_and_missing_slot(client, actors, monkeypatch):
    auth = client.actor_headers(actors["admin"])
    models = ModelGateway()

    def context():
        return ProcessingContext(actors["user"], str(uuid4()))

    with pytest.raises(ModelError, match="MODEL_UNAVAILABLE"):
        models.generate([], context())
    _, first = register(client, auth, ModelPurpose.LLM)
    policy(client, auth, ModelPurpose.LLM, first)
    _, second = register(client, auth, ModelPurpose.LLM)
    policy(client, auth, ModelPurpose.LLM, second)

    def confirmed(model, payload, before_send):
        before_send()
        return ProviderResult(ChatResult(str(model.revision), [], 1, 1), 1, 1)

    monkeypatch.setattr(gateway, "invoke", confirmed)
    assert models.generate([], context()).text == second


def test_four_slots_through_sdk(client, actors, monkeypatch):
    paths = []

    class ModelServer(BaseHTTPRequestHandler):
        def do_POST(self):
            paths.append(self.path)
            self.rfile.read(int(self.headers["Content-Length"]))
            if self.path == "/v1/embeddings":
                response = {"object": "list", "data": [{"index": 0, "embedding": [0.3, 0.5]}]}
            elif self.path == "/v1/rerank":
                response = {"results": [{"index": 0, "relevance_score": 1.0}]}
            else:
                response = {"id": "test", "choices": [{
                    "index": 0, "message": {"role": "assistant", "content": "hello"},
                    "finish_reason": "stop",
                }]}
            response["model"] = "fake"
            response["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
            payload = json.dumps(response).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *args):
            pass

    with ThreadingHTTPServer(("127.0.0.1", 0), ModelServer) as server:
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = f"http://127.0.0.1:{server.server_port}/v1"
        monkeypatch.setenv("AI_LOCAL_MODEL_ENDPOINTS", json.dumps({endpoint: ["127.0.0.1/32"]}))
        settings.cache_clear()
        auth = client.actor_headers(actors["admin"])
        try:
            for purpose in ModelPurpose:
                register(client, auth, purpose, endpoint=endpoint, execution="LOCAL")

            def context():
                return ProcessingContext(actors["user"], str(uuid4()))

            model = ModelGateway()
            assert model.generate([{"role": "user", "content": "hello"}], context()).text
            assert model.embed(["document"], context()).vectors == [[0.3, 0.5]]
            assert model.vision(b"opaque test image", "caption", context()).text
            assert model.rerank("query", ["document"], context()) == [0]
            assert paths == [
                "/v1/chat/completions", "/v1/embeddings", "/v1/chat/completions", "/v1/rerank",
            ]
        finally:
            server.shutdown()
            thread.join(timeout=3)
