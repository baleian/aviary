"""runtime_proxy.stream_from_pool — SSE consumption + Redis publish."""

from __future__ import annotations

import json
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest

from app.services import runtime_proxy


def _sse_stream_response(events: list[dict], status: int = 200) -> MagicMock:
    """Build a mock httpx.Response that iterates `events` as SSE `data:` lines."""
    resp = MagicMock()
    resp.status_code = status

    async def aiter_lines():
        for e in events:
            yield f"data: {json.dumps(e)}"

    async def aread():
        return b""

    resp.aiter_lines = aiter_lines
    resp.aread = aread
    return resp


@pytest.fixture
def fake_stream():
    """Patch runtime_proxy._open_stream to yield a crafted response."""
    def _factory(events, status=200):
        resp = _sse_stream_response(events, status)

        class _Ctx:
            async def __aenter__(self): return resp
            async def __aexit__(self, *a): return False

        return patch("app.services.runtime_proxy._open_stream", return_value=_Ctx())
    return _factory


@pytest.fixture
def redis_mock():
    with patch("app.services.runtime_proxy.redis_client.append_stream_chunk", new_callable=AsyncMock) as a, \
         patch("app.services.runtime_proxy.redis_client.publish_message", new_callable=AsyncMock) as p, \
         patch("app.services.runtime_proxy.redis_client.set_stream_status", new_callable=AsyncMock) as s:
        yield {"append": a, "publish": p, "status": s}


@pytest.mark.asyncio
async def test_stream_publishes_each_event_and_returns_complete(fake_stream, redis_mock):
    events = [
        {"type": "query_started"},          # triggers reached_runtime, not published
        {"type": "chunk", "content": "hi"},
        {"type": "tool_use", "name": "ls"},
        {"type": "result", "total_cost_usd": 0.01},
    ]
    with fake_stream(events):
        out = await runtime_proxy.stream_from_pool(
            pool_name="default", session_id="s1", agent_id="a1", body={},
        )

    assert out["status"] == "complete"
    assert out["reached_runtime"] is True
    assert out["result_meta"]["total_cost_usd"] == 0.01
    # query_started is not forwarded; other 3 are.
    assert redis_mock["append"].await_count == 3
    assert redis_mock["publish"].await_count == 3
    redis_mock["status"].assert_awaited_with("s1", "complete")


@pytest.mark.asyncio
async def test_stream_error_event_short_circuits(fake_stream, redis_mock):
    events = [
        {"type": "query_started"},
        {"type": "error", "message": "kaboom"},
        {"type": "chunk", "content": "never published"},
    ]
    with fake_stream(events):
        out = await runtime_proxy.stream_from_pool(
            pool_name="default", session_id="s1", agent_id="a1", body={},
        )

    assert out["status"] == "error"
    assert "kaboom" in out["message"]
    assert out["reached_runtime"] is True
    assert redis_mock["append"].await_count == 0  # no events published before error
    redis_mock["status"].assert_awaited_with("s1", "error")


@pytest.mark.asyncio
async def test_stream_non200_status_reports_error(fake_stream, redis_mock):
    with fake_stream([], status=503):
        out = await runtime_proxy.stream_from_pool(
            pool_name="default", session_id="s1", agent_id="a1", body={},
        )

    assert out["status"] == "error"
    assert "503" in out["message"]
    assert out["reached_runtime"] is False


@pytest.mark.asyncio
async def test_stream_http_exception_surfaces(redis_mock):
    class _Ctx:
        async def __aenter__(self):
            raise httpx.ConnectError("dns fail")
        async def __aexit__(self, *a): return False

    with patch("app.services.runtime_proxy._open_stream", return_value=_Ctx()):
        out = await runtime_proxy.stream_from_pool(
            pool_name="default", session_id="s1", agent_id="a1", body={},
        )

    assert out["status"] == "error"
    assert "dns fail" in out["message"]


@pytest.mark.asyncio
async def test_abort_cancels_active_stream_task(redis_mock):
    """abort_session cancels the tracked Task, which propagates into the
    httpx stream context and closes the connection to the pool pod."""
    import asyncio

    started = asyncio.Event()
    cancelled_inside_stream = asyncio.Event()

    class _NeverEndingCtx:
        async def __aenter__(self):
            started.set()
            # Block forever — simulates a long-running SSE stream.
            try:
                await asyncio.Event().wait()
            except asyncio.CancelledError:
                cancelled_inside_stream.set()
                raise
            return None

        async def __aexit__(self, *a):
            return False

    with patch("app.services.runtime_proxy._open_stream", return_value=_NeverEndingCtx()):
        stream_task = asyncio.create_task(
            runtime_proxy.stream_from_pool(
                pool_name="default", session_id="abort-s1", agent_id="a1", body={},
            )
        )
        await started.wait()

        result = await runtime_proxy.abort_session(pool_name="default", session_id="abort-s1")
        assert result == {"ok": True}

        final = await stream_task
        assert final["status"] == "aborted"
    assert cancelled_inside_stream.is_set()


@pytest.mark.asyncio
async def test_abort_noop_when_no_active_stream():
    result = await runtime_proxy.abort_session(pool_name="default", session_id="nonexistent")
    assert result == {"ok": False, "reason": "no_active_stream"}


@pytest.mark.asyncio
async def test_stream_forwards_session_and_agent_into_body(redis_mock):
    captured = {}

    class _Ctx:
        async def __aenter__(self_inner):
            return _sse_stream_response([{"type": "result"}])
        async def __aexit__(self_inner, *a): return False

    def _spy_open(pool_name, subpath, body):
        captured["pool_name"] = pool_name
        captured["subpath"] = subpath
        captured["body"] = body
        return _Ctx()

    with patch("app.services.runtime_proxy._open_stream", side_effect=_spy_open):
        await runtime_proxy.stream_from_pool(
            pool_name="default", session_id="s1", agent_id="a1",
            body={"content_parts": [{"text": "hi"}]},
        )

    assert captured["pool_name"] == "default"
    assert captured["subpath"] == "/message"
    assert captured["body"]["session_id"] == "s1"
    assert captured["body"]["agent_id"] == "a1"
    assert captured["body"]["content_parts"] == [{"text": "hi"}]
