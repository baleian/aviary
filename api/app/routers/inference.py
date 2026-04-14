"""Inference models — passthrough to LiteLLM's /model/info."""

import httpx
from fastapi import APIRouter, Depends, HTTPException, status

from app.auth.dependencies import get_current_user
from app.config import settings
from aviary_shared.db.models import User

router = APIRouter()


async def _fetch_model_info() -> list[dict]:
    async with httpx.AsyncClient(timeout=10) as client:
        resp = await client.get(
            f"{settings.litellm_url}/model/info",
            headers={"Authorization": f"Bearer {settings.litellm_api_key}"},
        )
        resp.raise_for_status()
        return resp.json().get("data", [])


@router.get("/models")
async def list_models(_user: User = Depends(get_current_user)):
    try:
        raw = await _fetch_model_info()
    except httpx.HTTPError as e:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"LiteLLM error: {e}") from e

    models = []
    for m in raw:
        name = m.get("model_name", "")
        if "/" not in name:
            continue
        models.append({
            "id": name,
            "name": name,
            "backend": name.split("/", 1)[0],
            "model_info": m.get("model_info", {}),
        })
    return {"models": models}
