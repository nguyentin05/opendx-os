# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import logging
from threading import Event

from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.database import engine

MAX_PUBLISH_ATTEMPTS = 8
BROKER_RECOVERY_SECONDS = 360


def publish_pending_jobs() -> int:
    from dxos_ai.worker import app

    published = 0
    for _ in range(10):
        with Session(engine()) as session, session.begin():
            row = session.execute(text("""
                SELECT id,publish_attempts,attempts FROM cap_ai.ingestion_jobs
                WHERE next_dispatch_at<=clock_timestamp() AND (
                    state='PENDING' OR (state='INDEXING' AND lease_until<=clock_timestamp())
                )
                ORDER BY next_dispatch_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
            """)).one_or_none()
            if row is None:
                break
            if row.publish_attempts >= MAX_PUBLISH_ATTEMPTS or row.attempts >= 3:
                session.execute(text("""
                    UPDATE cap_ai.ingestion_jobs SET state='FAILED',
                        error_code='INGESTION_DELIVERY_UNAVAILABLE' WHERE id=:job
                """), {"job": row.id})
                continue
            try:
                app.send_task("knowledge.ingest", args=[str(row.id)], retry=False)
            except Exception:
                accepted = False
            else:
                accepted = True
                published += 1
            wait = (
                BROKER_RECOVERY_SECONDS if accepted else 2 ** min(row.publish_attempts, 6)
            )
            session.execute(text("""
                UPDATE cap_ai.ingestion_jobs SET publish_attempts=publish_attempts+1,
                    next_dispatch_at=clock_timestamp() + make_interval(secs=>:wait),
                    dispatched_at=CASE WHEN :accepted THEN clock_timestamp() ELSE dispatched_at END
                WHERE id=:job
            """), {"job": row.id, "wait": wait, "accepted": accepted})
    return published


def poll_dispatch(stop: Event) -> None:
    while not stop.is_set():
        try:
            publish_pending_jobs()
        except Exception:
            logging.getLogger(__name__).warning("AI ingestion dispatch unavailable")
        stop.wait(2)
