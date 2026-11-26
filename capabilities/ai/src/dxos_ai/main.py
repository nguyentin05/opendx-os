# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from typing import Annotated

from fastapi import Depends, FastAPI, HTTPException
from sqlalchemy import text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session

from dxos_ai.config import settings
from dxos_ai.database import get_session


def create_app() -> FastAPI:
    settings()
    app = FastAPI(title="OpenDX-OS AI", docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/healthz")
    def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/readyz")
    def ready(session: Annotated[Session, Depends(get_session)]) -> dict[str, str]:
        try:
            revision = session.scalar(text("SELECT version_num FROM cap_ai.alembic_version"))
        except SQLAlchemyError:
            raise HTTPException(503, "AI database unavailable") from None
        if not revision:
            raise HTTPException(503, "AI database migration required")
        return {"status": "ready"}

    return app
