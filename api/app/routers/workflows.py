"""Workflow CRUD + deploy/edit/versions.

Run execution endpoints (trigger, cancel, list runs, WS) land in a separate
router once the Temporal worker wiring is in place.
"""

from fastapi import APIRouter, Depends, HTTPException, Query, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_workflow_owner
from app.db.models import User, Workflow
from app.db.session import get_db
from app.schemas.workflow import (
    WorkflowCreate,
    WorkflowListResponse,
    WorkflowResponse,
    WorkflowUpdate,
    WorkflowVersionResponse,
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
    items = []
    for w in workflows:
        cv = await workflow_service.current_version_number(db, w.id)
        items.append(WorkflowResponse.from_orm_workflow(w, current_version=cv))
    return WorkflowListResponse(items=items, total=total)


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
    workflow: Workflow = Depends(require_workflow_owner()),
    db: AsyncSession = Depends(get_db),
):
    cv = await workflow_service.current_version_number(db, workflow.id)
    return WorkflowResponse.from_orm_workflow(workflow, current_version=cv)


@router.put("/{workflow_id}", response_model=WorkflowResponse)
async def update_workflow(
    body: WorkflowUpdate,
    workflow: Workflow = Depends(require_workflow_owner()),
    db: AsyncSession = Depends(get_db),
):
    workflow = await workflow_service.update_workflow(db, workflow, body)
    await db.refresh(workflow)
    cv = await workflow_service.current_version_number(db, workflow.id)
    return WorkflowResponse.from_orm_workflow(workflow, current_version=cv)


@router.delete("/{workflow_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_workflow(
    workflow: Workflow = Depends(require_workflow_owner()),
    db: AsyncSession = Depends(get_db),
):
    await workflow_service.delete_workflow(db, workflow)
    return None


@router.post("/{workflow_id}/deploy", response_model=WorkflowVersionResponse)
async def deploy_workflow(
    workflow: Workflow = Depends(require_workflow_owner()),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    version = await workflow_service.deploy_workflow(db, workflow, user)
    await db.refresh(version)
    return WorkflowVersionResponse.from_orm_version(version)


@router.post("/{workflow_id}/edit", response_model=WorkflowResponse)
async def edit_workflow(
    workflow: Workflow = Depends(require_workflow_owner()),
    db: AsyncSession = Depends(get_db),
):
    workflow = await workflow_service.mark_workflow_draft(db, workflow)
    await db.refresh(workflow)
    cv = await workflow_service.current_version_number(db, workflow.id)
    return WorkflowResponse.from_orm_workflow(workflow, current_version=cv)


@router.get("/{workflow_id}/versions", response_model=list[WorkflowVersionResponse])
async def list_workflow_versions(
    workflow: Workflow = Depends(require_workflow_owner()),
    db: AsyncSession = Depends(get_db),
):
    versions = await workflow_service.list_workflow_versions(db, workflow.id)
    return [WorkflowVersionResponse.from_orm_version(v) for v in versions]
