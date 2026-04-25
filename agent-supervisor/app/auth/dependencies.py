"""Identity resolution for supervisor endpoints.

Three auth paths: user JWT (Bearer), worker (X-Aviary-Worker-Key +
on_behalf_of_sub), and no-IdP (Bearer optional, caller is dev_user_sub).
"""

from __future__ import annotations

import hmac
from dataclasses import dataclass

from fastapi import HTTPException, Request

from app.auth.oidc import TokenClaims, dev_user_sub, idp_enabled, validate_token
from app.config import settings


@dataclass
class IdentityContext:
    sub: str
    user_token: str | None
    via: str  # "user" | "worker" | "dev"


async def resolve_identity(request: Request, body: dict) -> IdentityContext:
    worker_key = request.headers.get("x-aviary-worker-key")
    if worker_key is not None:
        expected = settings.worker_shared_secret
        if not expected or not hmac.compare_digest(worker_key, expected):
            raise HTTPException(status_code=401, detail="Invalid worker key")
        sub = body.get("on_behalf_of_sub")
        if not sub:
            raise HTTPException(
                status_code=400, detail="on_behalf_of_sub required for worker auth"
            )
        return IdentityContext(sub=sub, user_token=None, via="worker")

    auth_header = request.headers.get("authorization", "")
    if not auth_header.lower().startswith("bearer "):
        if not idp_enabled():
            return IdentityContext(sub=dev_user_sub(), user_token=None, via="dev")
        raise HTTPException(
            status_code=401, detail="Missing or invalid Authorization header"
        )
    token = auth_header.split(None, 1)[1].strip()
    try:
        claims = await validate_token(token)
    except ValueError as exc:
        raise HTTPException(status_code=401, detail=str(exc)) from exc
    return IdentityContext(sub=claims.sub, user_token=token, via="user")


async def get_current_user(request: Request) -> TokenClaims:
    auth_header = request.headers.get("authorization", "")
    if not auth_header.lower().startswith("bearer "):
        if not idp_enabled():
            return await validate_token("")
        raise HTTPException(status_code=401, detail="Missing or invalid Authorization header")

    token = auth_header.split(None, 1)[1].strip()
    try:
        return await validate_token(token)
    except ValueError as exc:
        raise HTTPException(status_code=401, detail=str(exc)) from exc


def extract_bearer_token(request: Request) -> str:
    auth_header = request.headers.get("authorization", "")
    if not auth_header.lower().startswith("bearer "):
        raise HTTPException(status_code=401, detail="Missing or invalid Authorization header")
    return auth_header.split(None, 1)[1].strip()
