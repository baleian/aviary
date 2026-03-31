"""Inference backend endpoints — proxied through the Inference Router.

All provider access goes through the inference router service.
This ensures a single enforcement point for RBAC, key management, and quotas.
The API server never calls LLM providers directly.
"""

import logging

import httpx
from fastapi import APIRouter, Depends

from app.auth.dependencies import get_current_user
from app.config import settings
from app.db.models import User

logger = logging.getLogger(__name__)

router = APIRouter()


@router.get("/backends")
async def list_backends(user: User = Depends(get_current_user)):
    """List available inference backends (via inference router)."""
    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(f"{settings.inference_router_url}/v1/backends")
            resp.raise_for_status()
            return resp.json()
    except Exception as e:
        logger.warning("Failed to reach inference router: %s", e)
        return {"backends": ["claude", "ollama", "vllm", "bedrock"]}


@router.get("/{backend}/models")
async def list_models(backend: str, user: User = Depends(get_current_user)):
    """List available models for a backend (via inference router)."""
    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(
                f"{settings.inference_router_url}/v1/backends/{backend}/models"
            )
            resp.raise_for_status()
            return resp.json()
    except Exception as e:
        logger.warning("Failed to fetch models from inference router: %s", e)
        return {"models": [], "error": "Inference router not reachable"}


@router.get("/{backend}/health")
async def check_backend_health(backend: str, user: User = Depends(get_current_user)):
    """Check connectivity to an inference backend (via inference router)."""
    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(
                f"{settings.inference_router_url}/v1/backends/{backend}/health"
            )
            resp.raise_for_status()
            return resp.json()
    except Exception as e:
        logger.warning("Failed to check backend health: %s", e)
        return {"status": "error", "message": str(e)}
