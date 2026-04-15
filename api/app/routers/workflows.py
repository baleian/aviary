import asyncio
import json
import uuid

from fastapi import APIRouter, Cookie, Depends, HTTPException, Query, WebSocket, WebSocketDisconnect, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_workflow_permission
from app.auth.oidc import validate_token
from app.auth.session_store import SESSION_COOKIE_NAME, get_fresh_session
from app.db.models import User
from app.db.session import get_db
from app.schemas.workflow import (
    WorkflowCreate,
    WorkflowListResponse,
    WorkflowResponse,
    WorkflowRunCreate,
    WorkflowRunListResponse,
    WorkflowRunResponse,
    WorkflowUpdate,
    WorkflowVersionResponse,
)
from app.services import redis_service, workflow_service
from app.services.workflow_engine import start_run, cancel_run

from aviary_shared.db.models import Workflow, WorkflowRun

router = APIRouter()


# --- CRUD ---


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
    workflow: Workflow = Depends(require_workflow_permission("view")),
    db: AsyncSession = Depends(get_db),
):
    return WorkflowResponse.from_orm_workflow(workflow)


@router.put("/{workflow_id}", response_model=WorkflowResponse)
async def update_workflow(
    body: WorkflowUpdate,
    workflow: Workflow = Depends(require_workflow_permission("edit_config")),
    db: AsyncSession = Depends(get_db),
):
    if workflow.status == "active":
        raise HTTPException(status_code=409, detail="Cannot edit active workflow. Use edit endpoint first.")
    workflow = await workflow_service.update_workflow(db, workflow, body)
    await db.refresh(workflow)
    return WorkflowResponse.from_orm_workflow(workflow)


@router.delete("/{workflow_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_workflow(
    workflow: Workflow = Depends(require_workflow_permission("delete")),
    db: AsyncSession = Depends(get_db),
):
    await workflow_service.delete_workflow(db, workflow)
    return None


# --- Deploy / Edit ---


@router.post("/{workflow_id}/deploy", response_model=WorkflowVersionResponse)
async def deploy_workflow(
    workflow: Workflow = Depends(require_workflow_permission("edit_config")),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    if workflow.status == "active":
        raise HTTPException(status_code=409, detail="Workflow is already deployed")
    version = await workflow_service.deploy_workflow(db, workflow, user)
    return WorkflowVersionResponse(
        id=str(version.id),
        version=version.version,
        deployed_by=str(version.deployed_by),
        deployed_at=version.deployed_at,
    )


@router.post("/{workflow_id}/edit", response_model=WorkflowResponse)
async def edit_workflow(
    workflow: Workflow = Depends(require_workflow_permission("edit_config")),
    db: AsyncSession = Depends(get_db),
):
    if workflow.status != "active":
        raise HTTPException(status_code=409, detail="Workflow is not active")
    workflow = await workflow_service.edit_workflow(db, workflow)
    return WorkflowResponse.from_orm_workflow(workflow)


@router.get("/{workflow_id}/versions")
async def list_versions(
    workflow: Workflow = Depends(require_workflow_permission("view")),
    db: AsyncSession = Depends(get_db),
):
    versions = await workflow_service.list_versions(db, workflow.id)
    return [
        WorkflowVersionResponse(
            id=str(v.id), version=v.version,
            deployed_by=str(v.deployed_by), deployed_at=v.deployed_at,
        )
        for v in versions
    ]


# --- Runs ---


@router.post("/{workflow_id}/runs", response_model=WorkflowRunResponse, status_code=status.HTTP_201_CREATED)
async def trigger_run(
    body: WorkflowRunCreate,
    workflow: Workflow = Depends(require_workflow_permission("execute")),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    run = WorkflowRun(
        workflow_id=workflow.id,
        triggered_by=user.id,
        trigger_type=body.trigger_type,
        trigger_data=body.trigger_data,
        definition_snapshot=workflow.definition,
    )
    db.add(run)
    await db.flush()

    run_id = str(run.id)
    # Use workflow.id as the synthetic agent_id for runtime session-manager
    # keying (each run gets its own workspace subtree). The workflow's chosen
    # pool dictates egress/image the same way individual agents do.
    worker_agent_id = str(workflow.id)
    pool_name = workflow.pool_name

    await db.commit()

    start_run(run_id, str(workflow.id), worker_agent_id, pool_name, body.trigger_data)

    return WorkflowRunResponse.from_orm_run(run)


@router.get("/{workflow_id}/runs", response_model=WorkflowRunListResponse)
async def list_runs(
    workflow: Workflow = Depends(require_workflow_permission("view")),
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
    workflow: Workflow = Depends(require_workflow_permission("view")),
    db: AsyncSession = Depends(get_db),
):
    run = await workflow_service.get_run(db, run_id, with_node_runs=True)
    if not run or run.workflow_id != workflow.id:
        raise HTTPException(status_code=404, detail="Run not found")
    return WorkflowRunResponse.from_orm_run(run, include_node_runs=True)


@router.post("/{workflow_id}/runs/{run_id}/cancel", status_code=status.HTTP_204_NO_CONTENT)
async def cancel_run_endpoint(
    run_id: uuid.UUID,
    workflow: Workflow = Depends(require_workflow_permission("execute")),
):
    cancelled = cancel_run(str(run_id))
    if not cancelled:
        raise HTTPException(status_code=409, detail="Run is not active")
    return None


# --- WebSocket for run status ---


@router.websocket("/{workflow_id}/runs/{run_id}/ws")
async def run_ws(
    websocket: WebSocket,
    workflow_id: str,
    run_id: str,
):
    cookie = websocket.cookies.get(SESSION_COOKIE_NAME)
    if not cookie:
        await websocket.close(code=4001, reason="Not authenticated")
        return

    session_data = await get_fresh_session(cookie)
    if not session_data:
        await websocket.close(code=4001, reason="Session expired")
        return

    try:
        await validate_token(session_data.access_token)
    except ValueError:
        await websocket.close(code=4001, reason="Invalid token")
        return

    await websocket.accept()

    client = redis_service.get_client()
    if not client:
        await websocket.send_json({"type": "error", "message": "Redis unavailable"})
        await websocket.close()
        return

    pubsub = client.pubsub()
    channel = f"workflow_run:{run_id}"
    await pubsub.subscribe(channel)

    try:
        while True:
            message = await pubsub.get_message(ignore_subscribe_messages=True, timeout=1.0)
            if message and message["type"] == "message":
                data = json.loads(message["data"])
                await websocket.send_json(data)

                if data.get("type") == "run_status" and data.get("status") in (
                    "completed", "failed", "cancelled",
                ):
                    break

            try:
                await asyncio.wait_for(websocket.receive_text(), timeout=0.01)
            except asyncio.TimeoutError:
                pass
            except WebSocketDisconnect:
                break

    finally:
        await pubsub.unsubscribe(channel)
        await pubsub.close()
