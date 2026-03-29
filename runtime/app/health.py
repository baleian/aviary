"""Health and readiness probes for the agent runtime."""

from fastapi import APIRouter

router = APIRouter()

_ready = False


def set_ready(ready: bool = True):
    global _ready
    _ready = ready


@router.get("/health")
async def health():
    return {"status": "ok"}


@router.get("/ready")
async def ready():
    if not _ready:
        from fastapi.responses import JSONResponse
        return JSONResponse({"status": "not_ready"}, status_code=503)
    return {"status": "ready"}
