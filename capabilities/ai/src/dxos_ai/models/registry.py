# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
from enum import StrEnum
from ipaddress import ip_network
from typing import Annotated, Literal
from urllib.parse import urlsplit
from uuid import UUID

from cryptography.fernet import Fernet
from fastapi import APIRouter, HTTPException, Response
from pydantic import BaseModel, ConfigDict, Field, SecretStr, model_validator
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from dxos_ai.config import settings
from dxos_ai.security.identity import Actor, ActorDep, SessionDep, require_admin


class ModelPurpose(StrEnum):
    LLM = "LLM"
    EMBEDDING = "EMBEDDING"
    VLM = "VLM"
    RERANKER = "RERANKER"


DataClass = Literal["QUERY", "PERSONAL", "APPLICATION"]
Execution = Literal["LOCAL", "HOSTED"]
Protocol = Literal["openai", "cohere", "infinity"]


class ModelBinding(BaseModel):
    revision: UUID
    purpose: ModelPurpose
    dimension: int | None
    name: str
    input_chars: int
    output_tokens: int
    endpoint: str
    execution: Execution
    protocol: Protocol
    api_key_ciphertext: bytes | None


def allowed_destination(execution: Execution, endpoint: str) -> list:
    cfg = settings()
    url = urlsplit(endpoint)
    if (
        not url.hostname or url.username is not None or url.password is not None
        or url.query or url.fragment or "%" in endpoint or "\\" in endpoint
        or ".." in url.path or endpoint.endswith("/")
        or any(char.isspace() or ord(char) < 32 for char in endpoint)
        or not (url.port is None or 0 < url.port < 65536)
    ):
        raise ValueError("Invalid model endpoint")
    if (execution == "HOSTED" and url.scheme == "https"
            and endpoint in cfg.ai_hosted_model_endpoints):
        return []
    if execution == "LOCAL" and url.scheme in ("http", "https"):
        ranges = cfg.ai_local_model_endpoints.get(endpoint)
        if ranges:
            return [ip_network(network) for network in ranges]
    raise ValueError("Unapproved model endpoint")


def binding(
    session: Session, purpose: ModelPurpose, revision: UUID | None = None,
) -> ModelBinding | None:
    selector = "s.revision = m.revision" if revision is None else "m.revision = :revision"
    row = session.execute(text(f"""
        SELECT m.*, p.endpoint, p.execution, p.protocol, p.api_key_ciphertext
        FROM cap_ai.models m JOIN cap_ai.model_providers p ON p.id=m.provider_id
        JOIN cap_ai.model_slots s ON s.purpose=m.purpose AND {selector}
        WHERE m.purpose=:purpose AND p.enabled
    """), {"purpose": purpose, "revision": revision}).mappings().one_or_none()
    return ModelBinding.model_validate(row) if row else None


def model_lock(session: Session, *, shared: bool = False) -> None:
    function = "pg_advisory_xact_lock_shared" if shared else "pg_advisory_xact_lock"
    session.execute(text(f"SELECT {function}(hashtext('dxos.ai.models'))"))


def fernet() -> Fernet:
    key = settings().ai_model_master_key
    if key is None:
        raise ValueError("Model credential encryption unavailable")
    return Fernet(key.get_secret_value().encode("ascii"))


def audit(session: Session, actor: Actor, action: str, target: str, **details: object) -> None:
    session.execute(text("""
        INSERT INTO cap_ai.model_audit (actor_id, action, target, details)
        VALUES (:actor, :action, :target, CAST(:details AS jsonb))
    """), {
        "actor": actor.user_id, "action": action, "target": target, "details": json.dumps(details),
    })


router = APIRouter(prefix="/api/cap/ai/admin")


class Input(BaseModel):
    model_config = ConfigDict(extra="forbid")


class ProviderInput(Input):
    name: Annotated[str, Field(min_length=1, max_length=120)]
    execution: Execution
    endpoint: Annotated[str, Field(max_length=512)]
    protocol: Protocol
    api_key: Annotated[SecretStr | None, Field(max_length=4096)] = None


