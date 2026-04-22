"""Catalog publish / drift / rollback / unpublish coverage."""

import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


async def _create_agent(client: AsyncClient, slug: str = "pub-agent") -> str:
    resp = await client.post("/api/agents", json={
        "name": "Publishable",
        "slug": slug,
        "description": "For catalog",
        "instruction": "Be useful.",
        "model_config": _MODEL_CONFIG,
    })
    assert resp.status_code == 201, resp.text
    return resp.json()["id"]


@pytest.mark.asyncio
async def test_first_publish_creates_catalog_agent_and_v1(user1_client: AsyncClient):
    agent_id = await _create_agent(user1_client)

    resp = await user1_client.post(
        f"/api/agents/{agent_id}/publish",
        json={"category": "coding"},
    )
    assert resp.status_code == 200, resp.text
    v1 = resp.json()
    assert v1["version_number"] == 1
    assert v1["name"] == "Publishable"
    assert v1["category"] == "coding"

    # Agent now has linked_catalog_agent_id set.
    agent = (await user1_client.get(f"/api/agents/{agent_id}")).json()
    assert agent["linked_catalog_agent_id"] is not None
    assert agent["catalog_import_id"] is None


@pytest.mark.asyncio
async def test_republish_increments_version(user1_client: AsyncClient):
    agent_id = await _create_agent(user1_client, slug="pub-v2")
    await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )
    # Edit instruction
    await user1_client.put(
        f"/api/agents/{agent_id}",
        json={"instruction": "Be even more useful.", "model_config": _MODEL_CONFIG},
    )
    resp = await user1_client.post(
        f"/api/agents/{agent_id}/publish",
        json={"category": "coding", "release_notes": "tighter prompt"},
    )
    assert resp.status_code == 200, resp.text
    v2 = resp.json()
    assert v2["version_number"] == 2
    assert v2["release_notes"] == "tighter prompt"


@pytest.mark.asyncio
async def test_drift_clean_after_publish(user1_client: AsyncClient):
    agent_id = await _create_agent(user1_client, slug="drift-clean")
    await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )
    drift = (await user1_client.get(f"/api/agents/{agent_id}/drift")).json()
    assert drift["is_dirty"] is False
    assert drift["latest_version_number"] == 1
    assert drift["changed_fields"] == []


@pytest.mark.asyncio
async def test_drift_dirty_after_edit(user1_client: AsyncClient):
    agent_id = await _create_agent(user1_client, slug="drift-dirty")
    await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )
    await user1_client.put(
        f"/api/agents/{agent_id}",
        json={"instruction": "Mutated.", "model_config": _MODEL_CONFIG},
    )
    drift = (await user1_client.get(f"/api/agents/{agent_id}/drift")).json()
    assert drift["is_dirty"] is True
    assert "instruction" in drift["changed_fields"]


@pytest.mark.asyncio
async def test_drift_never_published(user1_client: AsyncClient):
    agent_id = await _create_agent(user1_client, slug="never-pub")
    drift = (await user1_client.get(f"/api/agents/{agent_id}/drift")).json()
    assert drift["is_dirty"] is True
    assert drift["latest_version_number"] is None
    assert drift["changed_fields"] == ["*"]


@pytest.mark.asyncio
async def test_imported_agent_cannot_publish(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    agent_id = await _create_agent(user1_client, slug="publishable")
    await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    imp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    imported_id = imp.json()["created"][0]["id"]

    resp = await user2_client.post(
        f"/api/agents/{imported_id}/publish", json={"category": "coding"}
    )
    assert resp.status_code == 409
