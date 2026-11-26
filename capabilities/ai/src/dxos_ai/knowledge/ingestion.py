# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
import time
from dataclasses import dataclass
from hashlib import sha256
from secrets import token_urlsafe
from uuid import UUID, uuid4

from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.database import engine
from dxos_ai.knowledge.sources import package_original, private_original
from dxos_ai.models.gateway import ModelError, ModelGateway
from dxos_ai.models.registry import ModelPurpose, binding
from dxos_ai.security.processing import ProcessingContext

LEASE_SECONDS = 300
MAX_ATTEMPTS = 3


@dataclass(frozen=True)
class Claim:
    job_id: UUID
    token: UUID
    delegation: str
    attempts: int


def claim_job(job_id: UUID) -> Claim | None:
    token = uuid4()
    delegation = token_urlsafe(32)
    with Session(engine()) as session, session.begin():
        result = session.execute(text("""
            UPDATE cap_ai.ingestion_jobs SET state='INDEXING',attempts=attempts+1,
                lease_token=:token,lease_until=clock_timestamp() + make_interval(secs=>:lease),
                delegation_hash=:delegation
            WHERE id=:job AND attempts<:max_attempts AND (
                state='PENDING' OR (state='INDEXING' AND lease_until<=clock_timestamp())
            )
            RETURNING attempts,version_id
        """), {
            "job": job_id, "token": token, "lease": LEASE_SECONDS,
            "delegation": sha256(delegation.encode()).hexdigest(), "max_attempts": MAX_ATTEMPTS,
        }).one_or_none()
        if result is None:
            return None
        session.execute(text("""
            UPDATE cap_ai.document_versions SET status='INDEXING' WHERE id=:id
        """), {"id": result.version_id})
        return Claim(job_id, token, delegation, result.attempts)


def read_original(claim: Claim):
    with Session(engine()) as session:
        row = session.execute(text("""
            SELECT d.title,d.file_id,v.sha256,v.size_bytes,v.media_type,v.artifact_ref
            FROM cap_ai.ingestion_jobs j
            JOIN cap_ai.document_versions v ON v.id=j.version_id
            JOIN cap_ai.documents d ON d.id=v.document_id AND d.desired_version=v.id
            WHERE j.id=:job AND j.lease_token=:token AND j.lease_until>clock_timestamp()
              AND cap_ai.source_current(d.id,v.id)
        """), {"job": claim.job_id, "token": claim.token}).one_or_none()
    if row is None:
        raise ValueError("SOURCE_UNAVAILABLE")
    return private_original(row, claim.delegation) if row.file_id else package_original(row)


def ingestion_pipeline():
    from haystack import Pipeline, component

    from dxos_ai.knowledge.parser import Chunk, parse_document

    @component
    class Parse:
        @component.output_types(chunks=list[Chunk], picture=bytes | None)
        def run(self, claim: Claim, pictures: bool) -> dict:
            source = parse_document(read_original(claim), pictures=pictures)
            return {"chunks": source.chunks, "picture": source.picture}

    @component
    class Enrich:
        @component.output_types(rows=list[dict], warning=str | None)
        def run(self, chunks: list[Chunk], picture: bytes | None, claim: Claim) -> dict:
            model = ModelGateway()

            def context(operation: str) -> ProcessingContext:
                return ProcessingContext(
                    None, f"ingest:{claim.job_id}:{operation}", query=False,
                    ingestion_job_id=claim.job_id, ingestion_lease=claim.token,
                )

            warning = None
            if picture:
                try:
                    caption = model.vision(
                        picture, "Describe only the visible content for document indexing.",
                        context("vision"),
                    ).text
                    if caption:
                        chunks.append(Chunk(caption[:1800], (), ("vlm-picture",), ()))
                except ModelError as exc:
                    if str(exc) == "MODEL_CALL_UNCERTAIN":
                        warning = str(exc)

            rows = [
                {"body": chunk.text, "provenance": json.dumps({
                    "section_path": chunk.section_path, "element_ids": chunk.element_ids,
                    "pages": chunk.pages,
                }), "revision": None, "dimension": None, "vector": None}
                for chunk in chunks
            ]
            deadline = time.monotonic() + 45
            for offset in range(0, len(chunks), 8):
                if time.monotonic() > deadline:
                    break
                try:
                    result = model.embed(
                        [chunk.text for chunk in chunks[offset:offset + 8]],
                        context(f"embedding:{offset}"),
                    )
                except ModelError as exc:
                    if str(exc) == "MODEL_CALL_UNCERTAIN":
                        warning = str(exc)
                    break
                for index, vector in enumerate(result.vectors, start=offset):
                    rows[index].update({
                        "revision": result.revision, "dimension": result.dimension,
                        "vector": str(vector),
                    })
            return {"rows": rows, "warning": warning}

    pipeline = Pipeline()
    pipeline.add_component("parse", Parse())
    pipeline.add_component("enrich", Enrich())
    pipeline.connect("parse.chunks", "enrich.chunks")
    pipeline.connect("parse.picture", "enrich.picture")
    return pipeline


