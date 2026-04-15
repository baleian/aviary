"""runtime_proxy — SSE consumption, Redis publish, and stream_id-based abort routing."""

from __future__ import annotations

import json
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest

from app.services import runtime_proxy


def _sse_stream_response(
    events: list[dict], *, status: int = 200, runtime_pod_ip: str | None = "10.42.0.7",
) -> MagicMock:
    """Mock httpx.Response that iterates `events` as SSE lines and advertises
    X-Runtime-Pod-IP — the supervisor should record this in Redis."""
    resp = MagicMock()
    resp.status_code = status
    resp.headers = {"x-runtime-pod-ip": runtime_pod_ip} if runtime_pod_ip else {}

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
    def _factory(events, *, status=200, runtime_pod_ip="10.42.0.7"):
        resp = _sse_stream_response(events, status=status, runtime_pod_ip=runtime_pod_ip)

        class _Ctx:
            async def __aenter__(self): return resp
            async def __aexit__(self, *a): return False

        return patch("app.services.runtime_proxy._open_stream", return_value=_Ctx())
    return _factory


@pytest.fixture
def redis_mock():
    with patch("app.services.runtime_proxy.redis_client.append_stream_chunk", new_callable=AsyncMock) as a, \
         patch("app.services.runtime_proxy.redis_client.publish_message", new_callable=AsyncMock) as p, \
         patch("app.services.runtime_proxy.redis_client.set_stream_status", new_callable=AsyncMock) as s, \
         patch("app.services.runtime_proxy.redis_client.set_stream_runtime_addr", new_callable=AsyncMock) as sra, \
         patch("app.services.runtime_proxy.redis_client.get_stream_runtime_addr", new_callable=AsyncMock) as gra, \
         patch("app.services.runtime_proxy.redis_client.delete_stream_runtime_addr", new_callable=AsyncMock) as dra:
        gra.return_value = None
        yield {
            "append": a, "publish": p, "status": s,
            "set_runtime": sra, "get_runtime": gra, "del_runtime": dra,
        }


def _stream_kwargs(stream_id: str = "stream-1", session_id: str = "s1") -> dict:
    return {
        "pool_name": "default",
        "session_id": session_id,
        "agent_id": "a1",
        "stream_id": stream_id,
        "body": {},
    }


@pytest.mark.asyncio
async def test_stream_records_runtime_pod_addr_in_redis(fake_stream, redis_mock):
    with fake_stream([{"type": "result"}]):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "complete"
    # Runtime address captured from X-Runtime-Pod-IP header and stored.
    redis_mock["set_runtime"].assert_awaited_once()
    call = redis_mock["set_runtime"].await_args
    assert call.args[0] == "stream-1"
    assert call.args[1] == "10.42.0.7:3000"
    # Cleaned up on completion.
    redis_mock["del_runtime"].assert_awaited_with("stream-1")


@pytest.mark.asyncio
async def test_stream_publishes_events_and_returns_complete(fake_stream, redis_mock):
    events = [
        {"type": "query_started"},
        {"type": "chunk", "content": "hi"},
        {"type": "tool_use", "name": "ls"},
        {"type": "result", "total_cost_usd": 0.01},
    ]
    with fake_stream(events):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "complete"
    assert out["reached_runtime"] is True
    assert out["result_meta"]["total_cost_usd"] == 0.01
    assert redis_mock["append"].await_count == 3  # query_started not published
    assert redis_mock["publish"].await_count == 3


@pytest.mark.asyncio
async def test_stream_aborted_event_returns_aborted_status(fake_stream, redis_mock):
    """Runtime emits {type: 'aborted'} when an external /abort caused the SDK
    to terminate. Supervisor must classify this as status=aborted (not
    complete, not error) so callers can differentiate."""
    events = [
        {"type": "query_started"},
        {"type": "chunk", "content": "hel"},
        {"type": "chunk", "content": "[Cancelled by user]"},
        {"type": "aborted"},
    ]
    with fake_stream(events):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "aborted"
    assert out["reached_runtime"] is True
    # The two chunk events are published; the terminator is not.
    assert redis_mock["append"].await_count == 2
    redis_mock["status"].assert_awaited_with("s1", "aborted")
    redis_mock["del_runtime"].assert_awaited_with("stream-1")