class ProviderUpdate(Input):
    name: Annotated[str, Field(min_length=1, max_length=120)]
    enabled: bool
    api_key: Annotated[SecretStr | None, Field(max_length=4096)] = None


class ModelInput(Input):
    provider_id: UUID
    purpose: ModelPurpose
    name: Annotated[str, Field(pattern=r"^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,199}$")]
    dimension: Annotated[int | None, Field(ge=1, le=4096)] = None
    input_chars: Annotated[int, Field(ge=1, le=128000)] = 24000
    output_tokens: Annotated[int, Field(ge=1, le=4096)] = 1024

    @model_validator(mode="after")
    def valid_dimension(self):
        if (self.dimension is not None) != (self.purpose == ModelPurpose.EMBEDDING):
            raise ValueError("Embedding models require a dimension")
        return self


class SlotInput(Input):
    revision: UUID | None


class PolicyInput(Input):
    app_id: Annotated[str | None, Field(pattern=r"^[a-z][a-z0-9_]{0,127}$")] = None
    data_class: DataClass
    purpose: ModelPurpose
    revision: UUID | None
    decision: Literal["ALLOW", "DENY"]


def write_access(actor: Actor, session: Session) -> None:
    model_lock(session)
    require_admin(actor, session, lock=True)


def key_bytes(secret: SecretStr | None) -> bytes | None:
    try:
        return fernet().encrypt(secret.get_secret_value().encode()) if secret else None
    except ValueError:
        raise HTTPException(503, "Model credential encryption unavailable") from None


@router.get("/registry")
def read_registry(actor: ActorDep, session: SessionDep) -> dict:
    require_admin(actor, session)
    queries = {
        "providers": """
            SELECT id, name, execution, endpoint, protocol, enabled,
                api_key_ciphertext IS NOT NULL AS has_api_key
            FROM cap_ai.model_providers ORDER BY name
        """,
        "models": "SELECT * FROM cap_ai.models ORDER BY name, revision",
        "slots": "SELECT * FROM cap_ai.model_slots ORDER BY purpose",
        "policies": "SELECT * FROM cap_ai.processing_policies ORDER BY data_class, app_id, purpose",
        "audit": "SELECT * FROM cap_ai.model_audit ORDER BY id DESC LIMIT 50",
        "operations": """
            SELECT revision, purpose, state, input_tokens, output_tokens, created_at, finished_at
            FROM cap_ai.model_operations ORDER BY created_at DESC LIMIT 50
        """,
    }
    return {
        key: [dict(row) for row in session.execute(text(query)).mappings()]
        for key, query in queries.items()
    }


@router.post("/providers", status_code=201)
def add_provider(body: ProviderInput, actor: ActorDep, session: SessionDep) -> dict:
    write_access(actor, session)
    try:
        allowed_destination(body.execution, body.endpoint)
    except ValueError:
        raise HTTPException(422, "Unapproved model endpoint") from None
    values = body.model_dump(exclude={"api_key"}) | {"key": key_bytes(body.api_key)}
    new_id = session.scalar(text("""
        INSERT INTO cap_ai.model_providers (name, execution, endpoint, protocol, api_key_ciphertext)
        VALUES (:name, :execution, :endpoint, :protocol, :key)
        ON CONFLICT DO NOTHING RETURNING id
    """), values)
    if new_id is None:
        raise HTTPException(409, "Provider already exists")
    audit(session, actor, "ADD_PROVIDER", str(new_id))
    session.commit()
    return {"id": new_id}


@router.patch("/providers/{provider_id}", status_code=204)
def update_provider(
    provider_id: UUID, body: ProviderUpdate, actor: ActorDep, session: SessionDep,
) -> Response:
    write_access(actor, session)
    values = body.model_dump(exclude={"api_key"}) | {
        "id": provider_id,
        "key": key_bytes(body.api_key),
        "replace_key": "api_key" in body.model_fields_set,
    }
    try:
        changed = session.execute(text("""
            UPDATE cap_ai.model_providers SET name=:name, enabled=:enabled,
                api_key_ciphertext=CASE WHEN :replace_key THEN :key ELSE api_key_ciphertext END
            WHERE id=:id
        """), values)
    except IntegrityError:
        raise HTTPException(409, "Provider name already exists") from None
    if not changed.rowcount:
        raise HTTPException(404, "Provider not found")
    audit(session, actor, "UPDATE_PROVIDER", str(provider_id), enabled=body.enabled)
    session.commit()
    return Response(status_code=204)


