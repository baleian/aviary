"""Workflow management REST API for admin console."""

import uuid

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import Workflow
from app.db import get_db

router = APIRouter()


class WorkflowPoolUpdate(BaseModel):
    pool_name: str


async def _get_workflow(db: AsyncSession, workflow_id: uuid.UUID) -> Workflow:
    result = await db.execute(select(Workflow).where(Workflow.id == workflow_id))
    wf = result.scalar_one_or_none()
    if not wf:
        raise HTTPException(status_code=404, detail="Workflow not found")
    return wf


@router.patch("/{workflow_id}/pool")
async def set_workflow_pool(
    workflow_id: uuid.UUID,
    body: WorkflowPoolUpdate,
    db: AsyncSession = Depends(get_db),
):
    wf = await _get_workflow(db, workflow_id)
    wf.pool_name = body.pool_name
    await db.flush()
    return {"workflow_id": str(wf.id), "pool_name": wf.pool_name}
