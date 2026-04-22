"""Catalog browse / detail / versions / facets / /mine coverage."""

import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


async def _publish(client: AsyncClient, slug: str, category: str = "coding", **extra):
    resp = await client.post("/api/agents", json={
        "name": extra.get("name", f"Agent {slug}"),
        "slug": slug,
        "description": extra.get("description"),
        "instruction": extra.get("instruction", "Be useful."),
        "model_config": _MODEL_CONFIG,
    })
    agent_id = resp.json()["id"]
    resp = await client.post(
        f"/api/agents/{agent_id}/publish",
        json={"category": category},
    )
    assert resp.status_code == 200, resp.text
    return agent_id, resp.json()


@pytest.mark.asyncio
async def test_catalog_list_empty_by_default(user2_client: AsyncClient):
    resp = await user2_client.get("/api/catalog")
    assert resp.status_code == 200
    data = resp.json()
    assert data["total"] == 0
    assert data["items"] == []


@pytest.mark.asyncio
async def test_browse_sees_other_users_published(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    await _publish(user1_client, slug="public-one", category="coding")
    await _publish(user1_client, slug="public-two", category="writing")

    resp = await user2_client.get("/api/catalog")
    assert resp.status_code == 200
    data = resp.json()
    assert data["total"] == 2
    slugs = sorted(i["slug"] for i in data["items"])
    assert slugs == ["public-one", "public-two"]


@pytest.mark.asyncio
async def test_category_filter(user1_client: AsyncClient, user2_client: AsyncClient):
    await _publish(user1_client, slug="cat-a", category="coding")
    await _publish(user1_client, slug="cat-b", category="writing")

    resp = await user2_client.get("/api/catalog?category=coding")
    assert resp.json()["total"] == 1
    assert resp.json()["items"][0]["slug"] == "cat-a"


@pytest.mark.asyncio
async def test_text_search_on_description(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    await _publish(user1_client, slug="searchable", category="coding",
                   description="Finds bugs fast")
    await _publish(user1_client, slug="other", category="coding",
                   description="unrelated stuff")
    resp = await user2_client.get("/api/catalog?q=bugs")
    data = resp.json()
    assert data["total"] == 1
    assert data["items"][0]["slug"] == "searchable"


@pytest.mark.asyncio
async def test_detail_includes_owner_email_and_version(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    _, v = await _publish(user1_client, slug="detailed", category="coding")
    catalog_id = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]

    resp = await user2_client.get(f"/api/catalog/{catalog_id}")
    assert resp.status_code == 200, resp.text
    data = resp.json()
    assert data["owner_email"] == "user1@test.com"
    assert data["version"]["version_number"] == 1
    assert data["imported_as_agent_id"] is None


@pytest.mark.asyncio
async def test_unpublished_catalog_not_in_browse(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    _, _ = await _publish(user1_client, slug="will-unpub")
    catalog_id = (await user1_client.get("/api/catalog/mine")).json()["items"][0]["id"]

    await user1_client.post(f"/api/catalog/agents/{catalog_id}/unpublish")

    resp = await user2_client.get("/api/catalog")
    assert resp.json()["total"] == 0


@pytest.mark.asyncio
async def test_facets_count(user1_client: AsyncClient, user2_client: AsyncClient):
    await _publish(user1_client, slug="f-a", category="coding")
    await _publish(user1_client, slug="f-b", category="coding")
    await _publish(user1_client, slug="f-c", category="writing")
    resp = await user2_client.get("/api/catalog/facets")
    data = resp.json()
    cats = {c["name"]: c["count"] for c in data["categories"]}
    assert cats.get("coding") == 2
    assert cats.get("writing") == 1


@pytest.mark.asyncio
async def test_my_catalog_shows_own_including_unpublished(user1_client: AsyncClient):
    await _publish(user1_client, slug="own-one", category="coding")
    _, _ = await _publish(user1_client, slug="own-two", category="coding")
    mine = (await user1_client.get("/api/catalog/mine")).json()
    assert mine["total"] == 2

    catalog_id = mine["items"][0]["id"]
    await user1_client.post(f"/api/catalog/agents/{catalog_id}/unpublish")
    mine = (await user1_client.get("/api/catalog/mine")).json()
    assert mine["total"] == 2  # unpublished stays
    for item in mine["items"]:
        if item["id"] == catalog_id:
            assert item["is_published"] is False
            assert item["unpublished_at"] is not None


@pytest.mark.asyncio
async def test_rollback_moves_current_version(user1_client: AsyncClient):
    agent_id, v1 = await _publish(user1_client, slug="rb", category="coding")
    # Edit + republish = v2
    await user1_client.put(
        f"/api/agents/{agent_id}",
        json={"instruction": "edit", "model_config": _MODEL_CONFIG},
    )
    _ = await user1_client.post(
        f"/api/agents/{agent_id}/publish", json={"category": "coding"}
    )
    mine = (await user1_client.get("/api/catalog/mine")).json()["items"][0]
    assert mine["current_version_number"] == 2

    # Rollback to v1
    resp = await user1_client.post(
        f"/api/catalog/agents/{mine['id']}/rollback",
        json={"version_id": v1["id"]},
    )
    assert resp.status_code == 200, resp.text
    assert resp.json()["current_version_number"] == 1