@router.post("/models", status_code=201)
def add_model(body: ModelInput, actor: ActorDep, session: SessionDep) -> dict:
    write_access(actor, session)
    provider = session.scalar(text(
        "SELECT protocol FROM cap_ai.model_providers WHERE id=:id"
    ), {"id": body.provider_id})
    if not provider:
        raise HTTPException(404, "Provider not found")
    if (provider == "openai") == (body.purpose == ModelPurpose.RERANKER):
        raise HTTPException(422, "Provider protocol does not support this purpose")
    revision = session.scalar(text("""
        INSERT INTO cap_ai.models(provider_id, purpose, name, dimension, input_chars, output_tokens)
        VALUES (:provider_id, :purpose, :name, :dimension, :input_chars, :output_tokens)
        RETURNING revision
    """), body.model_dump())
    audit(session, actor, "ADD_MODEL", str(revision))
    session.commit()
    return {"revision": revision}


@router.put("/slots/{purpose}", status_code=204)
def select_model(
    purpose: ModelPurpose, body: SlotInput, actor: ActorDep, session: SessionDep,
) -> Response:
    write_access(actor, session)
    if body.revision and not binding(session, purpose, body.revision):
        raise HTTPException(422, "Enabled model with this purpose required")
    session.execute(text("UPDATE cap_ai.model_slots SET revision=:rev WHERE purpose=:purpose"), {
        "rev": body.revision, "purpose": purpose,
    })
    audit(session, actor, "BIND_SLOT", purpose, revision=str(body.revision or ""))
    session.commit()
    return Response(status_code=204)


@router.put("/policy")
def put_policy(body: PolicyInput, actor: ActorDep, session: SessionDep) -> dict:
    write_access(actor, session)
    if body.app_id and body.data_class != "APPLICATION":
        raise HTTPException(422, "Application overrides require application data")
    if body.decision == "ALLOW" and body.revision is None:
        raise HTTPException(422, "Processing approval requires a reviewed model revision")
    if body.revision and not binding(session, body.purpose, body.revision):
        raise HTTPException(422, "Enabled model with this purpose required")
    try:
        item = session.scalar(text("""
            INSERT INTO cap_ai.processing_policies(app_id, data_class, purpose, revision, decision)
            VALUES (:app_id, :data_class, :purpose, :revision, :decision)
            ON CONFLICT (app_id, data_class, purpose, revision)
            DO UPDATE SET decision=EXCLUDED.decision RETURNING id
        """), body.model_dump())
    except IntegrityError:
        raise HTTPException(422, "Invalid application policy") from None
    audit(session, actor, "SET_POLICY", str(item), **body.model_dump(mode="json"))
    session.commit()
    return {"id": item}


@router.delete("/{kind}/{item}", status_code=204)
def remove_registry_entry(
    kind: Literal["providers", "models", "policy"],
    item: UUID, actor: ActorDep, session: SessionDep,
) -> Response:
    write_access(actor, session)
    table, pk = {
        "providers": ("model_providers", "id"),
        "models": ("models", "revision"),
        "policy": ("processing_policies", "id"),
    }[kind]
    try:
        result = session.execute(text(f"DELETE FROM cap_ai.{table} WHERE {pk}=:id"), {"id": item})
    except IntegrityError:
        raise HTTPException(409, "Registry entry is still referenced") from None
    if not result.rowcount:
        raise HTTPException(404, "Registry entry not found")
    audit(session, actor, "REMOVE_ENTRY", str(item), kind=kind)
    session.commit()
    return Response(status_code=204)
