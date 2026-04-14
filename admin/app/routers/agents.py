"""Agent admin pages — Fargate-ish vocabulary (tasks, service, security config).

The same concepts map to Docker locally:
  task   = runtime container (replica)
  service= the pool of running tasks for one agent
  security configuration = network policy + resource limits
"""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Form, HTTPException, Request
from fastapi.responses import HTMLResponse
from sqlalchemy import select
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Agent, Policy

from app.clients.supervisor import client as supervisor
from app.db import async_session_factory
from app.flash import err, ok
from app.templates import templates

router = APIRouter()


@router.get("/", response_class=HTMLResponse)
async def list_agents(request: Request):
    async with async_session_factory() as db:
        result = await db.execute(
            select(Agent).options(selectinload(Agent.policy)).order_by(Agent.created_at.desc())
        )
        agents = result.scalars().all()

    rows = []
    for a in agents:
        tasks = await supervisor.list_tasks(str(a.id))
        running = sum(1 for t in tasks if t.get("status") == "running")
        rows.append({
            "id": str(a.id),
            "name": a.name,
            "slug": a.slug,
            "owner_id": a.owner_id,
            "status": a.status,
            "tasks_running": running,
            "tasks_total": len(tasks),
            "min_tasks": a.policy.min_tasks if a.policy else 0,
            "max_tasks": a.policy.max_tasks if a.policy else 1,
        })
    return templates.TemplateResponse(request, "agents/list.html", {"agents": rows})


@router.get("/agents/{agent_id}", response_class=HTMLResponse)
async def agent_detail(request: Request, agent_id: uuid.UUID):
    async with async_session_factory() as db:
        agent = (
            await db.execute(
                select(Agent)
                .where(Agent.id == agent_id)
                .options(selectinload(Agent.policy))
            )
        ).scalar_one_or_none()
    if agent is None:
        raise HTTPException(404, "Agent not found")

    tasks = await supervisor.list_tasks(str(agent_id))
    metrics = await supervisor.metrics(str(agent_id))

    policy = agent.policy
    rules = (policy.policy_rules or {}) if policy else {}
    network = rules.get("network") or {}
    return templates.TemplateResponse(request, "agents/detail.html", {
        "agent": agent,
        "policy": policy,
        "tasks": tasks,
        "metrics": metrics,
        "security_groups": ", ".join(network.get("extra_networks") or []),
        "subnets": ", ".join(network.get("subnets") or []),
        "resource_limits": (policy.resource_limits if policy else None) or {},
    })


# ── Admin actions ─────────────────────────────────────────────


@router.post("/agents/{agent_id}/restart")
async def restart_tasks(agent_id: uuid.UUID):
    url = f"/agents/{agent_id}"
    try:
        await supervisor.restart(str(agent_id))
    except Exception as e:
        return err(url, f"Restart failed: {e}")
    return ok(url, "Rolling restart triggered")


@router.post("/agents/{agent_id}/deactivate")
async def deactivate(agent_id: uuid.UUID):
    url = f"/agents/{agent_id}"
    try:
        await supervisor.deactivate(str(agent_id))
    except Exception as e:
        return err(url, f"Deactivate failed: {e}")
    return ok(url, "Tasks stopped")


@router.post("/agents/{agent_id}/scale")
async def scale(agent_id: uuid.UUID, target: int = Form(...)):
    url = f"/agents/{agent_id}"
    try:
        await supervisor.scale(str(agent_id), target)
    except Exception as e:
        return err(url, f"Scale failed: {e}")
    return ok(url, f"Scaled to {target}")


@router.post("/agents/{agent_id}/purge")
async def purge(agent_id: uuid.UUID):
    try:
        await supervisor.purge(str(agent_id))
        async with async_session_factory() as db:
            agent = (
                await db.execute(select(Agent).where(Agent.id == agent_id))
            ).scalar_one_or_none()
            if agent is not None:
                await db.delete(agent)
                await db.commit()
    except Exception as e:
        return err(f"/agents/{agent_id}", f"Purge failed: {e}")
    return ok("/", "Agent purged")


@router.post("/agents/{agent_id}/security")
async def update_security(
    agent_id: uuid.UUID,
    min_tasks: int = Form(...),
    max_tasks: int = Form(...),
    security_groups: str = Form(""),
    subnets: str = Form(""),
    memory_mb: int | None = Form(None),
    cpu_units: int | None = Form(None),
):
    url = f"/agents/{agent_id}"
    extras = [s.strip() for s in security_groups.split(",") if s.strip()]
    subnet_list = [s.strip() for s in subnets.split(",") if s.strip()]

    async with async_session_factory() as db:
        agent = (
            await db.execute(
                select(Agent).where(Agent.id == agent_id).options(selectinload(Agent.policy))
            )
        ).scalar_one_or_none()
        if agent is None:
            return err(url, "Agent not found")

        if agent.policy is None:
            agent.policy = Policy(agent_id=agent.id)

        policy = agent.policy
        policy.min_tasks = max(0, min_tasks)
        policy.max_tasks = max(policy.min_tasks, max_tasks)

        rules = dict(policy.policy_rules or {})
        rules["network"] = {
            "extra_networks": extras,
            "subnets": subnet_list,
        }
        policy.policy_rules = rules

        limits = dict(policy.resource_limits or {})
        if memory_mb is not None:
            limits["memory_mb"] = memory_mb
        if cpu_units is not None:
            limits["cpu_units"] = cpu_units
        policy.resource_limits = limits or None

        await db.commit()
    return ok(url, "Configuration saved")