@pytest.mark.asyncio
async def test_stream_error_event_short_circuits(fake_stream, redis_mock):
    events = [
        {"type": "query_started"},
        {"type": "error", "message": "kaboom"},
    ]
    with fake_stream(events):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "error"
    assert "kaboom" in out["message"]
    redis_mock["status"].assert_awaited_with("s1", "error")


@pytest.mark.asyncio
async def test_stream_non200_status_reports_error(fake_stream, redis_mock):
    with fake_stream([], status=503):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "error"
    assert "503" in out["message"]


@pytest.mark.asyncio
async def test_stream_without_runtime_pod_ip_header_skips_redis_registration(
    fake_stream, redis_mock,
):
    """If the runtime pod doesn't advertise its IP, supervisor can't route
    abort — log and proceed, don't crash."""
    with fake_stream([{"type": "result"}], runtime_pod_ip=None):
        out = await runtime_proxy.stream_from_pool(**_stream_kwargs())

    assert out["status"] == "complete"
    redis_mock["set_runtime"].assert_not_awaited()


@pytest.mark.asyncio
async def test_abort_dials_runtime_pod_directly(redis_mock):
    """Abort reads runtime address from Redis and POSTs to the pod's
    /abort/{session_id}, bypassing the pool Service LB."""
    redis_mock["get_runtime"].return_value = "10.42.0.7:3000"
    sent = {}

    class _FakeClient:
        def __init__(self, *a, **kw): pass
        async def __aenter__(self): return self
        async def __aexit__(self, *a): return False
        async def post(self, url, **kw):
            sent["url"] = url
            resp = MagicMock(); resp.status_code = 200; return resp

    with patch("app.services.runtime_proxy.httpx.AsyncClient", _FakeClient):
        out = await runtime_proxy.abort_stream(stream_id="stream-1", session_id="sess-x")

    assert out == {"ok": True, "runtime_addr": "10.42.0.7:3000"}
    assert sent["url"] == "http://10.42.0.7:3000/abort/sess-x"


@pytest.mark.asyncio
async def test_abort_returns_no_active_stream_when_redis_missing(redis_mock):
    redis_mock["get_runtime"].return_value = None
    out = await runtime_proxy.abort_stream(stream_id="ghost", session_id="sess-x")
    assert out == {"ok": False, "reason": "no_active_stream"}


@pytest.mark.asyncio
async def test_abort_runtime_unreachable_reaps_stale_mapping(redis_mock):
    redis_mock["get_runtime"].return_value = "10.42.0.99:3000"

    class _FakeClient:
        def __init__(self, *a, **kw): pass
        async def __aenter__(self): return self
        async def __aexit__(self, *a): return False
        async def post(self, *a, **kw):
            raise httpx.ConnectError("no route")

    with patch("app.services.runtime_proxy.httpx.AsyncClient", _FakeClient):
        out = await runtime_proxy.abort_stream(stream_id="stream-dead", session_id="sess-x")

    assert out["ok"] is False
    assert out["reason"] == "runtime_unreachable"
    redis_mock["del_runtime"].assert_awaited_with("stream-dead")


@pytest.mark.asyncio
async def test_abort_session_gone_on_runtime_reports_session_not_on_runtime(redis_mock):
    """Runtime returns 404 if the session already finished / wasn't there."""
    redis_mock["get_runtime"].return_value = "10.42.0.7:3000"

    class _FakeClient:
        def __init__(self, *a, **kw): pass
        async def __aenter__(self): return self
        async def __aexit__(self, *a): return False
        async def post(self, *a, **kw):
            resp = MagicMock(); resp.status_code = 404; return resp

    with patch("app.services.runtime_proxy.httpx.AsyncClient", _FakeClient):
        out = await runtime_proxy.abort_stream(stream_id="stream-1", session_id="sess-x")

    assert out["ok"] is False
    assert out["reason"] == "session_not_on_runtime"
