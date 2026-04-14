"""Aviary Admin Console.

Local-only operator UI (no auth). Not exposed outside the dev network.
"""

from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles
import pathlib

from app.clients.supervisor import client as supervisor
from app.routers import agents, mcp, users

_STATIC_DIR = pathlib.Path(__file__).parent / "static"


@asynccontextmanager
async def lifespan(app: FastAPI):
    try:
        yield
    finally:
        await supervisor.close()


app = FastAPI(title="Aviary Admin", lifespan=lifespan)
app.mount("/static", StaticFiles(directory=str(_STATIC_DIR)), name="static")

app.include_router(agents.router)
app.include_router(mcp.router)
app.include_router(users.router)


@app.get("/health")
async def health():
    return {"status": "ok"}
