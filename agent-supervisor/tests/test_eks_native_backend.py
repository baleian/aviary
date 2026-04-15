"""EKS Native backend — Protocol compliance and AWS-specific resource shape."""

from __future__ import annotations

from unittest.mock import AsyncMock, patch

import httpx
import pytest

from app.backends.eks_native.backend import EKSNativeBackend
from app.backends.eks_native.identity import EKSIdentityBinder
from app.backends.eks_native.manifests import build_deployment_manifest
from app.backends.eks_native.workspace import EKSWorkspaceStore
from app.backends.protocol import AgentSpec
from app.config import settings


def _spec(agent_id: str = "agent-test-1") -> AgentSpec:
    return AgentSpec(
        agent_id=agent_id,
        owner_id="owner-1",
        image="123456789.dkr.ecr.us-east-1.amazonaws.com/aviary-runtime:latest",
        sa_name="agent-default-sa",
        min_pods=0,
        max_pods=3,
    )


@pytest.fixture
def k8s_apply():
    with patch("app.backends.eks_native.workspace.k8s_apply", new_callable=AsyncMock) as w, \
         patch("app.backends.eks_native.identity.k8s_apply", new_callable=AsyncMock) as i, \
         patch("app.backends.eks_native.backend.k8s_apply", new_callable=AsyncMock) as b, \
         patch("app.backends.eks_native.backend.apply_or_replace", new_callable=AsyncMock) as br, \
         patch("app.backends.eks_native.identity.apply_or_replace", new_callable=AsyncMock) as ir:
        yield {
            "workspace": w, "identity": i, "backend": b,
            "backend_replace": br, "identity_replace": ir,
        }


@pytest.fixture
def with_baseline_sgs(monkeypatch):
    monkeypatch.setattr(settings, "eks_baseline_security_group_ids", "sg-baseline1,sg-baseline2")


@pytest.mark.asyncio
async def test_register_agent_creates_sa_pvc_deployment_service_and_scaledobject(k8s_apply):
    backend = EKSNativeBackend()
    await backend.register_agent(_spec())

    # SA POST via identity, PVC POST via workspace
    assert k8s_apply["identity"].await_count >= 1
    assert k8s_apply["workspace"].await_count >= 1

    # Deployment + Service + ScaledObject via apply_or_replace (3 calls)
    assert k8s_apply["backend_replace"].await_count == 3


@pytest.mark.asyncio
async def test_register_agent_applies_baseline_sg_policy(k8s_apply, with_baseline_sgs):
    backend = EKSNativeBackend()
    await backend.register_agent(_spec())

    k8s_apply["identity_replace"].assert_awaited()
    manifest = k8s_apply["identity_replace"].await_args.args[2]
    assert manifest["kind"] == "SecurityGroupPolicy"
    assert manifest["spec"]["securityGroups"]["groupIds"] == ["sg-baseline1", "sg-baseline2"]


@pytest.mark.asyncio
async def test_unregister_deletes_all_resources(k8s_apply):
    backend = EKSNativeBackend()
    await backend.unregister_agent("agent-test-1")

    # SGP DELETE via identity module
    assert any(c.args[0] == "DELETE" for c in k8s_apply["identity"].await_args_list)

    # ScaledObject + Deployment + Service via backend module
    backend_deletes = [c for c in k8s_apply["backend"].await_args_list if c.args[0] == "DELETE"]
    assert len(backend_deletes) == 3

    # PVC DELETE via workspace (no PV in EKS — CSI dynamic provisioning)
    ws_deletes = [c for c in k8s_apply["workspace"].await_args_list if c.args[0] == "DELETE"]
    assert len(ws_deletes) == 1
    assert "persistentvolumeclaims" in ws_deletes[0].args[1]


@pytest.mark.asyncio
async def test_workspace_creates_efs_pvc_without_explicit_pv(k8s_apply):
    store = EKSWorkspaceStore()
    ref = await store.ensure_agent_workspace("agent-test-1")

    # Only one call: PVC POST. No PV — CSI dynamic provisioning handles it.
    post_calls = [c for c in k8s_apply["workspace"].await_args_list if c.args[0] == "POST"]
    assert len(post_calls) == 1
    pvc_body = post_calls[0].args[2]
    assert pvc_body["kind"] == "PersistentVolumeClaim"
    assert pvc_body["spec"]["storageClassName"] == settings.efs_storage_class
    assert "ReadWriteMany" in pvc_body["spec"]["accessModes"]

    # Volume ref shape matches protocol
    assert ref.volume["persistentVolumeClaim"]["claimName"].endswith("-workspace")
    assert ref.volume_mount["mountPath"] == "/workspace"


