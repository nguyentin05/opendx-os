# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os
from urllib.parse import urlsplit

from celery import Celery
from kombu import Queue

broker_url = os.environ["AI_BROKER_URL"]
broker = urlsplit(broker_url)
if broker.scheme != "pgmq" or not broker.hostname or not broker.path.strip("/"):
    raise ValueError("invalid AI broker URL")

app = Celery("dxos_ai", broker=broker_url)
app.conf.update(
    accept_content=["json"],
    task_serializer="json",
    task_default_queue="dxos_ai_ingest",
    task_queues=(Queue("dxos_ai_ingest", no_declare=True),),
    task_create_missing_queues=False,
    task_ignore_result=True,
    task_acks_late=True,
    task_reject_on_worker_lost=True,
    task_soft_time_limit=240,
    task_time_limit=270,
    worker_prefetch_multiplier=1,
    worker_concurrency=1,
    worker_enable_remote_control=False,
    worker_send_task_events=False,
    broker_connection_timeout=5,
    broker_transport_options={
        # PGMQ otherwise prefers DATABASE_URL, the worker’s separate business login.
        "conn_string": broker._replace(scheme="postgresql").geturl(),
        "init_extension": False,
        "visibility_timeout": 330,
        "wait_time_seconds": 0,
        "pool_size": 2,
    },
)
