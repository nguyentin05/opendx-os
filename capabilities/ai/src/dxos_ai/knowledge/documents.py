# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from typing import Literal
from uuid import UUID

from fastapi import APIRouter, HTTPException, Request, Response
from fastapi.responses import RedirectResponse
from pydantic import BaseModel
from sqlalchemy import text

from dxos_ai.knowledge.sources import enqueue, package_original
from dxos_ai.models.registry import Input
from dxos_ai.security.identity import ActorDep, SessionDep, holds_role

router = APIRouter(prefix="/api/cap/ai")
NOT_FOUND = "Document unavailable"


class RegistrationRequest(Input):
    file_id: UUID
    parser_profile: Literal["standard-text-v1"] = "standard-text-v1"


class Registration(BaseModel):
    document_id: UUID
    version_id: UUID
    job_id: UUID
    revision: int
    status: str


@router.post("/documents", status_code=201)
def register_document(actor: ActorDep, request: RegistrationRequest, session: SessionDep):
    if not holds_role(session, actor, lock=True):
        raise HTTPException(404, NOT_FOUND)
    source = session.execute(text("SELECT * FROM cap_ai.private_file(:actor,:role,:file)"), {
        "actor": actor.user_id, "role": actor.role, "file": request.file_id,
    }).mappings().one_or_none()
    if not source:
        raise HTTPException(404, NOT_FOUND)
    if source["scan_status"] != "CLEAN":
        raise HTTPException(422, "File is awaiting successful scanning")
    if source["mime_type"] not in ("application/pdf", "text/markdown"):
        raise HTTPException(415, "File format unavailable")
    args = dict(source) | {"owner": actor.user_id}
    doc_id = session.scalar(text("""
        INSERT INTO cap_ai.documents(app_id,owner_id,file_id,title)
        VALUES (:app_id,:owner,:id,:file_name)
        ON CONFLICT (owner_id,file_id) DO NOTHING RETURNING id
    """), args)
    if doc_id is None:
        row = session.execute(text("""
            SELECT d.id AS document_id,d.desired_version AS version_id,j.id AS job_id,d.revision,
                j.state AS status
            FROM cap_ai.documents d JOIN cap_ai.ingestion_jobs j ON j.version_id=d.desired_version
            WHERE d.owner_id=:owner AND d.file_id=:id AND d.deleted_at IS NULL
        """), args).mappings().one_or_none()
        if row is None:
            raise HTTPException(409, "File was already registered")
        return Registration.model_validate(row)
    version = session.scalar(text("""
        INSERT INTO cap_ai.document_versions(document_id,sha256,size_bytes,media_type)
        VALUES (:doc,:sha256,:size_bytes,:mime_type) RETURNING id
    """), args | {"doc": doc_id})
    session.execute(text("UPDATE cap_ai.documents SET desired_version=:ver WHERE id=:doc"), {
        "doc": doc_id, "ver": version,
    })
    job = enqueue(session, version)
    session.commit()
    return Registration(
        document_id=doc_id, version_id=version, job_id=job, revision=1, status="PENDING",
    )


@router.get("/documents")
def list_documents(
    actor: ActorDep, session: SessionDep, cursor: UUID | None = None,
    scope: Literal["PERSONAL"] = "PERSONAL",
) -> dict:
    rows = session.execute(text("""
        SELECT d.id AS document_id,d.title,d.revision,
            COALESCE(j.state,v.status) AS desired_version_status,a.status AS active_version_status,
            j.error_code
        FROM cap_ai.documents d JOIN cap_ai.document_versions v ON v.id=d.desired_version
        LEFT JOIN cap_ai.document_versions a ON a.id=d.active_version
        LEFT JOIN cap_ai.ingestion_jobs j ON j.version_id=v.id
        WHERE d.owner_id=:actor AND (CAST(:cursor AS uuid) IS NULL OR d.id>:cursor)
          AND cap_ai.source_access(:actor,:role,d.id,v.id,false)
        ORDER BY d.id LIMIT 51
    """), {"actor": actor.user_id, "role": actor.role, "cursor": cursor}).mappings().all()
    return {
        "documents": [
            dict(row) | {"warnings": [row["error_code"]] if row["error_code"] else []}
            for row in rows[:50]
        ],
        "next_cursor": rows[49]["document_id"] if len(rows) > 50 else None,
    }


