"""End-to-end smoke — Phase 1-4 catalog flow from publish through fork."""

import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


@pytest.mark.asyncio
async def test_full_catalog_flow(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    # 1. user1 creates a private agent and publishes v1.
    create = await user1_client.post("/api/agents", json={
        "name": "E2E Agent",
        "slug": "e2e-agent",
        "description": "End-to-end test subject",
        "instruction": "I help test things.",
        "model_config": _MODEL_CONFIG,
    })
    agent1_id = create.json()["id"]

    v1 = await user1_client.post(
        f"/api/agents/{agent1_id}/publish",
        json={"category": "coding", "release_notes": "initial"},
    )
    assert v1.status_code == 200

    # 2. drift is clean after publish.
    drift = (await user1_client.get(f"/api/agents/{agent1_id}/drift")).json()
    assert drift["is_dirty"] is False

    # 3. edit instruction → drift dirty.
    await user1_client.put(f"/api/agents/{agent1_id}", json={
        "name": "E2E Agent",
        "slug": "e2e-agent",
        "instruction": "I help test things harder.",
        "model_config": _MODEL_CONFIG,
    })
    drift = (await user1_client.get(f"/api/agents/{agent1_id}/drift")).json()
    assert drift["is_dirty"] is True
    assert "instruction" in drift["changed_fields"]

    # 4. publish v2.
    await user1_client.post(
        f"/api/agents/{agent1_id}/publish",
        json={"category": "coding", "release_notes": "sharpened"},
    )

    # 5. user2 browses catalog, imports, verifies "From Catalog" section.
    browse = (await user2_client.get("/api/catalog")).json()
    assert browse["total"] == 1
    catalog_id = browse["items"][0]["id"]

    imported = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": catalog_id}],
    })
    new_agent_id = imported.json()["created"][0]["id"]

    listed = (await user2_client.get("/api/agents")).json()
    assert any(
        a["id"] == new_agent_id and a["catalog_import_id"] is not None
        for a in listed["items"]
    )

    # 6. user2 tries to edit imported agent — blocked.
    block = await user2_client.put(f"/api/agents/{new_agent_id}", json={
        "name": "hacked",
        "slug": "e2e-agent",
        "instruction": "mine",
        "model_config": _MODEL_CONFIG,
    })
    assert block.status_code == 409

    # 7. user1 rolls back to v1.
    mine = (await user1_client.get("/api/catalog/mine")).json()["items"][0]
    versions = (await user1_client.get(
        f"/api/catalog/agents/{mine['id']}/versions"
    )).json()
    v1_id = next(v["id"] for v in versions["items"] if v["version_number"] == 1)
    rb = await user1_client.post(
        f"/api/catalog/agents/{mine['id']}/rollback",
        json={"version_id": v1_id},
    )
    assert rb.status_code == 200
    assert rb.json()["current_version_number"] == 1

    # 8. user1 unpublishes the catalog — user2's import stays deprecated.
    await user1_client.post(f"/api/catalog/agents/{mine['id']}/unpublish")
    browse2 = (await user2_client.get("/api/catalog")).json()
    assert browse2["total"] == 0
    # import still works (the row still exists on user2's side).
    still_there = (await user2_client.get("/api/agents")).json()
    assert any(a["id"] == new_agent_id for a in still_there["items"])

    # 9. user2 forks — agent id + sessions preserved, now editable.
    fork = await user2_client.post(f"/api/catalog/imports/{new_agent_id}/fork")
    assert fork.status_code == 200
    assert fork.json()["id"] == new_agent_id
    assert fork.json()["catalog_import_id"] is None
    assert fork.json()["linked_catalog_agent_id"] is None

    # Editing now works.
    edit = await user2_client.put(f"/api/agents/{new_agent_id}", json={
        "name": "Forked Mine",
        "slug": "e2e-agent",
        "instruction": "custom",
        "model_config": _MODEL_CONFIG,
    })
    assert edit.status_code == 200, edit.text

    # 10. user1 (as owner) restores the unpublished catalog to a new working copy.
    restore = await user1_client.post(
        f"/api/catalog/agents/{mine['id']}/restore"
    )
    assert restore.status_code == 200
    working_copy_id = restore.json()["id"]
    assert restore.json()["linked_catalog_agent_id"] == mine["id"]
    assert restore.json()["catalog_import_id"] is None

    # Visibility is NOT auto-restored — still unpublished until owner
    # publishes again.
    mine_after = (await user1_client.get("/api/catalog/mine")).json()
    entry = next(i for i in mine_after["items"] if i["id"] == mine["id"])
    assert entry["is_published"] is False

    # 11. publish from the restored working copy brings it back live.
    await user1_client.post(
        f"/api/agents/{working_copy_id}/publish",
        json={"category": "coding", "release_notes": "revived"},
    )
    live = (await user2_client.get("/api/catalog")).json()
    assert live["total"] == 1
