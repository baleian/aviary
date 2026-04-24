"""Workflow list, detail, update, and delete pages."""

import math
import uuid
import logging

from fastapi import APIRouter, Depends, Form, Request
from fastapi.responses import HTMLResponse, RedirectResponse
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import User, Workflow
from app.db import get_db
from app.routers.pages._templates import templates

logger = logging.getLogger(__name__)

router = APIRouter()


@router.get("/workflows", response_class=HTMLResponse)
async def workflow_list(request: Request, page: int = 1, db: AsyncSession = Depends(get_db)):
    per_page = 50
    offset = (page - 1) * per_page

    total_result = await db.execute(select(func.count()).select_from(Workflow))
    total = total_result.scalar() or 0
    total_pages = max(1, math.ceil(total / per_page))

    result = await db.execute(
        select(Workflow, User.email)
        .join(User, User.id == Workflow.owner_id)
        .order_by(Workflow.created_at.desc())
        .offset(offset)
        .limit(per_page)
    )
    rows = list(result.all())
    workflows = [row[0] for row in rows]
    owner_email_map = {str(row[0].id): row[1] for row in rows}

    return templates.TemplateResponse(request, "workflows.html", {
        "workflows": workflows,
        "owner_emails": owner_email_map,
        "total": total,
        "page": page,
        "total_pages": total_pages,
    })


@router.get("/workflows/{workflow_id}", response_class=HTMLResponse)
async def workflow_detail(
    request: Request, workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(Workflow, User.email)
        .join(User, User.id == Workflow.owner_id)
        .where(Workflow.id == workflow_id)
    )
    row = result.one_or_none()
    if not row:
        return RedirectResponse("/workflows?error=Workflow+not+found", status_code=303)
    workflow, owner_email = row

    flash = request.query_params.get("flash")
    flash_data = None
    if flash:
        flash_data = {"type": "success", "message": flash}
    error = request.query_params.get("error")
    if error:
        flash_data = {"type": "error", "message": error}

    return templates.TemplateResponse(request, "workflow_detail.html", {
        "workflow": workflow,
        "owner_email": owner_email,
        "flash": flash_data,
    })


@router.post("/workflows/{workflow_id}/update")
async def update_workflow_config(
    workflow_id: uuid.UUID,
    db: AsyncSession = Depends(get_db),
    name: str = Form(...),
    description: str = Form(""),
    runtime_endpoint: str = Form(""),
):
    result = await db.execute(select(Workflow).where(Workflow.id == workflow_id))
    workflow = result.scalar_one_or_none()
    if not workflow:
        return RedirectResponse(
            f"/workflows/{workflow_id}?error=Workflow+not+found", status_code=303,
        )

    workflow.name = name
    workflow.description = description or None
    workflow.runtime_endpoint = runtime_endpoint.strip() or None
    await db.flush()

    return RedirectResponse(
        f"/workflows/{workflow_id}?flash=Configuration+saved", status_code=303,
    )


@router.post("/workflows/{workflow_id}/delete")
async def delete_workflow(workflow_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Workflow).where(Workflow.id == workflow_id))
    workflow = result.scalar_one_or_none()
    if not workflow:
        return RedirectResponse("/workflows?error=Not+found", status_code=303)

    await db.delete(workflow)
    await db.flush()
    return RedirectResponse("/workflows?flash=Workflow+deleted", status_code=303)
