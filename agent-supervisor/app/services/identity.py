"""Server-authoritative identity enrichment — overwrites caller-supplied
identity/credential fields on agent_config so the runtime can't be lied to."""

from __future__ import annotations

import logging

from fastapi import HTTPException

from app.auth.dependencies import IdentityContext
from app.services.vault_client import fetch_user_credentials


logger = logging.getLogger(__name__)


async def enrich_agent_config(body: dict, identity: IdentityContext) -> None:
    agent_config = body.get("agent_config") or {}
    if not agent_config.get("agent_id"):
        raise HTTPException(status_code=400, detail="agent_config.agent_id is required")

    agent_config["user_external_id"] = identity.sub
    if identity.user_token:
        agent_config["user_token"] = identity.user_token
    else:
        agent_config.pop("user_token", None)

    credentials = await fetch_user_credentials(identity.sub)
    if credentials:
        agent_config["credentials"] = credentials
    else:
        agent_config.pop("credentials", None)

    # Caller can't redirect the runtime by smuggling api_base/api_key in the body.
    mc = agent_config.get("model_config")
    if isinstance(mc, dict):
        mc.pop("api_base", None)
        mc.pop("api_key", None)

    body["agent_config"] = agent_config
    body.pop("on_behalf_of_sub", None)

    logger.info(
        "agent_config enriched sub=%s agent_id=%s on_behalf_token=%s credentials=%s",
        identity.sub,
        agent_config.get("agent_id"),
        bool(identity.user_token),
        sorted(credentials.keys()) if credentials else [],
    )
