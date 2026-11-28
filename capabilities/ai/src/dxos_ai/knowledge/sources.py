# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os
from dataclasses import dataclass
from hashlib import sha256
from pathlib import PurePosixPath
from uuid import UUID

import httpx
from fastapi import APIRouter, HTTPException, Request, Response
from pydantic import Field
from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.config import settings
from dxos_ai.models.registry import Input
from dxos_ai.security.identity import SessionDep, subject

MAX_BYTES = 20 * 1024 * 1024
router = APIRouter(prefix="/internal/v1")


@dataclass(frozen=True)
class VerifiedSource:
    title: str
    data: bytes
    media_type: str


def verified(data: bytes, source) -> VerifiedSource:
    if len(data) != source.size_bytes or sha256(data).hexdigest() != source.sha256:
        raise ValueError("SOURCE_CONTENT_MISMATCH")
    if source.media_type == "application/pdf":
        if not data.startswith(b"%PDF-"):
            raise ValueError("SOURCE_FORMAT_INVALID")
    elif source.media_type == "text/markdown":
        data.decode("utf-8")
        if b"\x00" in data:
            raise ValueError("SOURCE_FORMAT_INVALID")
    else:
        raise ValueError("SOURCE_FORMAT_INVALID")
    return VerifiedSource(source.title, data, source.media_type)


def package_original(source) -> VerifiedSource:
    ref = source.artifact_ref
    path = PurePosixPath(ref)
    if path.is_absolute() or str(path) != ref or ".." in path.parts or len(path.parts) < 2:
        raise ValueError("SOURCE_PATH_INVALID")
    root = settings().rag_package_artifact_root
    if not root:
        raise ValueError("SOURCE_STORAGE_UNAVAILABLE")
    parent = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
    try:
        for parent_name in path.parts[:-1]:
            child = os.open(
                parent_name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent,
            )
            os.close(parent)
            parent = child
        with os.fdopen(os.open(
            path.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent,
        ), "rb") as file:
            return verified(file.read(MAX_BYTES + 1), source)
    finally:
        os.close(parent)


def current_source(session: Session, document_id: UUID, version_id: UUID) -> bool:
    return bool(session.scalar(text("SELECT cap_ai.source_current(:doc,:ver)"), {
        "doc": document_id, "ver": version_id,
    }))


def service_actor(request: Request, expected: UUID | None) -> None:
    if not expected or any(key.startswith("x-hasura-") for key in request.headers):
        raise HTTPException(403, "Service identity required")
    user_id, _ = subject(request)
    if user_id != expected:
        raise HTTPException(403, "Service identity required")


class Publication(Input):
    schema_version: int = Field(ge=1, le=1)
    app_id: str = Field(pattern=r"^[a-z][a-z0-9_]{0,127}$")
    publication_generation: int = Field(gt=0, lt=2**63)
    spec_digest: str = Field(pattern=r"^[a-f0-9]{64}$")


def enqueue(session: Session, version_id: UUID) -> UUID:
    return session.scalar(text("""
        INSERT INTO cap_ai.ingestion_jobs(version_id) VALUES (:id)
        ON CONFLICT(version_id) DO UPDATE SET version_id=EXCLUDED.version_id RETURNING id
    """), {"id": version_id})


