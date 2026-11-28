# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.models.registry import DataClass, ModelBinding
from dxos_ai.security.identity import Actor, holds_role


@dataclass(frozen=True)
class SourceRef:
    document_id: UUID
    version_id: UUID


@dataclass(frozen=True)
class ProcessingContext:
    actor: Actor | None
    operation_key: str
    query: bool = True
    sources: tuple[SourceRef, ...] = ()
    ingestion_job_id: UUID | None = None
    ingestion_lease: UUID | None = None


def processing_allowed(
    session: Session, model: ModelBinding, classes: set[tuple[DataClass, str | None]],
) -> bool:
    rows = session.execute(text("""
        SELECT app_id, data_class, decision
        FROM cap_ai.processing_policies
        WHERE purpose=:purpose AND (revision IS NULL OR revision=:revision)
    """), {"purpose": model.purpose, "revision": model.revision}).mappings().all()
    for data_class, app_id in classes:
        matching = [
            row for row in rows
            if row["data_class"] == data_class and row["app_id"] in (None, app_id)
        ]
        decisions = {row["decision"] for row in matching}
        if "DENY" in decisions or (model.execution == "HOSTED" and "ALLOW" not in decisions):
            return False
    return bool(classes)


def may_process(session: Session, model: ModelBinding, context: ProcessingContext) -> bool:
    if context.actor is not None and not holds_role(session, context.actor, lock=True):
        return False
    if context.actor is None and context.ingestion_job_id is None:
        return False
    if context.ingestion_job_id is None and not context.query:
        return False
    classes: set[tuple[DataClass, str | None]] = {("QUERY", None)} if context.query else set()
    if context.sources or context.ingestion_job_id is not None:
        from dxos_ai.knowledge.access import processing_classes

        source_classes = processing_classes(session, context)
        if source_classes is None:
            return False
        classes.update(source_classes)
    return processing_allowed(session, model, classes)
