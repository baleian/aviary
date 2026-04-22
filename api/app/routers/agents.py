import uuid

from fastapi import APIRouter, Depends, HTTPException, Query, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_agent_owner
from app.db.models import Agent, User
from app.db.session import get_db
from app.errors import ConflictError
from app.schemas.agent import AgentCreate, AgentListResponse, AgentResponse, AgentUpdate
from app.schemas.catalog import (
    AgentVersionResponse,
    DriftResponse,
    PublishRequest,
)
from app.services import agent_service, publish_service

router = APIRouter()


@router.get("", response_model=AgentListResponse)
async def list_agents(
    offset: int = Query(0, ge=0),
    limit: int = Query(50, ge=1, le=100),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agents, total = await agent_service.list_agents_for_user(db, user, offset, limit)
    return AgentListResponse(
        items=[AgentResponse.model_validate(a) for a in agents],
        total=total,
    )


@router.post("", response_model=AgentResponse, status_code=status.HTTP_201_CREATED)
async def create_agent(
    body: AgentCreate,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await agent_service.create_agent(db, user, body)
    return AgentResponse.model_validate(agent)


@router.get("/{agent_id}", response_model=AgentResponse)
async def get_agent(agent: Agent = Depends(require_agent_owner())):
    return AgentResponse.model_validate(agent)


@router.put("/{agent_id}", response_model=AgentResponse)
async def update_agent(
    body: AgentUpdate,
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    if agent.catalog_import_id is not None:
        raise ConflictError(
            "Imported catalog agents are read-only. Fork the agent first to edit it."
        )
    agent = await agent_service.update_agent(db, agent, body)
    return AgentResponse.model_validate(agent)


@router.delete("/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_agent(
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    await agent_service.delete_agent(db, agent)
    return None


@router.post("/{agent_id}/publish", response_model=AgentVersionResponse)
async def publish_agent(
    body: PublishRequest,
    agent: Agent = Depends(require_agent_owner()),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    version = await publish_service.publish_version(
        db, agent, user,
        category=body.category.value,
        release_notes=body.release_notes,
    )
    return AgentVersionResponse.model_validate(version)


@router.get("/{agent_id}/drift", response_model=DriftResponse)
async def get_drift(
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    data = await publish_service.compute_drift(db, agent)
    return DriftResponse(**data)
