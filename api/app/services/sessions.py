"""Session + message persistence."""

import uuid
from datetime import datetime

from sqlalchemy import desc, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import Message, Session, User


async def list_for_agent(db: AsyncSession, agent_id: uuid.UUID, user: User) -> list[Session]:
    result = await db.execute(
        select(Session)
        .where(
            Session.agent_id == agent_id,
            Session.status == "active",
            Session.created_by == str(user.id),
        )
        .order_by(desc(func.coalesce(Session.last_message_at, Session.created_at)))
    )
    return list(result.scalars().all())


async def get(db: AsyncSession, session_id: uuid.UUID) -> Session | None:
    result = await db.execute(select(Session).where(Session.id == session_id))
    return result.scalar_one_or_none()


async def require_owner(db: AsyncSession, session_id: uuid.UUID, user: User) -> Session:
    from fastapi import HTTPException

    session = await get(db, session_id)
    if not session or session.status == "deleted":
        raise HTTPException(404, "Session not found")
    if session.created_by != str(user.id):
        raise HTTPException(403, "Not the session owner")
    return session


async def create(db: AsyncSession, agent_id: uuid.UUID, user: User, title: str | None) -> Session:
    session = Session(agent_id=agent_id, created_by=str(user.id), title=title)
    db.add(session)
    await db.flush()
    return session


async def delete(db: AsyncSession, session: Session) -> None:
    from app.services.supervisor import supervisor_client

    session.status = "deleted"
    await db.flush()
    await supervisor_client.cleanup_session(str(session.agent_id), str(session.id))


async def update_title(db: AsyncSession, session: Session, title: str) -> Session:
    session.title = title
    await db.flush()
    return session


async def list_messages(
    db: AsyncSession,
    session_id: uuid.UUID,
    *,
    before: datetime | None = None,
    limit: int = 50,
) -> tuple[list[Message], bool]:
    stmt = select(Message).where(Message.session_id == session_id)
    if before is not None:
        stmt = stmt.where(Message.created_at < before)
    stmt = stmt.order_by(desc(Message.created_at)).limit(limit + 1)
    result = await db.execute(stmt)
    rows = list(result.scalars().all())
    has_more = len(rows) > limit
    return list(reversed(rows[:limit])), has_more


async def save_message(
    db: AsyncSession,
    session_id: uuid.UUID,
    sender_type: str,
    content: str | None,
    *,
    sender_id: str | None = None,
    metadata: dict | None = None,
) -> Message:
    msg = Message(
        session_id=session_id,
        sender_type=sender_type,
        sender_id=sender_id,
        content=content,
        metadata_=metadata,
    )
    db.add(msg)
    await db.flush()

    session = (await db.execute(select(Session).where(Session.id == session_id))).scalar_one()
    session.last_message_at = msg.created_at
    await db.flush()
    return msg


async def delete_message(db: AsyncSession, message_id: uuid.UUID) -> None:
    msg = (await db.execute(select(Message).where(Message.id == message_id))).scalar_one_or_none()
    if msg is not None:
        await db.delete(msg)
        await db.flush()