def if_match(request: Request) -> int:
    value = request.headers.get("if-match", "")
    digits = value[1:-1] if value.startswith('"') and value.endswith('"') else ""
    if not digits.isascii() or not digits.isdecimal() or len(digits) > 18:
        raise HTTPException(422, "Document revision required")
    return int(digits)


@router.delete("/documents/{doc_id}", status_code=204)
def delete_document(
    doc_id: UUID, request: Request, actor: ActorDep, session: SessionDep,
) -> Response:
    if not holds_role(session, actor, lock=True):
        raise HTTPException(404, NOT_FOUND)
    row = session.execute(text("""
        SELECT revision,deleted_at FROM cap_ai.documents
        WHERE id=:doc AND owner_id=:actor FOR UPDATE
    """), {"doc": doc_id, "actor": actor.user_id}).one_or_none()
    if row is None:
        raise HTTPException(404, NOT_FOUND)
    if row.deleted_at is not None:
        return Response(status_code=204)
    if row.revision != if_match(request):
        raise HTTPException(409, "Document revision changed")
    session.execute(text("""
        UPDATE cap_ai.documents SET deleted_at=clock_timestamp(),revision=revision+1 WHERE id=:doc
    """), {"doc": doc_id})
    session.commit()
    return Response(status_code=204)


@router.get("/documents/{doc_id}/original")
def original(doc_id: UUID, actor: ActorDep, session: SessionDep) -> Response:
    args = {"doc": doc_id, "actor": actor.user_id, "role": actor.role}
    row = session.execute(text("""
        SELECT d.title,d.file_id,v.id AS version_id,v.sha256,v.size_bytes,v.media_type,
            v.artifact_ref FROM cap_ai.documents d
        JOIN cap_ai.document_versions v ON v.id=COALESCE(d.active_version,d.desired_version)
        WHERE d.id=:doc AND cap_ai.source_access(:actor,:role,d.id,v.id,false)
    """), args).one_or_none()
    if not row:
        raise HTTPException(404, NOT_FOUND)
    if row.file_id:
        return RedirectResponse("/api/cap/process/files/" + str(row.file_id), headers={
            "Cache-Control": "no-store",
        })
    try:
        source = package_original(row)
    except (OSError, ValueError):
        raise HTTPException(503, "Original file unavailable") from None
    if not session.scalar(text("SELECT cap_ai.source_access(:actor,:role,:doc,:ver,false)"), {
        **args, "ver": row.version_id,
    }):
        raise HTTPException(404, NOT_FOUND)
    return Response(source.data, media_type="application/octet-stream", headers={
        "Cache-Control": "no-store",
        "Content-Disposition": "attachment",
        "X-Content-Type-Options": "nosniff",
    })


@router.get("/app-knowledge")
def app_knowledge(app_id: str, actor: ActorDep, session: SessionDep) -> dict:
    rows = session.execute(text("""
        SELECT d.id AS document_id, d.source_id, d.title,v.status,
            cap_ai.source_access(:actor,:role,d.id,v.id,false) AS readable,
            cap_ai.source_access(:actor,:role,d.id,v.id,true) AS manageable,
            d.active_version IS NOT NULL AND
              cap_ai.source_access(:actor,:role,d.id,d.active_version,false)
              AS active_version_available
        FROM cap_ai.documents d JOIN cap_ai.document_versions v ON v.id=d.desired_version
        WHERE d.app_id=:app AND d.source_id IS NOT NULL
        ORDER BY d.source_id
    """), {"app": app_id, "actor": actor.user_id, "role": actor.role}).mappings()
    return {"sources": [
        {**row, "title": row["title"] if row["readable"] else None}
        for row in rows if row["readable"] or row["manageable"]
    ]}


