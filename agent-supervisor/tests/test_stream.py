"""FastAPI integration tests for /v1/stream endpoints."""

from __future__ import annotations

from unittest.mock import AsyncMock, patch

import pytest
from fastapi.testclient import TestClient

from app.main import app


@pytest.fixture
def client():
    # Skip the lifespan (no real Redis in unit tests).
    with TestClient(app) as c:
        yield c


def test_health_endpoint(client):
    resp = client.get("/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_metrics_endpoint_exposes_prometheus(client):
    resp = client.get("/metrics")
    assert resp.status_code == 200
    body = resp.text
    assert "agent_supervisor_active_streams" in body
    assert "agent_supervisor_events_total" in body


def test_stream_endpoint_delegates_to_runtime_proxy(client):
    with patch("app.routers.stream.runtime_proxy.stream_from_pool", new_callable=AsyncMock) as m:
        m.return_value = {"status": "complete", "reached_runtime": True, "result_meta": None}
        resp = client.post("/v1/stream", json={
            "pool_name": "default",
            "session_id": "s1",
            "agent_id": "a1",
            "body": {"content_parts": [{"text": "hi"}], "agent_config": {}},
        })

    assert resp.status_code == 200
    assert resp.json()["status"] == "complete"
    call = m.await_args
    assert call.kwargs["pool_name"] == "default"
    assert call.kwargs["session_id"] == "s1"
    assert call.kwargs["agent_id"] == "a1"


def test_abort_endpoint(client):
    with patch("app.routers.stream.runtime_proxy.abort_session", new_callable=AsyncMock) as m:
        m.return_value = {"ok": True, "status": 200}
        resp = client.post("/v1/sessions/s1/abort", json={"pool_name": "default"})

    assert resp.status_code == 200
    m.assert_awaited_once_with(pool_name="default", session_id="s1")


def test_cleanup_endpoint(client):
    with patch("app.routers.stream.runtime_proxy.cleanup_session", new_callable=AsyncMock) as m:
        m.return_value = {"ok": True, "status": 200}
        resp = client.delete("/v1/sessions/s1", params={"pool_name": "default", "agent_id": "a1"})

    assert resp.status_code == 200
    m.assert_awaited_once_with(pool_name="default", session_id="s1", agent_id="a1")
