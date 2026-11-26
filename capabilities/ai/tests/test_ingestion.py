# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import json
import os
import time
from io import BytesIO
from pathlib import Path
from subprocess import Popen

import pytest
from PIL import Image, ImageDraw, ImageFont
from sqlalchemy import text
from test_knowledge_access import PACKAGE, publish, seed_app

from dxos_ai.dispatch import publish_pending_jobs

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.fixture(scope="module")
def worker(admin_db, tmp_path_factory):
    broker = os.environ.get("AI_TEST_BROKER_URL")
    dsn = os.environ.get("AI_TEST_WORKER_DATABASE_URL")
    artifacts = os.environ.get("AI_PARSER_ARTIFACTS")
    if not broker or not dsn or not artifacts:
        pytest.skip("requires the disposable PGMQ, worker URL and Docling assets")
    root = tmp_path_factory.mktemp("package-artifacts")
    log = root / "celery.log"
    with log.open("w", encoding="utf-8") as file:
        process = Popen([
            "celery", "-A", "dxos_ai.worker", "worker", "--loglevel=INFO",
            "--without-gossip", "--without-mingle", "--without-heartbeat",
        ], env={
            **os.environ, "AI_BROKER_URL": broker, "DATABASE_URL": dsn,
            "RAG_PACKAGE_ARTIFACT_ROOT": str(root), "HF_HUB_OFFLINE": "1",
            "TRANSFORMERS_OFFLINE": "1", "HAYSTACK_TELEMETRY_ENABLED": "false",
        }, stdout=file, stderr=file)
    try:
        for _ in range(450):
            if " ready." in log.read_text():
                break
            assert process.poll() is None, log.read_text()
            time.sleep(0.1)
        else:
            pytest.fail("Celery readiness timeout. " + log.read_text())
        yield root, log
    finally:
        process.terminate()
        process.wait(timeout=15)


@pytest.fixture
def broker(client, worker, admin_db, monkeypatch):
    monkeypatch.setenv("AI_BROKER_URL", os.environ["AI_TEST_BROKER_URL"])
    from dxos_ai.worker import app

    with admin_db.begin() as db:
        db.execute(text("""
            UPDATE cap_ai.ingestion_jobs SET next_dispatch_at = now() + interval '1 hour'
            WHERE state IN ('PENDING','INDEXING')
        """))
        db.execute(text("UPDATE cap_ai.model_slots SET revision=NULL"))
    return app


def source_job(client, admin_db, actors, monkeypatch, root, data, mime_type):
    source = seed_app(
        admin_db, actors["user"], actors["other"], mode="ROLE", data=data,
    )
    artifact = (root / source.artifact)
    artifact.parent.mkdir(parents=True)
    artifact.write_bytes(data)
    if mime_type == "application/pdf":
        old = source.spec["_publication"]["sources"][0]
        old.update({"media_type": mime_type, "source_path": "knowledge/handbook.pdf"})
        declaration = source.spec["knowledge"]["sources"][0]
        declaration.update({"mediaType": mime_type, "path": "knowledge/handbook.pdf"})
        with admin_db.begin() as db:
            db.execute(text("UPDATE core.apps SET spec=CAST(:spec AS jsonb) WHERE app_id=:app"), {
                "app": source.app, "spec": json.dumps(source.spec),
            })
    result = publish(client, source.app, monkeypatch)
    assert result.status_code == 204, result.text
    with admin_db.connect() as db:
        return db.scalar(text("""
            SELECT j.id FROM cap_ai.ingestion_jobs j
            JOIN cap_ai.documents d ON d.desired_version=j.version_id WHERE d.app_id=:app
        """), {"app": source.app})


def wait_for_job(admin_db, job, log):
    deadline = time.monotonic() + 185
    while time.monotonic() < deadline:
        with admin_db.connect() as db:
            row = db.execute(text("""
                SELECT state,error_code,attempts,version_id FROM cap_ai.ingestion_jobs WHERE id=:id
            """), {"id": job}).one()
        if row.state == "READY":
            return row
        assert row.state != "FAILED", (row.error_code, log.read_text()[-7000:])
        time.sleep(0.25)
    pytest.fail(log.read_text()[-7000:])


