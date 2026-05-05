"""Inference backend endpoints — gateway passthrough."""

import httpx
from fastapi import APIRouter, Depends

from app.auth.dependencies import get_current_user
from app.config import settings
from app.db.models import User

router = APIRouter()


async def _gateway_models() -> list[dict]:
    headers = {"Authorization": f"Bearer {settings.llm_gateway_api_key}"}
    async with httpx.AsyncClient(timeout=10) as client:
        resp = await client.get(
            f"{settings.llm_gateway_url}/model/info", headers=headers,
        )
        resp.raise_for_status()
        raw = resp.json().get("data", [])
    models = []
    for m in raw:
        name = m.get("model_name", "")
        if "/" not in name:
            continue
        backend = name.split("/", 1)[0]
        models.append({
            "id": name,
            "name": name,
            "backend": backend,
            "model_info": m.get("model_info", {}),
        })
    return models


@router.get("/models")
async def list_models(user: User = Depends(get_current_user)):
    return {"models": await _gateway_models()}


@router.get("/{backend}/health")
async def check_backend_health(backend: str, user: User = Depends(get_current_user)):
    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(f"{settings.llm_gateway_url}/health/liveliness")
            return {"status": "ok" if resp.status_code == 200 else "error"}
    except httpx.HTTPError as e:
        return {"status": "error", "error": str(e)}
