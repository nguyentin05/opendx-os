# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
from typing import Annotated
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.encoders import jsonable_encoder
from pydantic import Field
from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.agent.graph import Turn, run_agent
from dxos_ai.agent.memory import build_context, pack_sources, unpack_sources, usable
from dxos_ai.knowledge.retrieval import resolve_citation
from dxos_ai.models.registry import Input
from dxos_ai.security.identity import Actor, SessionDep, holds_role, subject

router = APIRouter(prefix="/api/cap/ai/conversations")
NO_CONVERSATION = "Conversation unavailable"


def agent_actor(request: Request, session: SessionDep) -> Actor:
    user, expires = subject(request)
    actor = Actor(user, None, expires)
    if not holds_role(session, actor):
        raise HTTPException(403, "Current account required")
    return actor


AgentActor = Annotated[Actor, Depends(agent_actor)]


class ConversationInput(Input):
    title: str = Field(default="Private conversation", min_length=1, max_length=160)


class TurnInput(Input):
    query: str = Field(min_length=1, max_length=3000)


def get_conversation(session: Session, actor: Actor, conversation_id: UUID, *, lock=False):
    suffix = "FOR UPDATE" if lock else ""
    row = session.execute(text(f"""
        SELECT id FROM cap_ai.conversations WHERE id=:id AND owner_id=:owner AND deleted_at IS NULL
        {suffix}
    """), {"id": conversation_id, "owner": actor.user_id}).one_or_none()
    if not row:
        raise HTTPException(404, NO_CONVERSATION)


@router.get("")
def list_conversations(actor: AgentActor, session: SessionDep, cursor: UUID | None = None) -> dict:
    rows = session.execute(text("""
        SELECT id,title,created_at FROM cap_ai.conversations
        WHERE owner_id=:owner AND deleted_at IS NULL
          AND (CAST(:cursor AS uuid) IS NULL OR id<:cursor)
        ORDER BY id DESC LIMIT 51
    """), {"owner": actor.user_id, "cursor": cursor}).mappings().all()
    return {
        "conversations": [dict(row) for row in rows[:50]],
        "next_cursor": rows[49]["id"] if len(rows) > 50 else None,
    }


@router.post("", status_code=201)
def create_conversation(body: ConversationInput, actor: AgentActor, session: SessionDep) -> dict:
    if not holds_role(session, actor, lock=True):
        raise HTTPException(403, "Current account required")
    row = session.execute(text("""
        INSERT INTO cap_ai.conversations(owner_id,title) VALUES (:actor,:title)
        RETURNING id,title,created_at
    """), {"actor": actor.user_id, "title": body.title}).mappings().one()
    session.commit()
    return dict(row)


@router.get("/{conversation_id}")
def transcript(
    conversation_id: UUID, actor: AgentActor, session: SessionDep, after_seq: int = 0,
) -> dict:
    get_conversation(session, actor, conversation_id)
    rows = session.execute(text("""
        SELECT m.id,m.seq,m.role,m.status,m.content,m.response FROM cap_ai.messages m
        JOIN cap_ai.conversations c ON c.id=m.conversation_id
        WHERE c.id=:id AND c.owner_id=:owner AND c.deleted_at IS NULL
          AND m.seq>:seq ORDER BY m.seq LIMIT 51
    """), {"id": conversation_id, "owner": actor.user_id, "seq": after_seq}).mappings().all()
    return {
        "messages": [dict(row) for row in rows[:50]],
        "next_after_seq": rows[49]["seq"] if len(rows) > 50 else None,
    }


@router.delete("/{conversation_id}", status_code=204)
def delete_conversation(
    conversation_id: UUID, actor: AgentActor, session: SessionDep,
) -> Response:
    changed = session.execute(text("""
        UPDATE cap_ai.conversations SET deleted_at=clock_timestamp(),
            summary='',summary_sources='[]'
        WHERE id=:id AND owner_id=:owner AND deleted_at IS NULL
    """), {"id": conversation_id, "owner": actor.user_id})
    if not changed.rowcount:
        raise HTTPException(404, NO_CONVERSATION)
    session.commit()
    return Response(status_code=204)


def store_turn(
    session: Session, conversation_id: UUID, seq: int, result: Turn,
) -> dict:
    citations = []
    for item in result.citations:
        url = (
            f"/api/cap/ai/conversations/{conversation_id}/citations/{item.citation_id}"
            f"?document_id={item.document_id}&message_seq={seq + 1}"
            f"&version_id={item.version_id}"
        )
        citations.append({
            "id": item.citation_id, "title": item.title, "snippet": item.text[:500], "url": url,
        })
    payload = jsonable_encoder({
        "status": result.status, "mode": result.mode, "answer": result.answer,
        "reason": result.reason, "citations": citations, "message_seq": seq + 1,
    })
    session.execute(text("""
        UPDATE cap_ai.messages SET status='RECEIVED',lease_until=NULL
        WHERE conversation_id=:id AND seq=:seq
    """), {"id": conversation_id, "seq": seq})
    session.execute(text("""
        INSERT INTO cap_ai.messages(conversation_id,seq,role,content,status,response,sources)
        VALUES (:id,:seq,'assistant',:content,:status,
            CAST(:response AS jsonb),CAST(:sources AS jsonb))
    """), {
        "id": conversation_id, "seq": seq + 1, "content": result.answer or "",
        "status": result.status, "response": json.dumps(payload),
        "sources": pack_sources(result.sources),
    })
    return payload


