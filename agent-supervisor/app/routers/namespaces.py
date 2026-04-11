"""K8s-specific namespace endpoints used by the admin console."""

import logging

import httpx
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from aviary_shared.naming import agent_namespace

from app.k8s import k8s_apply
from app.provisioning import apply_network_policy, provision_namespace

logger = logging.getLogger(__name__)

router = APIRouter()


class CreateNamespaceRequest(BaseModel):
    agent_id: str
    owner_id: str
    policy: dict


@router.post("/namespaces")
async def create_namespace(body: CreateNamespaceRequest):
    """Provision all K8s resources for a new agent."""
    try:
        ns_name = await provision_namespace(body.agent_id, body.owner_id, body.policy)
    except httpx.HTTPError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
    return {"namespace": ns_name}


class UpdateNetworkPolicyRequest(BaseModel):
    policy: dict


@router.put("/namespaces/{namespace}/network-policy")
async def update_network_policy(namespace: str, body: UpdateNetworkPolicyRequest):
    """Update the NetworkPolicy for an agent namespace."""
    try:
        await apply_network_policy(namespace, body.policy)
        logger.info("Updated NetworkPolicy in namespace %s", namespace)
        return {"ok": True}
    except httpx.HTTPError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.delete("/namespaces/{agent_id}")
async def delete_namespace(agent_id: str):
    """Delete the entire agent namespace and all its resources."""
    ns_name = agent_namespace(agent_id)
    try:
        await k8s_apply("DELETE", f"/api/v1/namespaces/{ns_name}")
        logger.info("Deleted namespace %s", ns_name)
        return {"ok": True}
    except httpx.HTTPError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
