"""EKS Fargate backend — Protocol compliance, tier sizing, and Fargate-specific pod shape."""

from __future__ import annotations

from unittest.mock import AsyncMock, patch

import httpx
import pytest

from app.backends.eks_fargate.backend import EKSFargateBackend
from app.backends.eks_fargate.manifests import build_deployment_manifest
from app.backends.eks_fargate.sizing import fit_fargate_tier
from app.backends.protocol import AgentSpec
from app.config import settings


def _spec(cpu: str = "4", memory: str = "8Gi") -> AgentSpec:
    return AgentSpec(
        agent_id="agent-test-1",
        owner_id="owner-1",
        image="123456789.dkr.ecr.us-east-1.amazonaws.com/aviary-runtime:latest",
        sa_name="agent-default-sa",
        min_pods=0,
        max_pods=3,
        cpu_limit=cpu,
        memory_limit=memory,
    )


@pytest.fixture
def k8s_apply():
    # Fargate backend reuses eks_native workspace/identity modules — patch all points.
    with patch("app.backends.eks_native.workspace.k8s_apply", new_callable=AsyncMock) as w, \
         patch("app.backends.eks_native.identity.k8s_apply", new_callable=AsyncMock) as i, \
         patch("app.backends.eks_fargate.backend.k8s_apply", new_callable=AsyncMock) as b, \
         patch("app.backends.eks_fargate.backend.apply_or_replace", new_callable=AsyncMock) as br, \
         patch("app.backends.eks_native.identity.apply_or_replace", new_callable=AsyncMock) as ir:
        yield {
            "workspace": w, "identity": i, "backend": b,
            "backend_replace": br, "identity_replace": ir,
        }


def _workspace_ref():
    from app.backends.eks_native.workspace import WORKSPACE_VOLUME

    return type("R", (), {
        "volume": {"name": WORKSPACE_VOLUME, "persistentVolumeClaim": {"claimName": "x"}},
        "volume_mount": {"name": WORKSPACE_VOLUME, "mountPath": "/workspace"},
    })()


# ---- Tier rounding --------------------------------------------------------

@pytest.mark.parametrize("cpu,mem,want_cpu,want_mem", [
    # Exact matches pass through.
    ("1", "2Gi", "1", "2Gi"),
    ("2", "4Gi", "2", "4Gi"),
    # Memory-short of CPU tier → bumped up within same vCPU row.
    ("4", "4Gi", "4", "8Gi"),
    # Sub-vCPU requests.
    ("0.25", "0.5Gi", "0.25", "0.5Gi"),
    ("0.5", "3Gi", "0.5", "3Gi"),
    # Request CPU just above a tier → next tier up.
    ("0.3", "1Gi", "0.5", "1Gi"),
    # Millicpu notation.
    ("500m", "1Gi", "0.5", "1Gi"),
    # Mi notation.
    ("1", "2048Mi", "1", "2Gi"),
])
def test_fit_fargate_tier_rounds_up(cpu, mem, want_cpu, want_mem):
    assert fit_fargate_tier(cpu, mem) == (want_cpu, want_mem)


def test_fit_fargate_tier_rejects_oversized():
    with pytest.raises(ValueError):
        fit_fargate_tier("32", "200Gi")


# ---- Manifest shape -------------------------------------------------------

def test_deployment_manifest_normalizes_to_valid_fargate_tier():
    # 4 vCPU + 4Gi is NOT a valid Fargate combination (4 vCPU needs ≥8Gi).
    # Backend must bump memory up to 8Gi.
    manifest = build_deployment_manifest(_spec("4", "4Gi"), _workspace_ref())
    container = manifest["spec"]["template"]["spec"]["containers"][0]
    resources = container["resources"]
    assert resources["requests"] == {"cpu": "4", "memory": "8Gi"}
    assert resources["limits"] == resources["requests"]


def test_deployment_manifest_has_fargate_node_selector_and_no_host_features():
    manifest = build_deployment_manifest(_spec(), _workspace_ref())
    pod_spec = manifest["spec"]["template"]["spec"]

    assert pod_spec["nodeSelector"] == {"eks.amazonaws.com/compute-type": "fargate"}
    assert "hostAliases" not in pod_spec
    assert "initContainers" not in pod_spec  # no chown — EFS fsGroup handles it


def test_deployment_manifest_adds_ephemeral_storage_annotation_when_set(monkeypatch):
    monkeypatch.setattr(settings, "fargate_ephemeral_storage_gib", 100)
    manifest = build_deployment_manifest(_spec(), _workspace_ref())
    annotations = manifest["spec"]["template"]["metadata"]["annotations"]
    assert annotations["eks.amazonaws.com/fargate-ephemeral-storage"] == "100Gi"


def test_deployment_manifest_omits_ephemeral_storage_annotation_by_default():
    manifest = build_deployment_manifest(_spec(), _workspace_ref())
    pod_meta = manifest["spec"]["template"]["metadata"]
    assert "annotations" not in pod_meta


# ---- Backend lifecycle ----------------------------------------------------

@pytest.mark.asyncio
async def test_register_agent_creates_all_resources(k8s_apply):
    backend = EKSFargateBackend()
    await backend.register_agent(_spec())

    assert k8s_apply["identity"].await_count >= 1  # SA
    assert k8s_apply["workspace"].await_count >= 1  # PVC
    # Deployment + Service + ScaledObject
    assert k8s_apply["backend_replace"].await_count == 3


@pytest.mark.asyncio
async def test_unregister_deletes_all_resources(k8s_apply):
    backend = EKSFargateBackend()
    await backend.unregister_agent("agent-test-1")

    backend_deletes = [c for c in k8s_apply["backend"].await_args_list if c.args[0] == "DELETE"]
    assert len(backend_deletes) == 3  # SO + Deployment + Service

    ws_deletes = [c for c in k8s_apply["workspace"].await_args_list if c.args[0] == "DELETE"]
    assert len(ws_deletes) == 1  # PVC only (CSI handles PV/access point)


@pytest.mark.asyncio
async def test_ensure_active_scales_when_idle(k8s_apply):
    k8s_apply["backend"].return_value = {"spec": {"replicas": 0}, "status": {}}
    backend = EKSFargateBackend()
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
    backend = EKSFargateBackend()
    status = await backend.get_status("agent-test-1")
    assert status.exists is False


@pytest.mark.asyncio
async def test_resolve_endpoint_shape():
    backend = EKSFargateBackend()
    ep = await backend.resolve_endpoint("agent-test-1")
    assert "/namespaces/agents/services/agent-agent-test-1-svc:3000/proxy" in ep


@pytest.mark.asyncio
async def test_scale_patches_replicas(k8s_apply):
    backend = EKSFargateBackend()
    await backend.scale("agent-test-1", 3)
    call = k8s_apply["backend"].await_args
    assert call.args[0] == "PATCH"
    assert call.args[2] == {"spec": {"replicas": 3}}


@pytest.mark.asyncio
async def test_backend_exposes_shared_efs_workspace_and_sgp_identity():
    """Fargate reuses the EKS-native EFS workspace + SecurityGroupPolicy binder."""
    from app.backends.eks_native.identity import EKSIdentityBinder
    from app.backends.eks_native.workspace import EKSWorkspaceStore

    backend = EKSFargateBackend()
    assert isinstance(backend.workspace, EKSWorkspaceStore)
    assert isinstance(backend.identity, EKSIdentityBinder)