def reply(
    session: Session, actor: Actor, conversation_id: UUID, client_turn_id: UUID, query: str,
) -> dict:
    get_conversation(session, actor, conversation_id, lock=True)
    args = {"id": conversation_id, "key": client_turn_id, "query": query}
    pending = session.execute(text("""
        SELECT seq,lease_until<=clock_timestamp() AS expired FROM cap_ai.messages
        WHERE conversation_id=:id AND status='RUNNING'
    """), args).one_or_none()
    if pending is not None and pending.expired:
        store_turn(session, conversation_id, pending.seq, Turn(
            "UNAVAILABLE", "INSUFFICIENT", reason="MODEL_CALL_UNCERTAIN",
        ))
        pending = None
    previous = session.execute(text("""
        SELECT u.content,a.response,a.sources FROM cap_ai.messages u
        LEFT JOIN cap_ai.messages a ON a.conversation_id=u.conversation_id AND a.seq=u.seq+1
        WHERE u.conversation_id=:id AND u.client_turn_id=:key
    """), args).one_or_none()
    if previous is not None:
        if previous.content != query:
            raise HTTPException(409, "Turn identity already used")
        if previous.response is None:
            raise HTTPException(409, "Turn is pending")
        payload = previous.response
        if not usable(session, actor, unpack_sources(previous.sources)):
            payload = {
                "status": "NO_EVIDENCE", "mode": "INSUFFICIENT", "answer": None,
                "citations": [], "reason": "AUTHORIZATION_CHANGED",
            }
        session.commit()
        return payload
    if pending is not None:
        raise HTTPException(409, "Turn is pending")

    seq = session.scalar(text("""
        INSERT INTO cap_ai.messages(
            conversation_id,seq,role,content,status,client_turn_id,lease_until
        )
        SELECT :id,COALESCE(max(seq),0)+1,'user',:query,'RUNNING',:key,
            clock_timestamp()+interval '200 seconds'
        FROM cap_ai.messages WHERE conversation_id=:id RETURNING seq
    """), args)
    context = build_context(session, actor, conversation_id, seq)
    session.commit()

    try:
        result = run_agent(actor, f"turn:{conversation_id}:{client_turn_id}", query, context)
    except Exception:
        result = Turn("UNAVAILABLE", "INSUFFICIENT", reason="ASSISTANT_UNAVAILABLE")

    get_conversation(session, actor, conversation_id, lock=True)
    active = session.scalar(text("""
        SELECT status='RUNNING' AND lease_until>clock_timestamp()
        FROM cap_ai.messages WHERE conversation_id=:id AND seq=:seq
    """), {"id": conversation_id, "seq": seq})
    if not active:
        raise HTTPException(409, "Turn has expired")
    if not holds_role(session, actor, lock=True) or not usable(session, actor, result.sources):
        result = Turn("NO_EVIDENCE", "INSUFFICIENT", reason="AUTHORIZATION_CHANGED")
    payload = store_turn(session, conversation_id, seq, result)
    session.commit()
    return payload


@router.post("/{conversation_id}/messages")
def send(
    conversation_id: UUID, body: TurnInput, request: Request,
    actor: AgentActor, session: SessionDep,
) -> dict:
    keys = request.headers.getlist("idempotency-key")
    try:
        if len(keys) != 1:
            raise ValueError
        key = UUID(keys[0])
    except ValueError:
        raise HTTPException(422, "A client turn ID is required") from None
    return reply(session, actor, conversation_id, key, body.query)


@router.get("/{conversation_id}/citations/{citation_id}")
def preview(
    conversation_id: UUID, citation_id: UUID, document_id: UUID, version_id: UUID,
    message_seq: int, actor: AgentActor, session: SessionDep,
) -> dict:
    payload = session.scalar(text("""
        SELECT m.response FROM cap_ai.messages m
        JOIN cap_ai.conversations c ON c.id=m.conversation_id
        WHERE c.id=:id AND c.owner_id=:owner AND c.deleted_at IS NULL AND m.seq=:seq
    """), {"id": conversation_id, "owner": actor.user_id, "seq": message_seq})
    citations = payload.get("citations", []) if payload else []
    if not any(row["id"] == str(citation_id) for row in citations):
        raise HTTPException(404, "Citation unavailable")
    item = resolve_citation(
        session, actor, citation_id, document_id=document_id, version_id=version_id,
    )
    if item is None:
        raise HTTPException(404, "Citation unavailable")
    return item.hit()
