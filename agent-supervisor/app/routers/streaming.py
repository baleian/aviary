"""Transparent SSE proxy to agent runtime Pods + abort relay."""

import logging

import httpx
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import StreamingResponse

from aviary_shared.naming import RUNTIME_PORT, SERVICE_NAME

from app.k8s import _get_k8s_client

logger = logging.getLogger(__name__)

router = APIRouter()


@router.post("/stream/{namespace}/message")
async def proxy_message(namespace: str, request: Request):
    """Transparent SSE proxy to agent-runtime-svc via K8s Service proxy.

    Byte-level pass-through — no parsing, no buffering.
    """
    body = await request.json()
    proxy_path = (
        f"/api/v1/namespaces/{namespace}/services/{SERVICE_NAME}:{RUNTIME_PORT}/proxy/message"
    )

    async def generate():
        try:
            async with _get_k8s_client() as client:
                async with client.stream(
                    "POST", proxy_path, json=body, timeout=300
                ) as resp:
                    if resp.status_code != 200:
                        error_body = await resp.aread()
                        logger.error(
                            "Pod stream returned %d: %s", resp.status_code, error_body
                        )
                        return
                    async for chunk in resp.aiter_bytes():
                        yield chunk
        except httpx.HTTPError:
            logger.exception("SSE proxy error for namespace %s", namespace)

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "X-Accel-Buffering": "no",
        },
    )


@router.post("/stream/{namespace}/abort/{session_id}")
async def abort_stream(namespace: str, session_id: str):
    """Relay abort request to the runtime Pod."""
    proxy_path = (
        f"/api/v1/namespaces/{namespace}/services/{SERVICE_NAME}:{RUNTIME_PORT}/proxy/abort/{session_id}"
    )
    try:
        async with _get_k8s_client() as client:
            resp = await client.post(proxy_path, timeout=5)
            return {"ok": True, "pod_status": resp.status_code}
    except httpx.HTTPError as e:
        logger.warning("Failed to send abort for session %s in %s", session_id, namespace, exc_info=True)
        raise HTTPException(status_code=502, detail="Failed to reach runtime Pod") from e
