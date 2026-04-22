"""Catalog test coverage — the checklist items plan §2.8 called out."""

import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


async def _create(client, slug, **extra):
    payload = {
        "name": extra.get("name", slug),
        "slug": slug,
        "description": extra.get("description"),
        "instruction": extra.get("instruction", "hi"),
        "model_config": _MODEL_CONFIG,
    }
    resp = await client.post("/api/agents", json=payload)
    assert resp.status_code == 201, resp.text
    return resp.json()["id"]


async def _publish(client, agent_id, category="coding", notes=None):
    resp = await client.post(
        f"/api/agents/{agent_id}/publish",
        json={"category": category, "release_notes": notes},
    )
    assert resp.status_code == 200, resp.text
    return resp.json()


# ── 1. catalog_agent_independence ────────────────────────────────────────

@pytest.mark.asyncio
async def test_catalog_agent_survives_private_deletion(user1_client: AsyncClient):
    agent_id = await _create(user1_client, "survivor")
    await _publish(user1_client, agent_id)
    mine = (await user1_client.get("/api/catalog/mine")).json()
    catalog_id = mine["items"][0]["id"]

    # Delete the private working copy.
    await user1_client.delete(f"/api/agents/{agent_id}")

    # CatalogAgent still manageable.
    mine = (await user1_client.get("/api/catalog/mine")).json()
    assert mine["total"] == 1
    assert mine["items"][0]["id"] == catalog_id
    assert mine["items"][0]["linked_agent_id"] is None

    # Unpublish still works.
    resp = await user1_client.post(
        f"/api/catalog/agents/{catalog_id}/unpublish"
    )
    assert resp.status_code == 200


# ── 2. drift across all fields ───────────────────────────────────────────

@pytest.mark.parametrize(
    "slug_tag,field,mutation",
    [
        ("name", "name", {"name": "Different"}),
        ("description", "description", {"description": "Different too"}),
        ("icon", "icon", {"icon": "🦊"}),
        ("instruction", "instruction", {"instruction": "New instructions"}),
        (
            "model-cfg",
            "model_config_json",
            {"model_config": {**_MODEL_CONFIG, "max_output_tokens": 512}},
        ),
        ("tools", "tools", {"tools": ["read_file"]}),
    ],
)
@pytest.mark.asyncio
async def test_drift_flips_on_each_field(
    user1_client: AsyncClient, slug_tag: str, field: str, mutation: dict
):
    slug = f"drift-{slug_tag}"
    agent_id = await _create(user1_client, slug)
    await _publish(user1_client, agent_id)
    body = {
        "name": slug,
        "slug": slug,
        "instruction": "hi",
        "model_config": _MODEL_CONFIG,
        **mutation,
    }
    await user1_client.put(f"/api/agents/{agent_id}", json=body)
    drift = (await user1_client.get(f"/api/agents/{agent_id}/drift")).json()
    assert drift["is_dirty"] is True
    assert field in drift["changed_fields"]


# ── 3. catalog search (trigram + jsonb) ──────────────────────────────────

