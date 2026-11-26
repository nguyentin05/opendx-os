# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from uuid import UUID

from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.models.registry import DataClass
from dxos_ai.security.identity import Actor
from dxos_ai.security.processing import ProcessingContext


def can_read(session: Session, actor: Actor, document_id: UUID, version_id: UUID) -> bool:
    return bool(session.scalar(text("""
        SELECT cap_ai.source_access(:actor, :role, :doc, :version, false)
        FROM cap_ai.documents WHERE id=:doc AND active_version=:version
    """), {
        "actor": actor.user_id, "role": actor.role, "doc": document_id, "version": version_id,
    }))


def processing_classes(
    session: Session, context: ProcessingContext,
) -> set[tuple[DataClass, str | None]] | None:
    result: set[tuple[DataClass, str | None]] = set()
    if context.ingestion_job_id:
        source = session.execute(text("""
            SELECT d.app_id, d.owner_id FROM cap_ai.ingestion_jobs j
            JOIN cap_ai.document_versions v ON v.id=j.version_id
            JOIN cap_ai.documents d ON d.id=v.document_id AND d.desired_version=v.id
            WHERE j.id=:job AND j.lease_token=:lease AND j.state='INDEXING'
              AND j.lease_until > clock_timestamp()
              AND cap_ai.source_current(d.id,v.id)
        """), {"job": context.ingestion_job_id, "lease": context.ingestion_lease}).one_or_none()
        if source is None:
            return None
        result.add(
            ("PERSONAL", None) if source.owner_id else ("APPLICATION", source.app_id)
        )
    for ref in context.sources:
        if not context.actor or not can_read(
            session, context.actor, ref.document_id, ref.version_id,
        ):
            return None
        source = session.execute(text("""
            SELECT owner_id, app_id FROM cap_ai.documents WHERE id=:doc
        """), {"doc": ref.document_id}).one()
        result.add(
            ("PERSONAL", None) if source.owner_id else ("APPLICATION", source.app_id)
        )
    return result