def publish_version(claim: Claim, rows: list[dict], warning: str | None) -> None:
    with Session(engine()) as session, session.begin():
        row = session.execute(text("""
            SELECT v.id AS ver,d.id AS doc FROM cap_ai.ingestion_jobs j
            JOIN cap_ai.document_versions v ON v.id=j.version_id
            JOIN cap_ai.documents d ON d.id=v.document_id AND d.desired_version=v.id
            WHERE j.id=:job AND j.lease_token=:token AND j.lease_until>clock_timestamp()
              AND cap_ai.source_current(d.id,v.id)
            FOR UPDATE OF j,d
        """), {"job": claim.job_id, "token": claim.token}).one_or_none()
        if row is None:
            raise ValueError("SOURCE_UNAVAILABLE")

        args = [{"version": row.ver, "ordinal": ordinal, **row_data}
                for ordinal, row_data in enumerate(rows)]
        session.execute(text("""
            INSERT INTO cap_ai.document_chunks(
                version_id,ordinal,body,provenance,model_revision,dimension,embedding
            ) VALUES (
                :version,:ordinal,:body,CAST(:provenance AS jsonb),:revision,:dimension,
                CAST(:vector AS public.vector)
            )
        """), args)
        session.execute(text("""
            UPDATE cap_ai.document_versions SET status='READY' WHERE id=:version
        """), {"version": row.ver})
        changed = session.execute(text("""
            UPDATE cap_ai.documents SET active_version=:version
            WHERE id=:doc AND desired_version=:version AND cap_ai.source_current(id,:version)
        """), {"doc": row.doc, "version": row.ver})
        if changed.rowcount != 1:
            raise ValueError("SOURCE_UNAVAILABLE")
        session.execute(text("""
            UPDATE cap_ai.ingestion_jobs SET state='READY',error_code=:warning,
                lease_token=NULL,lease_until=NULL,delegation_hash=NULL WHERE id=:job
        """), {"job": claim.job_id, "warning": warning})


def fail_job(claim: Claim, code: str, *, retry: bool) -> None:
    state = "PENDING" if retry and claim.attempts < MAX_ATTEMPTS else "FAILED"
    with Session(engine()) as session, session.begin():
        version = session.scalar(text("""
            UPDATE cap_ai.ingestion_jobs SET state=:state,error_code=:code,
                lease_token=NULL,lease_until=NULL,delegation_hash=NULL,
                next_dispatch_at=clock_timestamp() + make_interval(secs=>:delay)
            WHERE id=:job AND lease_token=:token RETURNING version_id
        """), {
            "job": claim.job_id, "token": claim.token, "state": state, "code": code,
            "delay": 5 * 2 ** (claim.attempts - 1),
        })
        if version is not None:
            session.execute(text("""
                UPDATE cap_ai.document_versions SET status=:state WHERE id=:version
            """), {"version": version, "state": state})


def process_ingestion(job_id: UUID) -> None:
    claim = claim_job(job_id)
    if claim is None:
        return
    try:
        with Session(engine()) as session:
            pictures = binding(session, ModelPurpose.VLM) is not None
        result = ingestion_pipeline().run({
            "parse": {"claim": claim, "pictures": pictures}, "enrich": {"claim": claim},
        })["enrich"]
        publish_version(claim, result["rows"], result["warning"])
    except ValueError:
        fail_job(claim, "SOURCE_OR_PARSE_UNAVAILABLE", retry=False)
    except Exception:
        fail_job(claim, "INGESTION_UNAVAILABLE", retry=True)
