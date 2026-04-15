"""EKS Native WorkspaceStore — EFS CSI dynamic provisioning.

Per-agent workspace uses a PVC against a pre-provisioned StorageClass backed
by the EFS CSI driver in access-point mode (`provisioningMode: efs-ap`). The
CSI driver creates an EFS access point per PVC, scoped to uid/gid 1000.

Ops pre-provision:
  - One EFS file system for agent workspaces
  - A StorageClass `{EFS_STORAGE_CLASS}` pointing at that FS in `efs-ap` mode
  - One PVC `{EFS_SHARED_WORKSPACE_PVC}` (ReadWriteMany) in the `agents`
    namespace for the cross-agent shared /workspace-shared volume
"""

from __future__ import annotations

import logging

import httpx

from aviary_shared.naming import (
    AGENTS_NAMESPACE,
    LABEL_AGENT_ID,
    PVC_SIZE,
    RUNTIME_PORT,
    agent_pvc_name,
    agent_service_name,
)

from app.backends._common.k8s_client import k8s_apply
from app.backends.protocol import WorkspaceRef, WorkspaceStore
from app.config import settings

logger = logging.getLogger(__name__)

SHARED_WORKSPACE_VOLUME = "shared-workspace"
WORKSPACE_VOLUME = "agent-workspace"


class EKSWorkspaceStore(WorkspaceStore):
    async def ensure_agent_workspace(self, agent_id: str) -> WorkspaceRef:
        pvc = agent_pvc_name(agent_id)
        await k8s_apply(
            "POST",
            f"/api/v1/namespaces/{AGENTS_NAMESPACE}/persistentvolumeclaims",
            {
                "apiVersion": "v1",
                "kind": "PersistentVolumeClaim",
                "metadata": {
                    "name": pvc,
                    "namespace": AGENTS_NAMESPACE,
                    "labels": {LABEL_AGENT_ID: agent_id},
                },
                "spec": {
                    "accessModes": ["ReadWriteMany"],
                    "storageClassName": settings.efs_storage_class,
                    "resources": {"requests": {"storage": PVC_SIZE}},
                },
            },
        )
        return WorkspaceRef(
            volume={
                "name": WORKSPACE_VOLUME,
                "persistentVolumeClaim": {"claimName": pvc},
            },
            volume_mount={"name": WORKSPACE_VOLUME, "mountPath": "/workspace"},
        )

    async def delete_agent_workspace(self, agent_id: str) -> None:
        """Delete the PVC. The EFS CSI driver deletes the access point on PV release."""
        pvc = agent_pvc_name(agent_id)
        await k8s_apply(
            "DELETE",
            f"/api/v1/namespaces/{AGENTS_NAMESPACE}/persistentvolumeclaims/{pvc}",
        )

    async def cleanup_session_workspace(self, agent_id: str, session_id: str) -> None:
        svc = agent_service_name(agent_id)
        path = (
            f"/api/v1/namespaces/{AGENTS_NAMESPACE}/services/"
            f"{svc}:{RUNTIME_PORT}/proxy/sessions/{session_id}/workspace"
        )
        try:
            await k8s_apply("DELETE", path)
        except httpx.HTTPError:
            logger.info(
                "Session workspace cleanup skipped for %s/%s (pod not reachable)",
                agent_id, session_id,
            )


def shared_workspace_volume() -> dict:
    return {
        "name": SHARED_WORKSPACE_VOLUME,
        "persistentVolumeClaim": {"claimName": settings.efs_shared_workspace_pvc},
    }


def shared_workspace_mount() -> dict:
    return {"name": SHARED_WORKSPACE_VOLUME, "mountPath": "/workspace-shared"}
