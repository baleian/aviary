import uuid

from fastapi import APIRouter, Depends, HTTPException, Query, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_workflow_permission
from app.db.models import User
from app.db.session import get_db
from app.schemas.workflow import (
    WorkflowCreate,
    WorkflowListResponse,
    WorkflowResponse,
    WorkflowRunListResponse,
    WorkflowRunResponse,
    WorkflowUpdate,
)
from app.services import workflow_service

router = APIRouter()


@router.get("", response_model=WorkflowListResponse)
async def list_workflows(
    offset: int = Query(0, ge=0),
    limit: int = Query(50, ge=1, le=100),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    workflows, total = await workflow_service.list_workflows_for_user(db, user, offset, limit)
    return WorkflowListResponse(
        items=[WorkflowResponse.from_orm_workflow(w) for w in workflows],
        total=total,
    )


@router.post("", response_model=WorkflowResponse, status_code=status.HTTP_201_CREATED)
async def create_workflow(
    body: WorkflowCreate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    try:
        workflow = await workflow_service.create_workflow(db, user, body)
    except ValueError as e:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=str(e)) from e
    return WorkflowResponse.from_orm_workflow(workflow)


@router.get("/{workflow_id}", response_model=WorkflowResponse)
async def get_workflow(
    workflow=Depends(require_workflow_permission("view")),
):
    return WorkflowResponse.from_orm_workflow(workflow)


@router.put("/{workflow_id}", response_model=WorkflowResponse)
async def update_workflow(
    body: WorkflowUpdate,
    workflow=Depends(require_workflow_permission("edit_config")),
    db: AsyncSession = Depends(get_db),
):
    workflow = await workflow_service.update_workflow(db, workflow, body)
    await db.refresh(workflow)
    return WorkflowResponse.from_orm_workflow(workflow)


@router.delete("/{workflow_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_workflow(
    workflow=Depends(require_workflow_permission("delete")),
    db: AsyncSession = Depends(get_db),
):
    await workflow_service.delete_workflow(db, workflow)
    return None


# --- Runs ---


@router.get("/{workflow_id}/runs", response_model=WorkflowRunListResponse)
async def list_runs(
    workflow=Depends(require_workflow_permission("view")),
    offset: int = Query(0, ge=0),
    limit: int = Query(50, ge=1, le=100),
    db: AsyncSession = Depends(get_db),
):
    runs, total = await workflow_service.list_runs(db, workflow.id, offset, limit)
    return WorkflowRunListResponse(
        items=[WorkflowRunResponse.from_orm_run(r) for r in runs],
        total=total,
    )


@router.get("/{workflow_id}/runs/{run_id}", response_model=WorkflowRunResponse)
async def get_run(
    run_id: uuid.UUID,
    workflow=Depends(require_workflow_permission("view")),
    db: AsyncSession = Depends(get_db),
):
    run = await workflow_service.get_run(db, run_id, with_node_runs=True)
    if not run or run.workflow_id != workflow.id:
        raise HTTPException(status_code=404, detail="Run not found")
    return WorkflowRunResponse.from_orm_run(run, include_node_runs=True)
