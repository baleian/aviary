"""Shared DB session factory."""

from collections.abc import AsyncGenerator

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine


def create_session_factory(
    database_url: str,
    pool_size: int = 20,
    max_overflow: int = 10,
    echo: bool = False,
) -> tuple:
    engine = create_async_engine(
        database_url,
        echo=echo,
        pool_size=pool_size,
        max_overflow=max_overflow,
    )
    factory = async_sessionmaker(engine, expire_on_commit=False)
    return engine, factory


async def get_db_dependency(factory: async_sessionmaker) -> AsyncGenerator[AsyncSession, None]:
    async with factory() as session:
        try:
            yield session
            await session.commit()
        except Exception:
            await session.rollback()
            raise
