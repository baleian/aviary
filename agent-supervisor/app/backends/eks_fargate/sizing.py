"""Fargate resource tier rounding.

Fargate schedules only at fixed (vCPU, memory) combinations. A pod whose
requests don't match a valid tier is rejected at admission. We round the
requested values UP to the smallest valid tier that satisfies both.

Tiers per AWS docs (as of 2024):
https://docs.aws.amazon.com/eks/latest/userguide/fargate-pod-configuration.html
"""

from __future__ import annotations

# (vCPU, sorted list of valid GiB) — must be ascending by vCPU.
FARGATE_TIERS: list[tuple[float, list[int | float]]] = [
    (0.25, [0.5, 1, 2]),
    (0.5, [1, 2, 3, 4]),
    (1.0, list(range(2, 9))),
    (2.0, list(range(4, 17))),
    (4.0, list(range(8, 31))),
    (8.0, list(range(16, 61, 4))),
    (16.0, list(range(32, 121, 8))),
]

_MAX_CPU = FARGATE_TIERS[-1][0]
_MAX_MEM = FARGATE_TIERS[-1][1][-1]


def _parse_cpu(value: str) -> float:
    """Accept '4', '4000m', '0.5'."""
    v = value.strip()
    if v.endswith("m"):
        return float(v[:-1]) / 1000.0
    return float(v)


def _parse_memory_gib(value: str) -> float:
    """Accept '4Gi', '4096Mi', '4G'. Returns GiB (power-of-two)."""
    v = value.strip()
    if v.endswith("Gi"):
        return float(v[:-2])
    if v.endswith("Mi"):
        return float(v[:-2]) / 1024.0
    if v.endswith("G"):
        return float(v[:-1]) * 1_000_000_000 / (1024 ** 3)
    if v.endswith("M"):
        return float(v[:-1]) * 1_000_000 / (1024 ** 3)
    raise ValueError(f"Unsupported memory suffix: {value!r}")


def fit_fargate_tier(cpu: str, memory: str) -> tuple[str, str]:
    """Round (cpu, memory) up to the smallest Fargate tier that satisfies both.

    Raises ValueError if the request exceeds the largest tier.
    """
    req_cpu = _parse_cpu(cpu)
    req_mem = _parse_memory_gib(memory)
    if req_cpu > _MAX_CPU or req_mem > _MAX_MEM:
        raise ValueError(
            f"Requested {cpu}/{memory} exceeds largest Fargate tier "
            f"({_MAX_CPU} vCPU / {_MAX_MEM} GiB)"
        )
    for tier_cpu, mems in FARGATE_TIERS:
        if tier_cpu < req_cpu:
            continue
        for mem in mems:
            if mem >= req_mem:
                cpu_str = f"{tier_cpu:g}"
                mem_str = f"{mem:g}Gi"
                return cpu_str, mem_str
    raise ValueError(f"No Fargate tier accommodates {cpu}/{memory}")