def test_ingest_pdf_and_markdown(client, admin_db, actors, monkeypatch, worker, broker):
    root, log = worker
    pdf = (FIXTURES / "text.pdf").read_bytes()
    pdf_job = source_job(client, admin_db, actors, monkeypatch, root, pdf, "application/pdf")
    md_job = source_job(client, admin_db, actors, monkeypatch, root, PACKAGE, "text/markdown")
    assert publish_pending_jobs() == 2
    with admin_db.connect() as db:
        for job in (pdf_job, md_job):
            row = wait_for_job(admin_db, job, log)
            positions = db.scalars(text("""
                SELECT provenance->'pages' FROM cap_ai.document_chunks WHERE version_id=:version
            """), {"version": row.version_id}).all()
            assert positions
            if job == pdf_job:
                assert any(1 in pages for pages in positions)


def test_scanned_pdf_smoke(client, admin_db, actors, monkeypatch, worker, broker):
    root, log = worker
    image = Image.new("RGB", (1300, 640), "white")
    draw = ImageDraw.Draw(image)
    draw.text(
        (75, 150), "MAINTENANCE CHECKLIST", font=ImageFont.load_default(size=65), fill="black",
    )
    draw.text(
        (75, 275), "Inspect the safety valve.", font=ImageFont.load_default(size=58), fill="black",
    )
    raw = BytesIO()
    image.save(raw, format="PDF", resolution=160)
    job = source_job(
        client, admin_db, actors, monkeypatch, root, raw.getvalue(), "application/pdf",
    )
    assert publish_pending_jobs() == 1
    row = wait_for_job(admin_db, job, log)
    with admin_db.connect() as db:
        bodies = db.scalars(text(
            "SELECT body FROM cap_ai.document_chunks WHERE version_id=:version"
        ), {"version": row.version_id}).all()
        assert "maintenance" in " ".join(bodies).lower()


def test_publish_failure_recovers(client, admin_db, actors, monkeypatch, worker, broker):
    root, log = worker
    job = source_job(client, admin_db, actors, monkeypatch, root, PACKAGE, "text/markdown")
    sends = broker.send_task
    with monkeypatch.context() as patch:
        def unavailable(*args, **kwargs):
            raise ConnectionError

        patch.setattr(broker, "send_task", unavailable)
        assert publish_pending_jobs() == 0
    assert broker.send_task == sends
    with admin_db.begin() as db:
        assert db.scalar(text(
            "SELECT dispatched_at IS NULL FROM cap_ai.ingestion_jobs WHERE id=:job"
        ), {"job": job})
        db.execute(text(
            "UPDATE cap_ai.ingestion_jobs SET next_dispatch_at=now() WHERE id=:job"
        ), {"job": job})
    assert publish_pending_jobs() == 1
    wait_for_job(admin_db, job, log)


def test_redelivery_or_interruption_is_idempotent(
    client, admin_db, actors, monkeypatch, worker, broker,
):
    root, log = worker
    job = source_job(client, admin_db, actors, monkeypatch, root, PACKAGE, "text/markdown")
    with admin_db.begin() as db:
        db.execute(text("""
            UPDATE cap_ai.ingestion_jobs SET state='INDEXING', attempts=1,
                lease_token=gen_random_uuid(), lease_until=now()-interval '1 second' WHERE id=:job
        """), {"job": job})
    assert publish_pending_jobs() == 1
    broker.send_task("knowledge.ingest", args=[str(job)], retry=False)
    row = wait_for_job(admin_db, job, log)
    assert row.attempts == 2
    with admin_db.connect() as db:
        assert db.scalar(text("""
            SELECT count(*) FROM cap_ai.documents WHERE active_version=:version
        """), {"version": row.version_id}) == 1


def test_embedding_unavailable_keeps_lexical(
    client, admin_db, actors, monkeypatch, worker, broker,
):
    root, log = worker
    job = source_job(client, admin_db, actors, monkeypatch, root, PACKAGE, "text/markdown")
    assert publish_pending_jobs() == 1
    row = wait_for_job(admin_db, job, log)
    with admin_db.connect() as db:
        assert db.scalar(text("""
            SELECT count(*) FROM cap_ai.document_chunks WHERE version_id=:version
              AND embedding IS NULL AND search_vector @@ plainto_tsquery('simple','maintenance')
        """), {"version": row.version_id}) > 0
