"""EKS Native IdentityBinder — ServiceAccount (optional IRSA) + SecurityGroupPolicy.

AWS VPC CNI natively supports per-pod security groups via the
`SecurityGroupPolicy` CRD (`vpcresources.k8s.aws/v1beta1`). The CNI merges all
matching policies' SG lists and attaches them to the pod ENI at admission.

`sg_refs` are AWS security group IDs (`sg-xxxxxxxx`). They are unioned with
the cluster-wide baseline SGs from `settings.eks_baseline_sg_list`.

IRSA: if `settings.eks_irsa_role_arn` is set, the SA is annotated with
`eks.amazonaws.com/role-arn` so pods get temporary AWS credentials for that
role via the pod identity webhook.
"""

from __future__ import annotations

import logging

import httpx

from aviary_shared.naming import (
    AGENTS_NAMESPACE,
    LABEL_AGENT_ID,
)

from app.backends._common.k8s_client import apply_or_replace, k8s_apply
from app.backends.protocol import IdentityBinder
from app.config import settings

logger = logging.getLogger(__name__)

_SGP_GROUP = "/apis/vpcresources.k8s.aws/v1beta1"


def _sgp_name(agent_id: str) -> str:
    return f"agent-{agent_id}-sgp"


class EKSIdentityBinder(IdentityBinder):
    async def ensure_service_account(self, sa_name: str) -> None:
        manifest: dict = {
            "apiVersion": "v1",
            "kind": "ServiceAccount",
            "metadata": {"name": sa_name, "namespace": AGENTS_NAMESPACE},
            "automountServiceAccountToken": bool(settings.eks_irsa_role_arn),
        }
        if settings.eks_irsa_role_arn:
            manifest["metadata"]["annotations"] = {
                "eks.amazonaws.com/role-arn": settings.eks_irsa_role_arn,
            }
        await k8s_apply(
            "POST",
            f"/api/v1/namespaces/{AGENTS_NAMESPACE}/serviceaccounts",
            manifest,
        )

    async def bind_identity(self, agent_id: str, sa_name: str, sg_refs: list[str]) -> None:
        """Apply a SecurityGroupPolicy selecting this agent's pods.

        Baseline SGs from config are always included so agents retain cluster
        egress to platform services even when `sg_refs` is empty.
        """
        merged = list(dict.fromkeys([*settings.eks_baseline_sg_list, *sg_refs]))
        if not merged:
            await self.unbind_identity(agent_id)
            return

        name = _sgp_name(agent_id)
        manifest = {
            "apiVersion": "vpcresources.k8s.aws/v1beta1",
            "kind": "SecurityGroupPolicy",
            "metadata": {
                "name": name,
                "namespace": AGENTS_NAMESPACE,
                "labels": {LABEL_AGENT_ID: agent_id},
            },
            "spec": {
                "podSelector": {"matchLabels": {LABEL_AGENT_ID: agent_id}},
                "securityGroups": {"groupIds": merged},
            },
        }
        await apply_or_replace(
            f"{_SGP_GROUP}/namespaces/{AGENTS_NAMESPACE}/securitygrouppolicies",
            name,
            manifest,
        )
        logger.info("Bound SGs %s to agent %s", merged, agent_id)

    async def unbind_identity(self, agent_id: str) -> None:
        name = _sgp_name(agent_id)
        try:
            await k8s_apply(
                "DELETE",
                f"{_SGP_GROUP}/namespaces/{AGENTS_NAMESPACE}/securitygrouppolicies/{name}",
            )
        except httpx.HTTPStatusError as e:
            if e.response.status_code != 404:
                raise
