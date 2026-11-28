# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import base64
import json
import logging
import math
import os
import socket
from collections.abc import Callable
from dataclasses import dataclass
from hashlib import sha256
from ipaddress import ip_address, ip_network
from urllib.parse import urlsplit
from uuid import UUID, uuid4

import httpx
import httpx2
from fastapi import HTTPException
from openai import OpenAI
from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.database import engine
from dxos_ai.models.registry import (
    ModelBinding,
    ModelPurpose,
    SessionDep,
    allowed_destination,
    audit,
    binding,
    fernet,
    model_lock,
    router,
    write_access,
)
from dxos_ai.security.identity import ActorDep
from dxos_ai.security.processing import ProcessingContext, may_process


class ModelError(Exception):
    pass


@dataclass(frozen=True)
class ChatResult:
    text: str | None
    tool_calls: list[dict]
    input_tokens: int | None
    output_tokens: int | None


@dataclass(frozen=True)
class EmbeddingResult:
    revision: UUID
    dimension: int
    vectors: list[list[float]]


@dataclass(frozen=True)
class ProviderResult:
    value: ChatResult | EmbeddingResult | list[int]
    input_tokens: int | None = None
    output_tokens: int | None = None


LOCAL_RANGES = tuple(map(ip_network, (
    "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "fc00::/7", "::1/128",
)))
MODEL_TIMEOUT = 18.0


def resolve_destination(model: ModelBinding) -> str:
    allowed = allowed_destination(model.execution, model.endpoint)
    url = urlsplit(model.endpoint)
    port = url.port or (443 if url.scheme == "https" else 80)
    results = socket.getaddrinfo(url.hostname, port, type=socket.SOCK_STREAM)
    addresses = [ip_address(result[4][0]) for result in results]
    for address in addresses:
        if address.is_multicast or address.is_unspecified or address.is_link_local:
            raise ValueError("Unsafe model destination")
        if model.execution == "HOSTED" and address.is_global:
            continue
        if model.execution == "LOCAL" and any(address in net for net in LOCAL_RANGES):
            if any(address in net for net in allowed):
                continue
        raise ValueError("Unsafe model destination")
    if not addresses:
        raise ValueError("Empty model destination")
    return str(addresses[0])


def invoke(
    model: ModelBinding, payload: dict, before_send: Callable[[], None],
) -> ProviderResult:
    os.environ["LITELLM_LOCAL_MODEL_COST_MAP"] = "True"
    os.environ["LITELLM_MODE"] = "PRODUCTION"
    import litellm
    from litellm.llms.custom_httpx.http_handler import HTTPHandler

    litellm.telemetry = False
    logging.getLogger("LiteLLM").setLevel(logging.CRITICAL + 1)
    cfg_url = urlsplit(model.endpoint)
    selected_ip = resolve_destination(model)
    secret = (
        fernet().decrypt(model.api_key_ciphertext).decode()
        if model.api_key_ciphertext is not None else "dxos-local"
    )

    def check_request(request: httpx.Request | httpx2.Request) -> None:
        if (
            request.method != "POST" or request.url.scheme != cfg_url.scheme
            or request.url.host != cfg_url.hostname
            or (request.url.port or (443 if cfg_url.scheme == "https" else 80))
            != (cfg_url.port or (443 if cfg_url.scheme == "https" else 80))
            or request.url.query or not request.url.path.startswith(cfg_url.path.rstrip("/") + "/")
        ):
            raise ModelError("MODEL_DESTINATION_DENIED")
        before_send()
        request.extensions["sni_hostname"] = cfg_url.hostname
        request.url = request.url.copy_with(host=selected_ip)

    options = {
        "model": f"{model.protocol}/{model.name}",
        "api_base": model.endpoint,
        "api_key": secret,
        "timeout": MODEL_TIMEOUT,
        "num_retries": 0,
        "num_retries_with_fallbacks": 0,
        "fallbacks": [],
        "caching": False,
    }
    if model.purpose == ModelPurpose.RERANKER:
        with httpx.Client(
            event_hooks={"request": [check_request]}, trust_env=False, follow_redirects=False,
        ) as http:
            response = litellm.rerank(
                **options, **payload, client=HTTPHandler(client=http), return_documents=False,
            )
        indexes = [item["index"] for item in response.results]
        if len(set(indexes)) != len(indexes) or any(
            not isinstance(i, int) or not 0 <= i < len(payload["documents"]) for i in indexes
        ):
            raise ModelError("MODEL_INVALID_RESPONSE")
        return ProviderResult(indexes)

    with httpx2.Client(
        event_hooks={"request": [check_request]}, trust_env=False, follow_redirects=False,
    ) as http:
        client = OpenAI(
            api_key=secret, base_url=model.endpoint, http_client=http,
            timeout=MODEL_TIMEOUT, max_retries=0,
        )
        if model.purpose == ModelPurpose.EMBEDDING:
            response = litellm.embedding(**options, **payload, client=client)
        else:
            response = litellm.completion(
                **options, **payload, client=client, max_tokens=model.output_tokens,
            )

    usage = getattr(response, "usage", None)
    input_tokens = getattr(usage, "prompt_tokens", None)
    output_tokens = getattr(usage, "completion_tokens", None)
    if model.purpose == ModelPurpose.EMBEDDING:
        vectors = [row["embedding"] for row in sorted(response.data, key=lambda row: row["index"])]
        if model.dimension is None or len(vectors) != len(payload["input"]) or any(
            len(vec) != model.dimension or not all(math.isfinite(x) for x in vec)
            for vec in vectors
        ):
            raise ModelError("MODEL_INVALID_RESPONSE")
        value = EmbeddingResult(model.revision, model.dimension, vectors)
    else:
        message = response.choices[0].message
        value = ChatResult(
            message.content, [call.model_dump() for call in (message.tool_calls or ())],
            input_tokens, output_tokens,
        )
    return ProviderResult(value, input_tokens, output_tokens)


