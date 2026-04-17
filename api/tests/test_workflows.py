import pytest
from httpx import AsyncClient

_MODEL_CONFIG = {"backend": "dummy-backend", "model": "dummy-model"}


async def _create(client: AsyncClient, slug: str) -> dict:
    resp = await client.post("/api/workflows", json={
        "name": f"Workflow {slug}",
        "slug": slug,
        "model_config": _MODEL_CONFIG,
    })
    assert resp.status_code == 201, resp.text
    return resp.json()


@pytest.mark.asyncio
async def test_create_lists_with_current_version_null(user1_client: AsyncClient):
    wf = await _create(user1_client, "wf-fresh")
    assert wf["status"] == "draft"
    assert wf["current_version"] is None


@pytest.mark.asyncio
async def test_deploy_creates_version_and_flips_status(user1_client: AsyncClient):
    wf = await _create(user1_client, "wf-deploy")
    wf_id = wf["id"]

    resp = await user1_client.post(f"/api/workflows/{wf_id}/deploy")
    assert resp.status_code == 200, resp.text
    v1 = resp.json()
    assert v1["version"] == 1
    assert v1["workflow_id"] == wf_id

    resp = await user1_client.post(f"/api/workflows/{wf_id}/deploy")
    v2 = resp.json()
    assert v2["version"] == 2

    # Workflow now reports deployed + current_version = 2
    resp = await user1_client.get(f"/api/workflows/{wf_id}")
    data = resp.json()
    assert data["status"] == "deployed"
    assert data["current_version"] == 2

    # Versions listed newest-first
    resp = await user1_client.get(f"/api/workflows/{wf_id}/versions")
    versions = resp.json()
    assert [v["version"] for v in versions] == [2, 1]


@pytest.mark.asyncio
async def test_edit_flips_status_back_to_draft(user1_client: AsyncClient):
    wf = await _create(user1_client, "wf-edit")
    wf_id = wf["id"]
    await user1_client.post(f"/api/workflows/{wf_id}/deploy")

    resp = await user1_client.post(f"/api/workflows/{wf_id}/edit")
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "draft"
    # Edit must not erase version history
    assert data["current_version"] == 1


@pytest.mark.asyncio
async def test_non_owner_cannot_deploy_or_list_versions(
    user1_client: AsyncClient, user2_client: AsyncClient
):
    wf = await _create(user1_client, "wf-acl")
    wf_id = wf["id"]

    resp = await user2_client.post(f"/api/workflows/{wf_id}/deploy")
    assert resp.status_code == 403

    resp = await user2_client.get(f"/api/workflows/{wf_id}/versions")
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_versions_empty_for_never_deployed(user1_client: AsyncClient):
    wf = await _create(user1_client, "wf-novers")
    wf_id = wf["id"]
    resp = await user1_client.get(f"/api/workflows/{wf_id}/versions")
    assert resp.status_code == 200
    assert resp.json() == []
