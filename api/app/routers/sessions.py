import uuid
from datetime import datetime

from fastapi import APIRouter, Depends, Query, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user
from app.deps import get_db
from app.schemas.message import MessagePageResponse, MessageResponse
from app.schemas.session import (
    SessionCreate,
    SessionListResponse,
    SessionResponse,
    SessionTitleUpdate,
)
from app.services import agents as agent_svc
from app.services import session_status
from app.services import sessions as svc
from aviary_shared.db.models import User

router = APIRouter()


@router.get("/agents/{agent_id}/sessions", response_model=SessionListResponse)
async def list_sessions(
    agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    await agent_svc.require_owner_viewable(db, agent_id, user)
    items = await svc.list_for_agent(db, agent_id, user)
    return SessionListResponse(items=[SessionResponse.model_validate(s) for s in items])


@router.post(
    "/agents/{agent_id}/sessions",
    response_model=SessionResponse,
    status_code=status.HTTP_201_CREATED,
)
async def create_session(
    agent_id: uuid.UUID,
    body: SessionCreate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await agent_svc.require_owner_active(db, agent_id, user)
    session = await svc.create(db, agent.id, user, body.title)
    return SessionResponse.model_validate(session)


@router.get("/sessions/status")
async def get_sessions_status(
    ids: str = Query(..., description="Comma-separated session IDs"),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    """Batch sidebar status — statuses + per-user unread counts + latest titles."""
    session_ids: list[str] = []
    uuids: list[uuid.UUID] = []
    for raw in ids.split(","):
        sid = raw.strip()
        if not sid:
            continue
        try:
            uuids.append(uuid.UUID(sid))
        except ValueError:
            continue
        session_ids.append(sid)
    if not session_ids:
        return {"statuses": {}, "unread": {}, "titles": {}}

    statuses = await session_status.get_bulk_status(session_ids)
    unread = await session_status.get_bulk_unread(session_ids, str(user.id))
    titles = await svc.get_titles_bulk(db, uuids)
    return {"statuses": statuses, "unread": unread, "titles": titles}


@router.get("/sessions/{session_id}", response_model=SessionResponse)
async def get_session(
    session_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    session = await svc.require_owner(db, session_id, user)
    return SessionResponse.model_validate(session)


@router.get("/sessions/{session_id}/messages", response_model=MessagePageResponse)
async def list_messages(
    session_id: uuid.UUID,
    before: datetime | None = Query(None),
    limit: int = Query(50, ge=1, le=200),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    await svc.require_owner(db, session_id, user)
    msgs, has_more = await svc.list_messages(db, session_id, before=before, limit=limit)
    return MessagePageResponse(
        messages=[MessageResponse.model_validate(m) for m in msgs],
        has_more=has_more,
    )


@router.patch("/sessions/{session_id}/title", response_model=SessionResponse)
async def update_title(
    session_id: uuid.UUID,
    body: SessionTitleUpdate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    session = await svc.require_owner(db, session_id, user)
    session = await svc.update_title(db, session, body.title)
    return SessionResponse.model_validate(session)


@router.delete("/sessions/{session_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_session(
    session_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    session = await svc.require_owner(db, session_id, user)
    await svc.delete(db, session)
