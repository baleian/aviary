"""Tests for session deletion with full resource cleanup and agent soft-delete behavior."""

from unittest.mock import AsyncMock, patch

import pytest
from httpx import AsyncClient


async def _create_agent(client: AsyncClient, slug: str, visibility: str = "public") -> str:
    resp = await client.post("/api/agents", json={
        "name": f"Agent {slug}",
        "slug": slug,
        "instruction": "Be helpful.",
        "visibility": visibility,
    })
    assert resp.status_code == 201
    return resp.json()["id"]


async def _create_session(client: AsyncClient, agent_id: str) -> str:
    resp = await client.post(f"/api/agents/{agent_id}/sessions", json={"type": "private"})
    assert resp.status_code == 201
    return resp.json()["id"]


# ── Session Deletion ─────────────────────────────────────────


@pytest.mark.asyncio
async def test_delete_session(admin_client: AsyncClient):
    """Deleting a session returns 204 and removes it from DB."""
    agent_id = await _create_agent(admin_client, "del-sess")
    session_id = await _create_session(admin_client, agent_id)

    with patch("app.services.controller_client.cleanup_session_workspace", new_callable=AsyncMock):
        resp = await admin_client.delete(f"/api/sessions/{session_id}")
    assert resp.status_code == 204

    # Session should be gone
    resp = await admin_client.get(f"/api/sessions/{session_id}")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_delete_session_removes_messages(admin_client: AsyncClient):
    """Messages are cascade-deleted with the session."""
    agent_id = await _create_agent(admin_client, "del-msgs")
    session_id = await _create_session(admin_client, agent_id)

    # Verify session exists with empty messages
    resp = await admin_client.get(f"/api/sessions/{session_id}")
    assert resp.status_code == 200
    assert resp.json()["messages"] == []

    with patch("app.services.controller_client.cleanup_session_workspace", new_callable=AsyncMock):
        resp = await admin_client.delete(f"/api/sessions/{session_id}")
    assert resp.status_code == 204

    # Session gone — 404
    resp = await admin_client.get(f"/api/sessions/{session_id}")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_delete_session_not_found(admin_client: AsyncClient):
    resp = await admin_client.delete("/api/sessions/00000000-0000-0000-0000-000000000000")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_delete_session_forbidden_for_non_creator(
    admin_client: AsyncClient, user2_client: AsyncClient,
):
    """Only session creator or platform admin can delete."""
    agent_id = await _create_agent(admin_client, "del-perm")
    session_id = await _create_session(admin_client, agent_id)

    # user2 is not the creator and not admin
    resp = await user2_client.delete(f"/api/sessions/{session_id}")
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_delete_session_does_not_affect_other_sessions(admin_client: AsyncClient):
    """Deleting one session leaves other sessions intact."""
    agent_id = await _create_agent(admin_client, "del-other")
    session1 = await _create_session(admin_client, agent_id)
    session2 = await _create_session(admin_client, agent_id)

    with patch("app.services.controller_client.cleanup_session_workspace", new_callable=AsyncMock):
        resp = await admin_client.delete(f"/api/sessions/{session1}")
    assert resp.status_code == 204

    # session2 still exists
    resp = await admin_client.get(f"/api/sessions/{session2}")
    assert resp.status_code == 200

    # session list should have 1
    resp = await admin_client.get(f"/api/agents/{agent_id}/sessions")
    assert len(resp.json()["items"]) == 1


# ── Session Title Update ──────────────────────────────────────


@pytest.mark.asyncio
async def test_update_session_title(admin_client: AsyncClient):
    agent_id = await _create_agent(admin_client, "title-edit")
    session_id = await _create_session(admin_client, agent_id)

    resp = await admin_client.patch(
        f"/api/sessions/{session_id}/title",
        json={"title": "My Custom Title"},
    )
    assert resp.status_code == 200
    assert resp.json()["title"] == "My Custom Title"

    # Verify persisted
    resp = await admin_client.get(f"/api/sessions/{session_id}")
    assert resp.json()["session"]["title"] == "My Custom Title"


# ── Agent Soft Delete ─────────────────────────────────────────


@pytest.mark.asyncio
async def test_delete_agent_with_sessions_keeps_sessions(admin_client: AsyncClient):
    """Deleting an agent with active sessions is a soft delete — sessions remain."""
    agent_id = await _create_agent(admin_client, "soft-del")
    session_id = await _create_session(admin_client, agent_id)

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    # Agent is still accessible via direct GET (include_deleted=True in router)
    resp = await admin_client.get(f"/api/agents/{agent_id}")
    assert resp.status_code == 200
    assert resp.json()["status"] == "deleted"

    # Session still works
    resp = await admin_client.get(f"/api/sessions/{session_id}")
    assert resp.status_code == 200


@pytest.mark.asyncio
async def test_deleted_agent_blocks_new_sessions(admin_client: AsyncClient):
    """Cannot create new sessions on a deleted agent (HTTP 410)."""
    agent_id = await _create_agent(admin_client, "no-new-sess")

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    resp = await admin_client.post(f"/api/agents/{agent_id}/sessions", json={"type": "private"})
    assert resp.status_code == 410


@pytest.mark.asyncio
async def test_deleted_agent_config_still_editable(admin_client: AsyncClient):
    """Config editing works on deleted agents."""
    agent_id = await _create_agent(admin_client, "edit-del")
    await _create_session(admin_client, agent_id)

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    resp = await admin_client.put(f"/api/agents/{agent_id}", json={
        "instruction": "Updated instruction after delete",
    })
    assert resp.status_code == 200
    assert resp.json()["instruction"] == "Updated instruction after delete"


@pytest.mark.asyncio
async def test_deleted_agent_visible_in_list_with_sessions(admin_client: AsyncClient):
    """Deleted agents with active sessions still appear in agent list."""
    agent_id = await _create_agent(admin_client, "visible-del")
    await _create_session(admin_client, agent_id)

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    resp = await admin_client.get("/api/agents")
    agent_ids = [a["id"] for a in resp.json()["items"]]
    assert agent_id in agent_ids


@pytest.mark.asyncio
async def test_deleted_agent_hidden_after_all_sessions_removed(admin_client: AsyncClient):
    """Once all sessions of a deleted agent are removed, agent disappears from list."""
    agent_id = await _create_agent(admin_client, "hidden-del")
    session_id = await _create_session(admin_client, agent_id)

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    with patch("app.services.controller_client.cleanup_session_workspace", new_callable=AsyncMock), \
         patch("app.services.agent_service.cleanup_agent_k8s_resources", new_callable=AsyncMock):
        resp = await admin_client.delete(f"/api/sessions/{session_id}")
    assert resp.status_code == 204

    resp = await admin_client.get("/api/agents")
    agent_ids = [a["id"] for a in resp.json()["items"]]
    assert agent_id not in agent_ids


@pytest.mark.asyncio
async def test_delete_agent_without_sessions_is_immediate(admin_client: AsyncClient):
    """Agent with no sessions is fully deleted (not visible at all)."""
    agent_id = await _create_agent(admin_client, "imm-del")

    resp = await admin_client.delete(f"/api/agents/{agent_id}")
    assert resp.status_code == 204

    resp = await admin_client.get("/api/agents")
    agent_ids = [a["id"] for a in resp.json()["items"]]
    assert agent_id not in agent_ids
