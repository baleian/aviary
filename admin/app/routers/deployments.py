"""Deployment management — activate, deactivate, scale, restart, status."""

import uuid
import logging

from datetime import datetime, timezone

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import Agent
from app.db import get_db
from app.services import controller_client

logger = logging.getLogger(__name__)

router = APIRouter()


@router.post("/{agent_id}/activate")
async def activate_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Create namespace (if needed) + ensure deployment with full policy from DB."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    # Create namespace if not exists
    if not agent.namespace:
        try:
            ns_name = await controller_client.create_namespace(
                agent_id=str(agent.id),
                owner_id=str(agent.owner_id),
                instruction=agent.instruction,
                tools=agent.tools,
                policy=agent.policy or {},
                mcp_servers=agent.mcp_servers or [],
            )
            agent.namespace = ns_name
        except Exception as e:
            raise HTTPException(status_code=500, detail=f"Namespace creation failed: {e}") from e

    # Ensure deployment
    try:
        await controller_client.ensure_deployment(
            namespace=agent.namespace,
            agent_id=str(agent.id),
            owner_id=str(agent.owner_id),
            instruction=agent.instruction,
            tools=agent.tools,
            policy=agent.policy or {},
            mcp_servers=agent.mcp_servers or [],
            min_pods=agent.min_pods,
            max_pods=agent.max_pods,
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Deployment failed: {e}") from e

    agent.deployment_active = True
    agent.last_activity_at = datetime.now(timezone.utc)
    await db.flush()

    return {"status": "activated", "deployment_active": True}


@router.post("/{agent_id}/deactivate")
async def deactivate_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Scale deployment to zero."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    if agent.namespace:
        try:
            await controller_client.scale_to_zero(agent.namespace)
        except Exception:
            logger.warning("Failed to scale down agent %s", agent.id, exc_info=True)

    agent.deployment_active = False
    await db.flush()

    return {"status": "deactivated", "deployment_active": False}


@router.get("/{agent_id}/deployment")
async def get_deployment_status(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Get deployment status."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    if agent.namespace:
        status_info = await controller_client.get_deployment_status(agent.namespace)
    else:
        status_info = {"replicas": 0, "ready_replicas": 0, "updated_replicas": 0}

    return {
        "deployment_active": agent.deployment_active,
        "pod_strategy": agent.pod_strategy,
        "min_pods": agent.min_pods,
        "max_pods": agent.max_pods,
        **status_info,
    }


@router.post("/{agent_id}/deploy")
async def deploy_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """Trigger rolling restart to apply config changes."""
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")

    if agent.deployment_active and agent.namespace:
        try:
            await controller_client.rolling_restart(agent.namespace)
        except Exception:
            logger.warning("Rolling restart failed for agent %s", agent.id, exc_info=True)

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

    # Update scaling bounds if provided
    if body.min_pods is not None:
        agent.min_pods = body.min_pods
    if body.max_pods is not None:
        agent.max_pods = body.max_pods
    await db.flush()

    if agent.namespace:
        await controller_client.scale_deployment(
            agent.namespace, body.replicas, agent.min_pods, agent.max_pods,
        )

    return {"status": "scaled", "replicas": body.replicas}