class ModelGateway:
    def generate(
        self, messages: list[dict], context: ProcessingContext, *, tools: list[dict] | None = None,
    ) -> ChatResult:
        payload = {"messages": messages}
        if tools:
            payload["tools"] = tools
        return self.call(ModelPurpose.LLM, payload, context)

    def embed(self, texts: list[str], context: ProcessingContext) -> EmbeddingResult:
        return self.call(ModelPurpose.EMBEDDING, {"input": texts}, context)

    def vision(self, image: bytes, prompt: str, context: ProcessingContext) -> ChatResult:
        return self.call(ModelPurpose.VLM, vision_payload(image, prompt), context)

    def rerank(self, query: str, documents: list[str], context: ProcessingContext) -> list[int]:
        return self.call(
            ModelPurpose.RERANKER, {"query": query, "documents": documents}, context,
        )

    def call(
        self, purpose: ModelPurpose, payload: dict, context: ProcessingContext,
    ) -> ChatResult | EmbeddingResult | list[int]:
        key = sha256(f"{purpose}:{context.operation_key}".encode()).hexdigest()
        with Session(engine()) as session, session.begin():
            model_lock(session, shared=True)
            model = binding(session, purpose)
            if model is None:
                raise ModelError("MODEL_UNAVAILABLE")
            if not may_process(session, model, context):
                raise ModelError("MODEL_PROCESSING_DENIED")
            if not session.scalar(text("SELECT cap_ai.begin_model_operation(:key, :rev, :p)"), {
                "key": key, "rev": model.revision, "p": purpose,
            }):
                raise ModelError("MODEL_CALL_UNCERTAIN")

        sent = False
        try:
            validate_payload(model, payload)
            with Session(engine()) as session, session.begin():
                model_lock(session, shared=True)

                def before_send() -> None:
                    nonlocal sent
                    if sent:
                        raise ModelError("MODEL_CALL_UNCERTAIN")
                    if (binding(session, purpose) != model
                            or not may_process(session, model, context)):
                        raise ModelError("MODEL_PROCESSING_DENIED")
                    sent = True

                result = invoke(model, payload, before_send)
                if not sent:
                    raise ModelError("MODEL_UNAVAILABLE")
        except Exception as exc:
            finish(key, "UNCERTAIN" if sent else "FAILED_UNSENT")
            if sent:
                raise ModelError("MODEL_CALL_UNCERTAIN") from None
            if isinstance(exc, ModelError):
                raise exc from None
            raise ModelError("MODEL_UNAVAILABLE") from None

        finish(key, "SUCCEEDED", result.input_tokens, result.output_tokens)
        return result.value


def finish(key: str, state: str, input_tokens: int | None = None, output_tokens: int | None = None):
    with Session(engine()) as session, session.begin():
        session.execute(text("SELECT cap_ai.finish_model_operation(:key, :state, :i, :o)"), {
            "key": key, "state": state, "i": input_tokens, "o": output_tokens,
        })


def validate_payload(model: ModelBinding, payload: dict) -> None:
    limit = model.input_chars + (1_400_000 if model.purpose == ModelPurpose.VLM else 0)
    if len(json.dumps(payload, ensure_ascii=False)) > limit:
        raise ModelError("MODEL_INPUT_TOO_LARGE")


def vision_payload(image: bytes, prompt: str) -> dict:
    if len(image) > 1_000_000:
        raise ModelError("MODEL_INPUT_TOO_LARGE")
    uri = "data:image/png;base64," + base64.b64encode(image).decode("ascii")
    return {"messages": [{"role": "user", "content": [
        {"type": "text", "text": prompt}, {"type": "image_url", "image_url": {"url": uri}},
    ]}]}


@router.post("/models/{purpose}/{revision}/probe")
def probe_model(
    purpose: ModelPurpose, revision: UUID, actor: ActorDep, session: SessionDep,
) -> dict[str, str]:
    write_access(actor, session)
    model = binding(session, purpose, revision)
    if model is None:
        raise HTTPException(404, "Model not found")
    request_id = str(uuid4())
    if not session.scalar(text("SELECT cap_ai.begin_model_operation(:key, :rev, :p)"), {
        "key": request_id, "rev": model.revision, "p": purpose,
    }):
        raise HTTPException(503, "Synthetic probe unavailable")
    audit(session, actor, "PROBE_MODEL", str(revision))
    session.commit()

    payload = {
        ModelPurpose.LLM: {"messages": [{"role": "user", "content": "Say hello."}]},
        ModelPurpose.EMBEDDING: {"input": ["Example text"]},
        ModelPurpose.VLM: vision_payload(base64.b64decode(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8"
            "/x8AAwMCAO+aT9sAAAAASUVORK5CYII="
        ), "Describe the test image."),
        ModelPurpose.RERANKER: {"query": "Fruit", "documents": ["Apple", "Clock"]},
    }[purpose]
    sent = False

    def before_send() -> None:
        nonlocal sent
        if sent or binding(session, purpose, revision) != model:
            raise ModelError("MODEL_UNAVAILABLE")
        write_access(actor, session)
        sent = True

    try:
        model_lock(session)
        result = invoke(model, payload, before_send)
        if not sent:
            raise ModelError("MODEL_UNAVAILABLE")
    except Exception:
        session.rollback()
        state = "UNCERTAIN" if sent else "FAILED_UNSENT"
        finish(request_id, state)
        return {"state": state}
    session.commit()
    finish(request_id, "SUCCEEDED", result.input_tokens, result.output_tokens)
    return {"state": "SUCCEEDED"}
