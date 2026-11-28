# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.knowledge.access import can_read
from dxos_ai.security.identity import Actor
from dxos_ai.security.processing import SourceRef


@dataclass(frozen=True)
class Context:
    messages: list[dict]
    sources: tuple[SourceRef, ...]


def unpack_sources(values: list[dict]) -> tuple[SourceRef, ...]:
    return tuple(SourceRef(
        UUID(row["document_id"]), UUID(row["version_id"]),
    ) for row in values)


def pack_sources(sources: tuple[SourceRef, ...]) -> str:
    return json.dumps([
        {"document_id": str(row.document_id), "version_id": str(row.version_id)}
        for row in sources
    ])


def usable(session: Session, actor: Actor, sources: tuple[SourceRef, ...]) -> bool:
    return all(can_read(session, actor, row.document_id, row.version_id) for row in sources)


def build_context(session: Session, actor: Actor, conversation_id: UUID, seq: int) -> Context:
    rows = session.execute(text("""
        SELECT u.content AS question,m.content AS answer,m.sources,m.response
        FROM cap_ai.messages m
        JOIN cap_ai.messages u ON u.conversation_id=m.conversation_id AND u.seq=m.seq-1
        JOIN cap_ai.conversations c ON c.id=m.conversation_id
        WHERE m.conversation_id=:id AND c.owner_id=:actor AND c.deleted_at IS NULL
          AND m.seq<:seq AND m.role='assistant' AND m.status='ANSWERED'
        ORDER BY m.seq DESC LIMIT 18
    """), {"id": conversation_id, "actor": actor.user_id, "seq": seq}).all()
    recent: list[tuple[str, str]] = []
    summaries: list[str] = []
    recent_sources: set[SourceRef] = set()
    summary_sources: set[SourceRef] = set()
    for index, row in enumerate(rows):
        try:
            sources = unpack_sources(row.sources)
        except (ValueError, TypeError, KeyError):
            continue
        kind = row.response.get("mode")
        if kind not in ("GENERAL", "GROUNDED") or (kind == "GROUNDED" and not sources):
            continue
        material = recent_sources | summary_sources | set(sources)
        if len(material) > 30 or not usable(session, actor, sources):
            continue
        if index < 6:
            recent.append((row.question[:700], row.answer[:1000]))
            recent_sources.update(sources)
        elif len(summaries) < 8:
            summaries.append(f"Question: {row.question[:120]}\nAnswer: {row.answer[:180]}")
            summary_sources.update(sources)

    summary = "\n\n".join(reversed(summaries))
    session.execute(text("""
        UPDATE cap_ai.conversations SET summary=:summary, summary_sources=CAST(:sources AS jsonb)
        WHERE id=:id AND owner_id=:actor AND deleted_at IS NULL
    """), {
        "id": conversation_id, "actor": actor.user_id, "summary": summary,
        "sources": pack_sources(tuple(summary_sources)),
    })
    messages = [
        {"role": "user", "content": "Earlier conversation excerpts (quoted context):\n" + summary},
    ] if summary else []
    for question, answer in reversed(recent):
        messages.extend([
            {"role": "user", "content": question}, {"role": "assistant", "content": answer},
        ])
    return Context(messages, tuple(recent_sources | summary_sources))