def management_row(doc_id: UUID, actor: ActorDep, session: SessionDep, lock: bool = False):
    if lock and not holds_role(session, actor, lock=True):
        raise HTTPException(404, NOT_FOUND)
    lock_clause = "FOR UPDATE OF d" if lock else ""
    result = session.execute(text(f"""
        SELECT d.revision,d.app_id,v.incarnation,v.authority_generation,
            cap_ai.package_source(d.app_id,d.source_id)->'declaration'->'access' AS ceiling
        FROM cap_ai.documents d
        JOIN cap_ai.document_versions v ON v.id=d.desired_version
        WHERE d.id=:doc AND cap_ai.source_access(:actor,:role,d.id,v.id,true)
        {lock_clause}
    """), {"doc": doc_id, "actor": actor.user_id, "role": actor.role}).one_or_none()
    if result is None:
        raise HTTPException(404, NOT_FOUND)
    return result


@router.get("/documents/{doc_id}/grant-management")
def manage_grants(
    doc_id: UUID, actor: ActorDep, session: SessionDep, cursor: UUID | None = None,
) -> dict:
    source = management_row(doc_id, actor, session)
    rows = session.execute(text("""
        SELECT user_id,
            CASE WHEN incarnation=:inc AND authority_generation=:gen THEN 'ACTIVE'
                ELSE 'STALE' END AS status
        FROM cap_ai.document_grants WHERE document_id=:doc
          AND (CAST(:cursor AS uuid) IS NULL OR user_id>:cursor)
        ORDER BY user_id LIMIT 51
    """), {
        "doc": doc_id, "inc": source.incarnation, "gen": source.authority_generation,
        "cursor": cursor,
    }).mappings().all()
    return {
        "document_id": doc_id, "revision": source.revision, "ceiling": source.ceiling,
        "authorization_generation": source.authority_generation,
        "pending_publication": False, "grants": [dict(row) for row in rows[:50]],
        "next_cursor": rows[49]["user_id"] if len(rows) > 50 else None,
    }


class GrantRequest(Input):
    user_id: UUID


def change_grant(
    doc_id: UUID, user_id: UUID, request: Request, actor: ActorDep, session: SessionDep,
    revoke: bool,
) -> Response:
    session.execute(text("SELECT pg_advisory_xact_lock(hashtext('dxos.operator.publication'))"))
    source = management_row(doc_id, actor, session, lock=True)
    if source.revision != if_match(request):
        raise HTTPException(409, "Document revision changed")
    args = {
        "document": doc_id, "user": user_id, "actor": actor.user_id,
        "inc": source.incarnation, "gen": source.authority_generation,
    }
    if revoke:
        session.execute(text("""
            DELETE FROM cap_ai.document_grants WHERE document_id=:document AND user_id=:user
        """), args)
    else:
        if not session.scalar(text("SELECT cap_ai.grant_eligible(:document,:user)"), args):
            raise HTTPException(404, NOT_FOUND)
        session.execute(text("""
            INSERT INTO cap_ai.document_grants(
                document_id,user_id,incarnation,authority_generation,granted_by
            ) VALUES (:document,:user,:inc,:gen,:actor)
            ON CONFLICT(document_id,user_id) DO UPDATE SET
                incarnation=EXCLUDED.incarnation,authority_generation=EXCLUDED.authority_generation,
                granted_by=EXCLUDED.granted_by
        """), args)
    session.execute(text("""
        UPDATE cap_ai.documents SET revision=revision+1 WHERE id=:document
    """), args)
    session.commit()
    return Response(status_code=204)


@router.post("/documents/{doc_id}/grants", status_code=204)
def grant_read(
    doc_id: UUID, body: GrantRequest, request: Request, actor: ActorDep, session: SessionDep,
) -> Response:
    return change_grant(doc_id, body.user_id, request, actor, session, revoke=False)


@router.delete("/documents/{doc_id}/grants/{user_id}", status_code=204)
def revoke_read(
    doc_id: UUID, user_id: UUID, request: Request, actor: ActorDep, session: SessionDep,
) -> Response:
    return change_grant(doc_id, user_id, request, actor, session, revoke=True)