@pytest.mark.asyncio
async def test_catalog_search_ilike_name(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    # Use longer, distinctive names — pg_trgm requires ≥3-char patterns
    # for the GIN trigram operator to engage on very short prefixes.
    await _publish(user1_client, await _create(user1_client, "unicorn-agent", name="Unicorn Planner"))
    await _publish(user1_client, await _create(user1_client, "mundane-agent", name="Ordinary Runner"))
    resp = (await user2_client.get("/api/catalog?q=unicorn")).json()
    assert resp["total"] == 1
    assert resp["items"][0]["name"] == "Unicorn Planner"


# ── 4. mention_scope ─────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_mention_scope_covers_private_and_imports(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    # user1 publishes.
    await _publish(user1_client, await _create(user1_client, "mention-src"))
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    # user2 imports it + creates a Private agent.
    await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    await _create(user2_client, "user2-private")

    agents = (await user2_client.get("/api/agents")).json()
    slugs = sorted(a["slug"] for a in agents["items"])
    assert "mention-src" in slugs
    assert "user2-private" in slugs


# ── 5. editor_viewer_permission ──────────────────────────────────────────

@pytest.mark.asyncio
async def test_is_catalog_editor_flag(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    agent_id = await _create(user1_client, "editorcheck")
    await _publish(user1_client, agent_id)
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]

    mine = (await user1_client.get(f"/api/agents/{agent_id}")).json()
    assert mine["is_catalog_editor"] is True

    imp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    imported_id = imp.json()["created"][0]["id"]
    imported = (await user2_client.get(f"/api/agents/{imported_id}")).json()
    assert imported["is_catalog_editor"] is False


@pytest.mark.asyncio
async def test_viewer_cannot_publish_or_rollback(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    agent_id = await _create(user1_client, "viewer-block")
    await _publish(user1_client, agent_id)
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    imp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    imported_id = imp.json()["created"][0]["id"]

    # Viewer cannot publish the import.
    r = await user2_client.post(
        f"/api/agents/{imported_id}/publish", json={"category": "coding"}
    )
    assert r.status_code == 409

    # Viewer cannot rollback another user's catalog.
    r = await user2_client.post(
        f"/api/catalog/agents/{cid}/rollback",
        json={"version_id": "00000000-0000-0000-0000-000000000000"},
    )
    assert r.status_code in (403, 404)


# ── 6. join_display — imported agent display uses catalog snapshot ──────

@pytest.mark.asyncio
async def test_imported_agent_display_reflects_catalog_updates(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    agent_id = await _create(
        user1_client, "jd", name="Original", description="v1 desc"
    )
    await _publish(user1_client, agent_id)
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    imp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    imported_id = imp.json()["created"][0]["id"]

    # user1 edits + publishes v2.
    await user1_client.put(f"/api/agents/{agent_id}", json={
        "name": "Renamed",
        "slug": "jd",
        "description": "v2 desc",
        "instruction": "hi",
        "model_config": _MODEL_CONFIG,
    })
    await _publish(user1_client, agent_id)

    # user2's imported agent detail reflects the new snapshot (latest mode).
    fresh = (await user2_client.get(f"/api/agents/{imported_id}")).json()
    assert fresh["name"] == "Renamed"
    assert fresh["description"] == "v2 desc"


# ── 7. privacy — unpublished not in browse, facet no-leak ───────────────

@pytest.mark.asyncio
async def test_privacy_unpublished_hidden_from_browse(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    aid = await _create(user1_client, "private-agent")
    await _publish(user1_client, aid)
    cid = (await user1_client.get("/api/catalog/mine")).json()["items"][0]["id"]
    await user1_client.post(f"/api/catalog/agents/{cid}/unpublish")
    resp = (await user2_client.get("/api/catalog")).json()
    assert resp["total"] == 0
    facets = (await user2_client.get("/api/catalog/facets")).json()
    assert all(c["count"] == 0 or c["name"] == "" for c in facets["categories"])


# ── 8. delete catalog blocks on active imports ─────────────────────────

@pytest.mark.asyncio
async def test_delete_catalog_blocks_if_imports_exist(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    aid = await _create(user1_client, "dc-block")
    await _publish(user1_client, aid)
    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    r = await user1_client.delete(f"/api/catalog/agents/{cid}")
    assert r.status_code == 409


# ── 9. version-level unpublish ──────────────────────────────────────────

@pytest.mark.asyncio
async def test_version_unpublish_falls_back(user1_client: AsyncClient):
    aid = await _create(user1_client, "vu")
    v1 = await _publish(user1_client, aid)
    # Edit and publish v2.
    await user1_client.put(f"/api/agents/{aid}", json={
        "name": "vu", "slug": "vu", "instruction": "v2",
        "model_config": _MODEL_CONFIG,
    })
    v2 = await _publish(user1_client, aid)

    # Unpublish v2 → fallback should be v1.
    r = await user1_client.post(f"/api/catalog/versions/{v2['id']}/unpublish")
    assert r.status_code == 200
    mine = (await user1_client.get("/api/catalog/mine")).json()["items"][0]
    assert mine["current_version_number"] == 1
    # `_ = v1` silences the linter for the unused local.
    assert v1["id"] != v2["id"]


# ── 10. import runtime resolve — pin ↔ latest toggle ────────────────────

@pytest.mark.asyncio
async def test_import_runtime_resolve_pin_toggle(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    aid = await _create(user1_client, "rr", instruction="v1")
    v1 = await _publish(user1_client, aid)
    await user1_client.put(f"/api/agents/{aid}", json={
        "name": "rr", "slug": "rr", "instruction": "v2",
        "model_config": _MODEL_CONFIG,
    })
    await _publish(user1_client, aid)

    cid = (await user2_client.get("/api/catalog")).json()["items"][0]["id"]
    imp = await user2_client.post("/api/catalog/imports", json={
        "items": [{"catalog_agent_id": cid}],
    })
    imported_id = imp.json()["created"][0]["id"]

    # Latest mode — should show v2 instruction.
    latest = (await user2_client.get(f"/api/agents/{imported_id}")).json()
    assert latest["instruction"] == "v2"
    assert latest["pinned_version_id"] is None

    # Pin to v1 — should show v1 instruction.
    await user2_client.patch(
        f"/api/catalog/imports/{imported_id}",
        json={"pinned_version_id": v1["id"]},
    )
    pinned = (await user2_client.get(f"/api/agents/{imported_id}")).json()
    assert pinned["instruction"] == "v1"
    assert pinned["pinned_version_id"] == v1["id"]
