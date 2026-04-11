"""Deployment management — activate, deactivate, scale, restart, status."""

import uuid
import logging

import httpx
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import Agent
from aviary_shared.naming import agent_namespace
from app.db import get_db
from app.services import agent_lifecycle, supervisor_client

logger = logging.getLogger(__name__)

router = APIRouter()


@router.post("/{agent_id}/activate")
async def activate_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Create namespace (if needed) + ensure deployment with full policy from DB."""
    agent = await agent_lifecycle.find_agent_or_none(db, agent_id)
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    try:
        await agent_lifecycle.activate(agent)
    except httpx.HTTPError as e:
        raise HTTPException(status_code=502, detail=f"Activation failed: {e}") from e
    return {"status": "activated"}


@router.post("/{agent_id}/deactivate")
async def deactivate_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Scale deployment to zero."""
    agent = await agent_lifecycle.find_agent_or_none(db, agent_id)
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    try:
        await agent_lifecycle.deactivate(agent)
    except httpx.HTTPError as e:
        raise HTTPException(status_code=502, detail=f"Failed to scale down: {e}") from e
    return {"status": "deactivated"}


@router.get("/{agent_id}/deployment")
async def get_deployment_status(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Get deployment status."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    ns = agent_namespace(str(agent.id))
    try:
        status_info = await supervisor_client.get_deployment_status(ns)
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            # Deployment genuinely does not exist (never activated or already torn down).
            status_info = {"exists": False, "replicas": 0, "ready_replicas": 0, "updated_replicas": 0}
        else:
            raise HTTPException(status_code=502, detail=f"Status fetch failed: {e}") from e
    else:
        status_info = {"exists": True, **status_info}

    return {
        "pod_strategy": agent.pod_strategy,
        "min_pods": agent.min_pods,
        "max_pods": agent.max_pods,
        **status_info,
    }


@router.post("/{agent_id}/deploy")
async def deploy_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Trigger rolling restart to apply config changes."""
    agent = await agent_lifecycle.find_agent_or_none(db, agent_id)
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    try:
        await agent_lifecycle.rolling_restart(agent)
    except httpx.HTTPError as e:
        raise HTTPException(status_code=502, detail=f"Rolling restart failed: {e}") from e
    return {"status": "deploying"}


class ScaleRequest(BaseModel):
    replicas: int
    min_pods: int | None = None
    max_pods: int | None = None


@router.patch("/{agent_id}/scale")
async def scale_agent(
    agent_id: uuid.UUID, body: ScaleRequest, db: AsyncSession = Depends(get_db),
):
    """Manual scaling."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    if body.min_pods is not None:
        agent.min_pods = body.min_pods
    if body.max_pods is not None:
        agent.max_pods = body.max_pods
    await db.flush()

    ns = agent_namespace(str(agent.id))
    try:
        status = await supervisor_client.get_deployment_status(ns)
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            # Deployment not provisioned yet — DB scaling fields are saved, no K8s sync needed.
            return {"status": "scaled", "replicas": body.replicas, "applied": False}
        raise HTTPException(status_code=502, detail=f"Status fetch failed: {e}") from e

    if status.get("replicas", 0) > 0 or status.get("ready_replicas", 0) > 0:
        try:
            await supervisor_client.scale_deployment(ns, body.replicas, agent.min_pods, agent.max_pods)
        except httpx.HTTPError as e:
            raise HTTPException(status_code=502, detail=f"Scale failed: {e}") from e

    return {"status": "scaled", "replicas": body.replicas, "applied": True}
