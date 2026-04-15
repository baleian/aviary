"""Deployment + Service manifest builders for the EKS Fargate backend.

Fargate-specific deviations from EKS Native:
  - Resource requests == limits, rounded up to a valid Fargate tier.
  - `eks.amazonaws.com/compute-type: fargate` nodeSelector forces Fargate
    scheduling even if mixed managed-node groups exist in the cluster.
  - Optional `eks.amazonaws.com/fargate-ephemeral-storage` annotation when
    the default 20 GiB ephemeral disk isn't enough.
  - No init container chown — EFS access points enforce uid/gid.
"""

from __future__ import annotations

from aviary_shared.naming import (
    AGENTS_NAMESPACE,
    LABEL_AGENT_ID,
    LABEL_ROLE,
    ROLE_RUNTIME,
    RUNTIME_PORT,
    agent_deployment_name,
    agent_service_name,
)

from app.backends.eks_fargate.sizing import fit_fargate_tier
from app.backends.eks_native.workspace import (
    shared_workspace_mount,
    shared_workspace_volume,
)
from app.backends.protocol import AgentSpec, WorkspaceRef
from app.config import settings


def build_deployment_manifest(spec: AgentSpec, workspace: WorkspaceRef) -> dict:
    cpu, memory = fit_fargate_tier(spec.cpu_limit, spec.memory_limit)
    resources = {
        # Requests must equal limits on Fargate for predictable scheduling.
        "requests": {"cpu": cpu, "memory": memory},
        "limits": {"cpu": cpu, "memory": memory},
    }

    pod_annotations: dict = {}
    if settings.fargate_ephemeral_storage_gib:
        pod_annotations["eks.amazonaws.com/fargate-ephemeral-storage"] = (
            f"{settings.fargate_ephemeral_storage_gib}Gi"
        )

    runtime_env = [
        {"name": "AGENT_ID", "value": spec.agent_id},
        {"name": "INFERENCE_ROUTER_URL", "value": settings.inference_router_url},
        {"name": "MCP_GATEWAY_URL", "value": settings.mcp_gateway_url},
        {"name": "LITELLM_API_KEY", "value": settings.litellm_api_key},
        {"name": "HOME", "value": "/tmp"},
        {"name": "AVIARY_API_URL", "value": settings.aviary_api_url},
        {"name": "AVIARY_INTERNAL_API_KEY", "value": settings.internal_api_key},
    ] + [{"name": k, "value": v} for k, v in spec.env.items()]

    pod_metadata: dict = {
        "labels": {
            LABEL_AGENT_ID: spec.agent_id,
            LABEL_ROLE: ROLE_RUNTIME,
        },
    }
    if pod_annotations:
        pod_metadata["annotations"] = pod_annotations

    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": agent_deployment_name(spec.agent_id),
            "namespace": AGENTS_NAMESPACE,
            "labels": {
                LABEL_AGENT_ID: spec.agent_id,
                LABEL_ROLE: ROLE_RUNTIME,
            },
        },
        "spec": {
            "replicas": spec.min_pods,
            "selector": {"matchLabels": {LABEL_AGENT_ID: spec.agent_id}},
            "template": {
                "metadata": pod_metadata,
                "spec": {
                    "serviceAccountName": spec.sa_name,
                    "terminationGracePeriodSeconds": 600,
                    "nodeSelector": {"eks.amazonaws.com/compute-type": "fargate"},
                    "securityContext": {
                        "runAsUser": 1000,
                        "runAsGroup": 1000,
                        "fsGroup": 1000,
                        "fsGroupChangePolicy": "OnRootMismatch",
                    },
                    "containers": [
                        {
                            "name": ROLE_RUNTIME,
                            "image": spec.image,
                            "imagePullPolicy": settings.agent_image_pull_policy,
                            "ports": [{"containerPort": RUNTIME_PORT}],
                            "env": runtime_env,
                            "volumeMounts": [
                                workspace.volume_mount,
                                shared_workspace_mount(),
                            ],
                            "resources": resources,
                            "livenessProbe": {
                                "httpGet": {"path": "/health", "port": RUNTIME_PORT},
                                "initialDelaySeconds": 5,
                                "periodSeconds": 30,
                            },
                            "readinessProbe": {
                                "httpGet": {"path": "/ready", "port": RUNTIME_PORT},
                                "initialDelaySeconds": 3,
                            },
                        }
                    ],
                    "volumes": [workspace.volume, shared_workspace_volume()],
                },
            },
        },
    }


def build_service_manifest(agent_id: str) -> dict:
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {
            "name": agent_service_name(agent_id),
            "namespace": AGENTS_NAMESPACE,
            "labels": {LABEL_AGENT_ID: agent_id},
        },
        "spec": {
            "selector": {LABEL_AGENT_ID: agent_id},
            "ports": [{"port": RUNTIME_PORT, "targetPort": RUNTIME_PORT, "protocol": "TCP"}],
        },
    }
