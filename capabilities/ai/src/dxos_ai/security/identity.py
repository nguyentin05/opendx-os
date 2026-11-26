# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import re
import time
from dataclasses import dataclass
from functools import lru_cache
from typing import Annotated
from uuid import UUID

import jwt
from fastapi import Depends, HTTPException, Request
from sqlalchemy import text
from sqlalchemy.orm import Session

from dxos_ai.config import settings
from dxos_ai.database import get_session

SessionDep = Annotated[Session, Depends(get_session)]
ROLE_NAME = re.compile(r"role_[a-z0-9_]{1,122}")


@dataclass(frozen=True)
class Actor:
    user_id: UUID
    role: str
    expires_at: float


@lru_cache(maxsize=1)
def jwks(url: str) -> jwt.PyJWKClient:
    return jwt.PyJWKClient(url, lifespan=300, timeout=3)


def subject(request: Request) -> tuple[UUID, int]:
    auth = request.headers.getlist("authorization")
    parts = auth[0].split() if len(auth) == 1 else []
    if len(parts) != 2 or parts[0].lower() != "bearer" or len(parts[1]) > 16384:
        raise HTTPException(401, "Invalid bearer token")
    cfg = settings()
    if not cfg.oidc_jwks_url or not cfg.oidc_issuer_url:
        raise HTTPException(503, "Identity service unavailable")
    try:
        signed = jwks(str(cfg.oidc_jwks_url)).get_signing_key_from_jwt(parts[1])
        claims = jwt.decode(
            parts[1],
            signed.key,
            algorithms=["RS256"],
            issuer=cfg.oidc_issuer_url,
            audience=cfg.oidc_audience,
            options={"require": ["sub", "iss", "aud", "exp"]},
        )
        return UUID(claims["sub"]), claims["exp"]
    except jwt.PyJWKClientConnectionError:
        raise HTTPException(503, "Identity service unavailable") from None
    except (jwt.PyJWTError, TypeError, ValueError):
        raise HTTPException(401, "Invalid bearer token") from None


def holds_role(session: Session, actor: Actor, *, lock: bool = False) -> bool:
    if actor.expires_at <= time.time():
        return False
    return bool(session.scalar(
        text("SELECT cap_ai.holds_role(:actor, :role, :lock)"),
        {"actor": actor.user_id, "role": actor.role, "lock": lock},
    ))


def current_actor(request: Request, session: SessionDep) -> Actor:
    user_id, expires = subject(request)
    roles = request.headers.getlist("x-hasura-role")
    if len(roles) != 1 or not ROLE_NAME.fullmatch(roles[0]):
        raise HTTPException(403, "Current role required")
    actor = Actor(user_id, roles[0], expires)
    if not holds_role(session, actor):
        raise HTTPException(403, "Current role required")
    return actor


ActorDep = Annotated[Actor, Depends(current_actor)]


def require_admin(actor: Actor, session: Session, *, lock: bool = False) -> None:
    if actor.role != "role_operator" or not holds_role(session, actor, lock=lock):
        raise HTTPException(403, "Platform Admin required")
