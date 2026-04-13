"""Network policy extraction from `policies.policy_rules` JSONB.

Structure:
    policy_rules = {"network": {"extra_networks": [...], "subnets": [...]}}

Both fields are admin-set raw infrastructure names (Docker network names
locally, Security Group / Subnet IDs on Fargate).
"""

from __future__ import annotations


def extract_network_policy(policy) -> dict | None:
    if not policy or not policy.policy_rules:
        return None
    return (policy.policy_rules or {}).get("network") or None
