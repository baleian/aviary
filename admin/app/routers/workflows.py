"""Workflow management REST API for admin console."""

import uuid

import httpx
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Workflow, Policy
from aviary_shared.naming import agent_namespace
from app.db import get_db
from app.services import supervisor_client

router = APIRouter()


async def _get_workflow(db: AsyncSession, workflow_id: uuid.UUID) -> Workflow:
    result = await db.execute(
        select(Workflow).where(Workflow.id == workflow_id).options(selectinload(Workflow.policy))
    )
    wf = result.scalar_one_or_none()
    if not wf:
        raise HTTPException(status_code=404, detail="Workflow not found")
    return wf


@router.get("/{workflow_id}/deployment")
async def get_deployment_status(workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    wf = await _get_workflow(db, workflow_id)
    ns = agent_namespace(str(wf.id))
    try:
        info = await supervisor_client.get_deployment_status(ns)
        return {"exists": True, **info, "ready": info.get("ready_replicas", 0)}
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            return {"exists": False, "replicas": 0, "ready_replicas": 0, "ready": 0}
        raise HTTPException(status_code=502, detail=str(e)) from e


@router.post("/{workflow_id}/activate")
async def activate_workflow(workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    wf = await _get_workflow(db, workflow_id)
    policy = wf.policy
    policy_rules = policy.policy_rules if policy else {}
    min_pods = policy.min_pods if policy else 1
    max_pods = policy.max_pods if policy else 3

    ns = agent_namespace(str(wf.id))
    try:
        await supervisor_client.create_namespace(
            agent_id=str(wf.id), owner_id=str(wf.owner_id), policy=policy_rules,
        )
    except httpx.HTTPStatusError as e:
        if e.response.status_code != 409:
            raise HTTPException(status_code=502, detail=str(e)) from e

    await supervisor_client.ensure_deployment(
        namespace=ns, agent_id=str(wf.id), owner_id=str(wf.owner_id),
        policy=policy_rules, min_pods=min_pods, max_pods=max_pods,
    )
    return {"status": "activated"}


@router.post("/{workflow_id}/deactivate")
async def deactivate_workflow(workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    wf = await _get_workflow(db, workflow_id)
    ns = agent_namespace(str(wf.id))
    await supervisor_client.scale_to_zero(ns)
    return {"status": "deactivated"}


@router.post("/{workflow_id}/restart")
async def restart_workflow(workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    wf = await _get_workflow(db, workflow_id)
    ns = agent_namespace(str(wf.id))
    await supervisor_client.rolling_restart(ns)
    return {"status": "restarted"}
