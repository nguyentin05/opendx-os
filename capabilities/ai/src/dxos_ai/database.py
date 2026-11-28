# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from collections.abc import Iterator
from functools import lru_cache

from sqlalchemy import create_engine
from sqlalchemy.engine import URL, Engine, make_url
from sqlalchemy.orm import Session

from dxos_ai.config import settings


def postgres_url(value: str) -> URL:
    return make_url(value).set(drivername="postgresql+psycopg")


@lru_cache
def engine() -> Engine:
    return create_engine(
        postgres_url(settings().database_url.get_secret_value()),
        pool_pre_ping=True,
        hide_parameters=True,
        connect_args={"connect_timeout": 5},
    )


def get_session() -> Iterator[Session]:
    with Session(engine()) as session:
        yield session
