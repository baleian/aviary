"""Agent Supervisor — manages runtime containers via RuntimeBackend abstraction."""

import asyncio
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI

from aviary_shared.redis import RedisPublisher
from app.config import settings
from app.backends.docker import DockerBackend
from app.routers.agents import router as agents_router
from app.services.scaling import scaling_loop
from app.services.cleanup import idle_cleanup_loop

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger(__name__)


def create_backend():
    if settings.runtime_backend == "docker":
        return DockerBackend(
            socket=settings.docker_socket,
            network=settings.docker_network or None,
        )
    elif settings.runtime_backend == "fargate":
        from app.backends.fargate import FargateBackend
        raise NotImplementedError("FargateBackend not yet available")
    else:
        raise ValueError(f"Unknown runtime backend: {settings.runtime_backend}")


@asynccontextmanager
async def lifespan(app: FastAPI):
    backend = create_backend()
    redis_pub = RedisPublisher(settings.redis_url)

    app.state.backend = backend
    app.state.redis_publisher = redis_pub

    scaling_task = asyncio.create_task(scaling_loop(backend))
    cleanup_task = asyncio.create_task(idle_cleanup_loop(backend))

    logger.info("Agent Supervisor started (backend=%s)", settings.runtime_backend)

    yield

    scaling_task.cancel()
    cleanup_task.cancel()
    await redis_pub.close()
    logger.info("Agent Supervisor shutting down")


app = FastAPI(title="Aviary Agent Supervisor", lifespan=lifespan)
app.include_router(agents_router)
