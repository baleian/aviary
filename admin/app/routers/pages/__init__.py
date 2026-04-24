"""Combined router for all admin HTML pages."""

from fastapi import APIRouter

from app.routers.pages.agents import router as agents_router
from app.routers.pages.workflows import router as workflows_router

router = APIRouter()
router.include_router(agents_router)
router.include_router(workflows_router)
