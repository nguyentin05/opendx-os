# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import os
from contextlib import asynccontextmanager
from threading import Event, Thread
from typing import Annotated

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from sqlalchemy import text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session

from dxos_ai.agent.conversations import router as agent_router
from dxos_ai.config import settings
from dxos_ai.database import get_session
from dxos_ai.dispatch import poll_dispatch
from dxos_ai.knowledge.documents import router as document_router
from dxos_ai.knowledge.retrieval import router as retrieval_router
from dxos_ai.knowledge.sources import router as internal_router
from dxos_ai.models.gateway import router as model_router


@asynccontextmanager
async def lifespan(app: FastAPI):
    if not os.environ.get("AI_BROKER_URL"):
        yield
        return
    stop = Event()
    dispatcher = Thread(target=poll_dispatch, args=(stop,), daemon=True)
    dispatcher.start()
    try:
        yield
    finally:
        stop.set()
        dispatcher.join(timeout=5)


def create_app() -> FastAPI:
    settings()
    app = FastAPI(
        title="OpenDX-OS AI", docs_url=None, redoc_url=None, openapi_url=None, lifespan=lifespan,
    )

    @app.exception_handler(RequestValidationError)
    async def validation_error(request: Request, error: RequestValidationError) -> JSONResponse:
        return JSONResponse({"error": {"code": "INVALID_REQUEST"}}, status_code=422)

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

    app.include_router(model_router)
    app.include_router(document_router)
    app.include_router(internal_router)
    app.include_router(retrieval_router)
    app.include_router(agent_router)
    return app
