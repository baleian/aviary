import uuid

from sqlalchemy import func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Workflow, WorkflowACL, WorkflowRun, WorkflowNodeRun, TeamMember, User
from app.schemas.workflow import WorkflowCreate, WorkflowUpdate


async def create_workflow(db: AsyncSession, user: User, data: WorkflowCreate) -> Workflow:
    existing = await db.execute(select(Workflow).where(Workflow.slug == data.slug))
    if existing.scalar_one_or_none():
        raise ValueError(f"Workflow slug '{data.slug}' already exists")

    workflow = Workflow(
        name=data.name,
        slug=data.slug,
        description=data.description,
        owner_id=user.id,
        visibility=data.visibility,
    )
    db.add(workflow)
    await db.flush()
    return workflow


async def get_workflow(
    db: AsyncSession, workflow_id: uuid.UUID, include_deleted: bool = False
) -> Workflow | None:
    query = select(Workflow).where(Workflow.id == workflow_id)
    if not include_deleted:
        query = query.where(Workflow.status != "deleted")
    result = await db.execute(query)
    return result.scalar_one_or_none()


async def list_workflows_for_user(
    db: AsyncSession, user: User, offset: int = 0, limit: int = 50
) -> tuple[list[Workflow], int]:
    team_ids_result = await db.execute(
        select(TeamMember.team_id).where(TeamMember.user_id == user.id)
    )
    user_team_ids = [row[0] for row in team_ids_result.all()]

    conditions = [
        Workflow.owner_id == user.id,
        Workflow.visibility == "public",
    ]

    conditions.append(
        select(WorkflowACL.id).where(
            WorkflowACL.workflow_id == Workflow.id,
            WorkflowACL.user_id == user.id,
        ).exists()
    )

    if user_team_ids:
        conditions.append(
            select(WorkflowACL.id).where(
                WorkflowACL.workflow_id == Workflow.id,
                WorkflowACL.team_id.in_(user_team_ids),
            ).exists()
        )
        conditions.append(Workflow.visibility == "team")

    base_query = select(Workflow).where(
        Workflow.status != "deleted",
        or_(*conditions),
    )

    count_result = await db.execute(
        select(func.count()).select_from(base_query.subquery())
    )
    total = count_result.scalar() or 0

    result = await db.execute(
        base_query.order_by(Workflow.created_at.desc()).offset(offset).limit(limit)
    )
    return list(result.scalars().all()), total


async def update_workflow(db: AsyncSession, workflow: Workflow, data: WorkflowUpdate) -> Workflow:
    if data.name is not None:
        workflow.name = data.name
    if data.description is not None:
        workflow.description = data.description
    if data.definition is not None:
        workflow.definition = data.definition
    if data.visibility is not None:
        workflow.visibility = data.visibility
    if data.status is not None:
        workflow.status = data.status

    await db.flush()
    return workflow


async def delete_workflow(db: AsyncSession, workflow: Workflow) -> None:
    await db.delete(workflow)
    await db.flush()


async def get_run(
    db: AsyncSession, run_id: uuid.UUID, with_node_runs: bool = False
) -> WorkflowRun | None:
    query = select(WorkflowRun).where(WorkflowRun.id == run_id)
    if with_node_runs:
        query = query.options(selectinload(WorkflowRun.node_runs))
    result = await db.execute(query)
    return result.scalar_one_or_none()


async def list_runs(
    db: AsyncSession, workflow_id: uuid.UUID, offset: int = 0, limit: int = 50
) -> tuple[list[WorkflowRun], int]:
    base_query = select(WorkflowRun).where(WorkflowRun.workflow_id == workflow_id)

    count_result = await db.execute(
        select(func.count()).select_from(base_query.subquery())
    )
    total = count_result.scalar() or 0

    result = await db.execute(
        base_query.order_by(WorkflowRun.created_at.desc()).offset(offset).limit(limit)
    )
    return list(result.scalars().all()), total