@pytest.mark.asyncio
async def test_bind_identity_merges_baseline_and_extras(k8s_apply, with_baseline_sgs):
    binder = EKSIdentityBinder()
    await binder.bind_identity("agent-test-1", "agent-default-sa", ["sg-extra1"])

    k8s_apply["identity_replace"].assert_awaited_once()
    manifest = k8s_apply["identity_replace"].await_args.args[2]
    group_ids = manifest["spec"]["securityGroups"]["groupIds"]
    assert group_ids == ["sg-baseline1", "sg-baseline2", "sg-extra1"]
    assert manifest["spec"]["podSelector"]["matchLabels"]["aviary/agent-id"] == "agent-test-1"


@pytest.mark.asyncio
async def test_bind_identity_deduplicates_overlapping_sgs(k8s_apply, with_baseline_sgs):
    binder = EKSIdentityBinder()
    await binder.bind_identity("agent-test-1", "agent-default-sa", ["sg-baseline1", "sg-extra1"])

    manifest = k8s_apply["identity_replace"].await_args.args[2]
    assert manifest["spec"]["securityGroups"]["groupIds"] == [
        "sg-baseline1", "sg-baseline2", "sg-extra1",
    ]


@pytest.mark.asyncio
async def test_bind_identity_empty_refs_with_no_baseline_unbinds(k8s_apply):
    binder = EKSIdentityBinder()
    await binder.bind_identity("agent-test-1", "agent-default-sa", [])

    deletes = [c for c in k8s_apply["identity"].await_args_list if c.args[0] == "DELETE"]
    assert len(deletes) == 1
    assert "securitygrouppolicies" in deletes[0].args[1]


@pytest.mark.asyncio
async def test_bind_identity_unbind_swallows_404(k8s_apply):
    req = httpx.Request("DELETE", "http://k8s/test")
    k8s_apply["identity"].side_effect = httpx.HTTPStatusError(
        "Not Found", request=req, response=httpx.Response(404, request=req),
    )
    binder = EKSIdentityBinder()
    await binder.unbind_identity("agent-test-1")  # must not raise


@pytest.mark.asyncio
async def test_ensure_service_account_applies_irsa_annotation(k8s_apply, monkeypatch):
    monkeypatch.setattr(settings, "eks_irsa_role_arn", "arn:aws:iam::123:role/agent")
    binder = EKSIdentityBinder()
    await binder.ensure_service_account("agent-default-sa")

    body = k8s_apply["identity"].await_args.args[2]
    assert body["metadata"]["annotations"]["eks.amazonaws.com/role-arn"] == (
        "arn:aws:iam::123:role/agent"
    )
    assert body["automountServiceAccountToken"] is True


def test_deployment_manifest_uses_ifnotpresent_and_has_no_hostaliases(monkeypatch):
    monkeypatch.setattr(settings, "agent_image_pull_policy", "IfNotPresent")
    from app.backends.eks_native.workspace import WORKSPACE_VOLUME

    workspace_ref = type("R", (), {
        "volume": {"name": WORKSPACE_VOLUME, "persistentVolumeClaim": {"claimName": "x"}},
        "volume_mount": {"name": WORKSPACE_VOLUME, "mountPath": "/workspace"},
    })()
    manifest = build_deployment_manifest(_spec(), workspace_ref)

    pod_spec = manifest["spec"]["template"]["spec"]
    assert "hostAliases" not in pod_spec
    assert "initContainers" not in pod_spec  # EFS fsGroup handles ownership
    container = pod_spec["containers"][0]
    assert container["imagePullPolicy"] == "IfNotPresent"
    assert pod_spec["securityContext"]["fsGroup"] == 1000


@pytest.mark.asyncio
async def test_resolve_endpoint_shape():
    backend = EKSNativeBackend()
    ep = await backend.resolve_endpoint("agent-test-1")
    assert "/namespaces/agents/services/agent-agent-test-1-svc:3000/proxy" in ep


@pytest.mark.asyncio
async def test_scale_patches_replicas(k8s_apply):
    backend = EKSNativeBackend()
    await backend.scale("agent-test-1", 3)
    call = k8s_apply["backend"].await_args
    assert call.args[0] == "PATCH"
    assert call.args[2] == {"spec": {"replicas": 3}}


@pytest.mark.asyncio
async def test_ensure_active_scales_when_idle(k8s_apply):
    k8s_apply["backend"].return_value = {"spec": {"replicas": 0}, "status": {}}
    backend = EKSNativeBackend()
    await backend.ensure_active("agent-test-1")

    methods = [c.args[0] for c in k8s_apply["backend"].await_args_list]
    assert "GET" in methods
    assert "PATCH" in methods


@pytest.mark.asyncio
async def test_get_status_returns_zero_on_404(k8s_apply):
    req = httpx.Request("GET", "http://k8s/test")
    k8s_apply["backend"].side_effect = httpx.HTTPStatusError(
        "Not Found", request=req, response=httpx.Response(404, request=req),
    )
    backend = EKSNativeBackend()
    status = await backend.get_status("agent-test-1")
    assert status.exists is False
