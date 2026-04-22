"""Import → fork flow tests."""

import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


async def _publish_agent(
    client: AsyncClient, slug: str = "to-publish", category: str = "coding",
    instruction: str = "Be useful."
):
    resp = await client.post("/api/agents", json={
        "name": f"Agent {slug}",
        "slug": slug,
        "instruction": instruction,
        "model_config": _MODEL_CONFIG,
    })
    agent_id = resp.json()["id"]
    resp = await client.post(
        f"/api/agents/{agent_id}/publish",
        json={"category": category},
    )
    return agent_id, resp.json()


@pytest.mark.asyncio
async def test_import_creates_new_agents_row(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    _, _ = await _publish_agent(user1_client, slug="shared-thing")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]

    resp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert len(body["created"]) == 1
    new_agent = body["created"][0]
    assert new_agent["slug"] == "shared-thing"
    assert new_agent["catalog_import_id"] is not None
    assert new_agent["linked_catalog_agent_id"] == catalog_id

    # Appears in user2's /api/agents list.
    mine = (await user2_client.get("/api/agents")).json()
    assert mine["total"] == 1
    assert mine["items"][0]["catalog_import_id"] is not None


@pytest.mark.asyncio
async def test_import_slug_collision_gets_suffix(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    # user2 already has an agent named "shared-thing"
    await user2_client.post("/api/agents", json={
        "name": "Mine",
        "slug": "shared-thing",
        "instruction": "Local",
        "model_config": _MODEL_CONFIG,
    })
    await _publish_agent(user1_client, slug="shared-thing")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]

    resp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })
    assert resp.status_code == 200, resp.text
    assert resp.json()["created"][0]["slug"] == "shared-thing-2"


@pytest.mark.asyncio
async def test_import_is_idempotent(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    await _publish_agent(user1_client, slug="idem")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]

    await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })
    second = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })
    body = second.json()
    assert body["created"] == []
    assert len(body["already_imported"]) == 1


@pytest.mark.asyncio
async def test_imported_agent_cannot_be_updated_directly(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    await _publish_agent(user1_client, slug="readonly")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    import_body = (await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })).json()
    new_agent_id = import_body["created"][0]["id"]

    resp = await user2_client.put(f"/api/agents/{new_agent_id}", json={
        "name": "Trying to edit",
        "slug": "readonly",
        "instruction": "nope",
        "model_config": _MODEL_CONFIG,
    })
    assert resp.status_code == 409


@pytest.mark.asyncio
async def test_fork_detaches_in_place(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    await _publish_agent(user1_client, slug="forkable",
                         instruction="ORIGINAL INSTR")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    import_body = (await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })).json()
    new_agent_id = import_body["created"][0]["id"]

    # Fork
    resp = await user2_client.post(f"/api/catalog/imports/{new_agent_id}/fork")
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["id"] == new_agent_id  # same id (in-place)
    assert body["catalog_import_id"] is None
    assert body["linked_catalog_agent_id"] is None
    assert body["instruction"] == "ORIGINAL INSTR"

    # Now editable.
    resp = await user2_client.put(f"/api/agents/{new_agent_id}", json={
        "name": "mine now",
        "slug": "forkable",
        "instruction": "custom",
        "model_config": _MODEL_CONFIG,
    })
    assert resp.status_code == 200


@pytest.mark.asyncio
async def test_restore_creates_working_copy_preserves_visibility(
    user1_client: AsyncClient
):
    # User1 publishes → catalog visible.
    await _publish_agent(user1_client, slug="restore-me")
    catalog_id = (await user1_client.get("/api/catalog/mine")).json()["items"][0]["id"]

    # Delete the private working copy (agents row).
    my_agents = (await user1_client.get("/api/agents")).json()
    private_id = my_agents["items"][0]["id"]
    await user1_client.delete(f"/api/agents/{private_id}")

    # Unpublish the catalog so visibility is off.
    await user1_client.post(f"/api/catalog/agents/{catalog_id}/unpublish")

    # Restore via /catalog/agents/{cid}/restore.
    resp = await user1_client.post(f"/api/catalog/agents/{catalog_id}/restore")
    assert resp.status_code == 200, resp.text
    new_working_copy = resp.json()
    assert new_working_copy["linked_catalog_agent_id"] == catalog_id
    assert new_working_copy["catalog_import_id"] is None

    # Catalog visibility is NOT automatically re-enabled.
    mine = (await user1_client.get("/api/catalog/mine")).json()
    assert any(
        item["id"] == catalog_id and item["is_published"] is False
        for item in mine["items"]
    )


@pytest.mark.asyncio
async def test_patch_import_pin(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    agent_id, v1 = await _publish_agent(user1_client, slug="pinable")
    # Edit → publish v2
    await user1_client.put(f"/api/agents/{agent_id}", json={
        "name": "Agent pinable",
        "slug": "pinable",
        "instruction": "v2",
        "model_config": _MODEL_CONFIG,
    })
    _ = await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )

    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    imp = (await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })).json()
    new_agent_id = imp["created"][0]["id"]

    # Pin to v1.
    resp = await user2_client.patch(
        f"/api/catalog/imports/{new_agent_id}",
        json={"pinned_version_id": v1["id"]},
    )
    assert resp.status_code == 200, resp.text
