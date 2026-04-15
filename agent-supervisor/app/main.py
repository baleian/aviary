"""Agent Supervisor — streaming proxy + Redis publisher.

Role (v0.4 onward): forward a pool-keyed `/v1/stream` call to the target
runtime pool Service via SSE, publish each event to Redis, and return a final
status summary. No K8s CRUD, no per-agent lifecycle. Pool manifests are
managed by the infra team via GitOps; lifecycle is declarative.
"""

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app import redis_client
from app.routers import health, stream

logging.basicConfig(level=logging.INFO)


@asynccontextmanager
async def lifespan(app: FastAPI):
    await redis_client.init_redis()
    try:
        yield
    finally:
        await redis_client.close_redis()


app = FastAPI(title="Aviary Agent Supervisor", version="0.4.0", lifespan=lifespan)

app.include_router(stream.router, prefix="/v1", tags=["stream"])
app.include_router(health.router, tags=["health"])