def reconcile_publication(session: Session, notification: Publication) -> None:
    args = notification.model_dump()
    session.execute(text("SELECT pg_advisory_xact_lock(hashtext('dxos.operator.publication'))"))
    snapshot = session.scalar(text("""
        SELECT cap_ai.publication(:app_id,:publication_generation,:spec_digest)
    """), args)
    if snapshot is None:
        raise HTTPException(409, "Stale publication")
    if snapshot["status"] != "Ready":
        return

    for source in snapshot["spec"]["_publication"]["sources"]:
        info = session.scalar(text("SELECT cap_ai.package_source(:app,:source)"), {
            "app": notification.app_id, "source": source["source_id"],
        })
        if info is None:
            raise HTTPException(404, "Package source unavailable")
        params = source | {
            "app": notification.app_id, "generation": notification.publication_generation,
        }
        doc = session.scalar(text("""
            INSERT INTO cap_ai.documents (app_id,source_id,title)
            VALUES (:app,:source_id,:source_id)
            ON CONFLICT(app_id,source_id) DO NOTHING RETURNING id
        """), params)
        if doc is None:
            doc = session.scalar(text("""
                SELECT id FROM cap_ai.documents WHERE app_id=:app AND source_id=:source_id
            """), params)
        version = session.scalar(text("""
            INSERT INTO cap_ai.document_versions (
                document_id, publication_generation, incarnation, authority_generation,
                declaration_hash, artifact_ref, sha256, size_bytes, media_type
            ) VALUES (
                :doc, :generation, :incarnation, :authorization_generation,
                :declaration_hash, :artifact_ref, :sha256, :size_bytes, :media_type
            ) ON CONFLICT (document_id, publication_generation) DO NOTHING RETURNING id
        """), params | {"doc": doc})
        if version is not None:
            session.execute(text("""
                UPDATE cap_ai.documents SET desired_version=:version, revision=revision+1
                WHERE id=:doc
            """), {"doc": doc, "version": version})
            enqueue(session, version)


@router.post("/package-sources/reconcile", status_code=204)
def notify_publication(body: Publication, request: Request, session: SessionDep) -> Response:
    service_actor(request, settings().rag_dispatcher_service_subject)
    try:
        UUID(request.headers["idempotency-key"])
    except (KeyError, ValueError):
        raise HTTPException(422, "Publication event ID required") from None
    reconcile_publication(session, body)
    session.commit()
    return Response(status_code=204)


class Delegation(Input):
    token: str = Field(min_length=32, max_length=200)
    worker_id: UUID
    source_id: UUID


@router.post("/source-delegations/validate")
def validate_delegation(body: Delegation, request: Request, session: SessionDep) -> dict:
    cfg = settings()
    service_actor(request, cfg.rag_file_service_subject)
    if body.worker_id != cfg.rag_worker_service_subject:
        raise HTTPException(404, "Source unavailable")
    source = session.execute(text("""
        SELECT 'UPLOAD' AS source_kind, d.file_id AS source_id, d.owner_id, v.sha256,
            j.lease_until AS expires_at
        FROM cap_ai.ingestion_jobs j
        JOIN cap_ai.document_versions v ON v.id=j.version_id
        JOIN cap_ai.documents d ON d.id=v.document_id AND d.desired_version=v.id
        WHERE d.file_id=:file AND j.delegation_hash=:token AND j.state='INDEXING'
          AND j.lease_until > clock_timestamp() AND cap_ai.source_current(d.id,v.id)
    """), {"file": body.source_id, "token": sha256(body.token.encode()).hexdigest()})
    row = source.mappings().one_or_none()
    if row is None:
        raise HTTPException(404, "Source unavailable")
    return dict(row)


def worker_bearer(http: httpx.Client) -> str:
    cfg = settings()
    if cfg.rag_worker_bearer_token:
        return cfg.rag_worker_bearer_token.get_secret_value()
    if not cfg.oidc_token_url or not cfg.rag_worker_client_secret:
        raise ValueError("SOURCE_IDENTITY_UNAVAILABLE")
    response = http.post(str(cfg.oidc_token_url), data={
        "grant_type": "client_credentials",
        "client_id": cfg.rag_worker_client_id,
        "client_secret": cfg.rag_worker_client_secret.get_secret_value(),
    })
    response.raise_for_status()
    return response.json()["access_token"]


def private_original(source, delegation: str) -> VerifiedSource:
    base = settings().rag_internal_file_url
    if not base:
        raise ValueError("SOURCE_STORAGE_UNAVAILABLE")
    with httpx.Client(timeout=15, trust_env=False, follow_redirects=False) as http:
        with http.stream("GET", f"{str(base).rstrip('/')}/internal/v1/files/{source.file_id}",
                         headers={"Authorization": f"Bearer {worker_bearer(http)}",
                                  "X-Source-Delegation": delegation}) as response:
            response.raise_for_status()
            data = bytearray()
            for chunk in response.iter_bytes(65536):
                data.extend(chunk)
                if len(data) > MAX_BYTES:
                    raise ValueError("SOURCE_CONTENT_MISMATCH")
    return verified(bytes(data), source)
