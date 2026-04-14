import asyncio
import uuid

from fastapi import APIRouter, Depends, Query, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user
from app.deps import get_db, get_redis
from app.schemas.agent import AgentCreate, AgentListResponse, AgentResponse, AgentUpdate
from app.services import agents as svc
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import User

router = APIRouter()

_STATUS_CACHE_TTL = 10


@router.get("", response_model=AgentListResponse, response_model_by_alias=True)
async def list_agents(
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    items = await svc.list_for_owner(db, user)
    return AgentListResponse(items=[AgentResponse.model_validate(a) for a in items])


@router.post("", response_model=AgentResponse, response_model_by_alias=True, status_code=status.HTTP_201_CREATED)
async def create_agent(
    body: AgentCreate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await svc.create(db, user, body)
    return AgentResponse.model_validate(agent)


@router.get("/status")
async def get_agents_status(
    ids: str = Query(..., description="Comma-separated agent IDs"),
    _user: User = Depends(get_current_user),
):
    """Batch readiness probe for the sidebar (Redis-cached)."""
    agent_ids = [s.strip() for s in ids.split(",") if s.strip()]
    if not agent_ids:
        return {"statuses": {}}

    redis = get_redis()

    async def check_one(aid: str) -> tuple[str, str]:
        key = f"agent_readiness:{aid}"
        cached = await redis.get(key)
        if cached is not None:
            return aid, cached
        ready = await supervisor_client.is_ready(aid)
        result = "ready" if ready else "offline"
        await redis.set(key, result, ex=_STATUS_CACHE_TTL)
        return aid, result

    results = await asyncio.gather(*(check_one(aid) for aid in agent_ids))
    return {"statuses": dict(results)}


@router.get("/{agent_id}", response_model=AgentResponse)
async def get_agent(
    agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await svc.require_owner_viewable(db, agent_id, user)
    return AgentResponse.model_validate(agent)


@router.patch("/{agent_id}", response_model=AgentResponse)
async def update_agent(
    agent_id: uuid.UUID,
    body: AgentUpdate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await svc.require_owner_active(db, agent_id, user)
    agent = await svc.update(db, agent, body)
    return AgentResponse.model_validate(agent)


@router.delete("/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_agent(
    agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await svc.require_owner_active(db, agent_id, user)
    await svc.soft_delete(db, agent)
