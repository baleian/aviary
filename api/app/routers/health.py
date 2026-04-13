from fastapi import APIRouter

from app.deps import get_redis
from app.services.supervisor import supervisor_client

router = APIRouter()


@router.get("/api/health")
async def health():
    redis_ok = False
    try:
        redis_ok = await get_redis().ping()
    except Exception:
        pass
    supervisor_ok = await supervisor_client.health()
    return {
        "status": "ok",
        "redis": "ok" if redis_ok else "down",
        "supervisor": "ok" if supervisor_ok else "down",
    }
