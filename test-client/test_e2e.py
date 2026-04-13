"""End-to-end integration tests for Phase 1.

Prerequisites:
  - Runtime image built: docker build -t aviary-runtime:latest ./runtime/
  - Stack running:       docker compose up -d

The runtime dispatches per-request: when `agent_config.mock_scenario` is
present it runs the scripted mock, otherwise it invokes claude-agent-sdk.
File ops in scenarios flow through the real bwrap sandbox, so we can verify:
  - session workspace isolation
  - resume / persistence of session state
  - A2A file sharing across agents in the same session
"""

from __future__ import annotations

import asyncio
import json
import sys
import uuid
from dataclasses import dataclass

import asyncpg
import httpx
import redis.asyncio as aioredis

SUPERVISOR_URL = "http://localhost:9000"
REDIS_URL = "redis://localhost:6379/0"
DB_URL = "postgresql://aviary:aviary@localhost:5432/aviary"

# Dummy model config — mock agent ignores the values but they must be present.
DUMMY_MODEL_CFG = {"model": "mock-model", "backend": "mock"}


# ─── helpers ─────────────────────────────────────────────────────────

@dataclass
class Ctx:
    client: httpx.AsyncClient


async def seed_agent(
    agent_id: str,
    name: str = "test",
    min_tasks: int = 1,
    max_tasks: int = 1,
    network_policy: dict | None = None,
) -> None:
    conn = await asyncpg.connect(DB_URL)
    try:
        policy_rules = {"network": network_policy} if network_policy else None
        await conn.execute(
            """
            INSERT INTO agents (id, name, slug, owner_id, instruction, model_config, status)
            VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
            ON CONFLICT (id) DO NOTHING
            """,
            uuid.UUID(agent_id),
            f"Test {name}",
            f"test-{name}-{agent_id[:8]}",
            "test-user",
            "test instruction",
            json.dumps(DUMMY_MODEL_CFG),
            "active",
        )
        await conn.execute(
            """
            INSERT INTO policies (id, agent_id, min_tasks, max_tasks, policy_rules)
            VALUES ($1, $2, $3, $4, $5::jsonb)
            ON CONFLICT (agent_id) DO NOTHING
            """,
            uuid.uuid4(), uuid.UUID(agent_id), min_tasks, max_tasks,
            json.dumps(policy_rules) if policy_rules is not None else None,
        )
    finally:
        await conn.close()


async def cleanup_agent(client: httpx.AsyncClient, agent_id: str) -> None:
    # Stop replicas first; then the agent row — DB CASCADE removes sessions,
    # messages, and the policy in one shot.
    await client.delete(f"{SUPERVISOR_URL}/v1/agents/{agent_id}", params={"purge": "true"})
    conn = await asyncpg.connect(DB_URL)
    try:
        await conn.execute("DELETE FROM agents WHERE id = $1::uuid", agent_id)
    finally:
        await conn.close()


async def replica_count(client: httpx.AsyncClient, agent_id: str) -> int:
    r = await client.get(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/replicas")
    r.raise_for_status()
    return len([x for x in r.json()["replicas"] if x["status"] == "running"])


async def wait_until(
    fn, predicate, timeout: float, interval: float = 1.0, desc: str = "condition",
):
    deadline = asyncio.get_event_loop().time() + timeout
    last = None
    while asyncio.get_event_loop().time() < deadline:
        last = await fn()
        if predicate(last):
            return last
        await asyncio.sleep(interval)
    raise AssertionError(f"wait_until timed out ({desc}); last value: {last}")


async def register_and_run(client: httpx.AsyncClient, agent_id: str) -> None:
    r = await client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/register")
    r.raise_for_status()
    r = await client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/run")
    r.raise_for_status()
    r = await client.get(
        f"{SUPERVISOR_URL}/v1/agents/{agent_id}/wait", params={"timeout": 60}, timeout=70,
    )
    r.raise_for_status()


async def send_scenario(
    client: httpx.AsyncClient,
    agent_id: str,
    session_id: str,
    steps: list[dict],
) -> list[dict]:
    """POST /message with a mock scenario; return collected SSE events."""
    body = {
        "content_parts": [{"text": "go"}],
        "session_id": session_id,
        "model_config_data": DUMMY_MODEL_CFG,
        "agent_config": {"mock_scenario": {"steps": steps}},
    }
    events: list[dict] = []
    async with client.stream(
        "POST",
        f"{SUPERVISOR_URL}/v1/agents/{agent_id}/sessions/{session_id}/message",
        json=body,
        timeout=60,
    ) as resp:
        resp.raise_for_status()
        async for line in resp.aiter_lines():
            if line.startswith("data: "):
                try:
                    events.append(json.loads(line[6:]))
                except json.JSONDecodeError:
                    pass
    return events


def chunks_of(events: list[dict], etype: str = "chunk") -> list[str]:
    return [e.get("content", "") for e in events if e.get("type") == etype]


def require(condition: bool, msg: str) -> None:
    if not condition:
        print(f"  ✗ {msg}")
        sys.exit(1)
    print(f"  ✓ {msg}")


# ─── test cases ──────────────────────────────────────────────────────

async def test_basic_streaming(ctx: Ctx, agent_id: str) -> None:
    print("\n[test] basic streaming + Redis Pub/Sub + Streams")
    session_id = str(uuid.uuid4())

    # Subscribe BEFORE sending so we can verify real-time delivery.
    redis = aioredis.from_url(REDIS_URL, decode_responses=True)
    pubsub = redis.pubsub()
    await pubsub.subscribe(f"session:{session_id}:stream")
    # Drain the subscribe-ack so subsequent reads see only data messages.
    for _ in range(5):
        m = await pubsub.get_message(timeout=0.2)
        if m and m.get("type") == "subscribe":
            break

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "thinking", "content": "thinking out loud"},
        {"type": "chunk", "content": "hello "},
        {"type": "chunk", "content": "world"},
    ])

    texts = chunks_of(events)
    require("hello " in texts and "world" in texts, "chunks streamed in order")
    require(any(e.get("type") == "result" for e in events), "result event emitted")
    require(any(e.get("type") == "thinking" for e in events), "thinking event emitted")

    # Drain Pub/Sub (buffer might hold events briefly)
    received = 0
    for _ in range(20):
        msg = await pubsub.get_message(ignore_subscribe_messages=True, timeout=0.3)
        if msg is not None:
            received += 1
    require(received >= 3, f"Pub/Sub received {received} events (≥3)")

    # Redis Streams: result event persisted
    entries = await redis.xrange(f"session:{session_id}:events", count=10)
    require(any(data.get("type") == "result" for _, data in entries), "result event in Redis Stream")

    await pubsub.unsubscribe()
    await redis.aclose()


async def test_session_isolation(ctx: Ctx, agent_id: str) -> None:
    print("\n[test] session isolation — two sessions don't see each other's files")
    s1 = str(uuid.uuid4())
    s2 = str(uuid.uuid4())

    await send_scenario(ctx.client, agent_id, s1, [
        {"type": "write", "path": "/workspace/secret.txt", "content": "from-s1"},
    ])

    # s2 tries to read s1's file — should not exist in its sandbox.
    events = await send_scenario(ctx.client, agent_id, s2, [
        {"type": "read", "path": "/workspace/secret.txt"},
    ])
    combined = "".join(chunks_of(events))
    require("[missing" in combined and "from-s1" not in combined,
            "s2 cannot see file written by s1")

    # Sanity: s1 can still read its own file.
    events = await send_scenario(ctx.client, agent_id, s1, [
        {"type": "read", "path": "/workspace/secret.txt"},
    ])
    combined = "".join(chunks_of(events))
    require("from-s1" in combined, "s1 still sees its own file")


async def test_session_resume(ctx: Ctx, agent_id: str) -> None:
    print("\n[test] session resume — files persist across separate messages")
    session_id = str(uuid.uuid4())

    await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "write", "path": "/workspace/notes.md", "content": "round-1"},
    ])
    # Second message to same session — new SSE stream, same session state.
    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/notes.md"},
        {"type": "write", "path": "/workspace/notes.md", "content": "round-1\nround-2"},
    ])
    combined = "".join(chunks_of(events))
    require("round-1" in combined, "file from prior message visible on resume")

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/notes.md"},
    ])
    combined = "".join(chunks_of(events))
    require("round-1" in combined and "round-2" in combined,
            "appended content also persists")


async def test_a2a_sharing(
    ctx: Ctx, agent_a: str, agent_b: str,
) -> None:
    print("\n[test] A2A — two agents share files via the session workspace")
    session_id = str(uuid.uuid4())

    # Agent A writes a file
    await send_scenario(ctx.client, agent_a, session_id, [
        {"type": "write", "path": "/workspace/shared.txt", "content": "hi-from-A"},
    ])

    # Agent B, SAME session, should see the file.
    events = await send_scenario(ctx.client, agent_b, session_id, [
        {"type": "read", "path": "/workspace/shared.txt"},
    ])
    combined = "".join(chunks_of(events))
    require("hi-from-A" in combined, "agent B reads file written by agent A in same session")

    # Reverse direction
    await send_scenario(ctx.client, agent_b, session_id, [
        {"type": "write", "path": "/workspace/reply.txt", "content": "hi-back-from-B"},
    ])
    events = await send_scenario(ctx.client, agent_a, session_id, [
        {"type": "read", "path": "/workspace/reply.txt"},
    ])
    combined = "".join(chunks_of(events))
    require("hi-back-from-B" in combined, "agent A reads file written by agent B")

    # Different session: agent A in a DIFFERENT session must NOT see shared.txt
    other = str(uuid.uuid4())
    events = await send_scenario(ctx.client, agent_a, other, [
        {"type": "read", "path": "/workspace/shared.txt"},
    ])
    combined = "".join(chunks_of(events))
    require("hi-from-A" not in combined, "different session does not leak files")


async def test_scale_up_under_load(ctx: Ctx, agent_id: str) -> None:
    """Many concurrent sessions on one agent should trigger scale-up.

    Requires agent policy: min_tasks=1, max_tasks>=2.
    Depends on supervisor env: SCALING_CHECK_INTERVAL=5 (set in docker-compose).
    Default scale-up threshold is 3 sessions/replica.
    """
    print("\n[test] scaling — concurrent load triggers replica scale-up")

    # Previous tests may have left this agent idle long enough for
    # idle_cleanup to zero-scale it; re-ensure baseline before measuring.
    await register_and_run(ctx.client, agent_id)

    start = await replica_count(ctx.client, agent_id)
    require(start >= 1, f"agent starts with at least one replica (got {start})")

    # Hold 5 sessions active for long enough to span a scaling tick.
    concurrency = 5
    hold_ms = 20_000

    async def hold_session(sid: str):
        return await send_scenario(ctx.client, agent_id, sid, [
            {"type": "chunk", "content": f"holding {sid[:8]}"},
            {"type": "sleep", "ms": hold_ms},
        ])

    session_ids = [str(uuid.uuid4()) for _ in range(concurrency)]
    tasks = [asyncio.create_task(hold_session(sid)) for sid in session_ids]

    try:
        # Give the scaling loop (5s interval) at least 2 ticks to react.
        await wait_until(
            lambda: replica_count(ctx.client, agent_id),
            lambda n: n >= 2,
            timeout=25,
            interval=1.0,
            desc="replicas >= 2",
        )
        after_scale = await replica_count(ctx.client, agent_id)
        require(after_scale >= 2, f"scaled up to {after_scale} replicas under load")
    finally:
        for t in tasks:
            await t

    # After load drops, scaling returns to min_tasks (1). Idle cleanup may
    # then take it to 0 if activity stays quiet past AGENT_IDLE_TIMEOUT —
    # both outcomes are correct; we just assert we dropped below the peak.
    end = await wait_until(
        lambda: replica_count(ctx.client, agent_id),
        lambda n: n <= 1,
        timeout=40,
        interval=2.0,
        desc="scale back down to ≤1",
    )
    require(end <= 1, f"scaled back down to {end} replica(s)")


async def test_context_across_scaling(ctx: Ctx, agent_id: str) -> None:
    """Session state must survive scale-up/-down. Writes happen before and
    after the scaling storm, and files must be intact at the end — proving
    the agent subpath is shared across replicas (no per-replica local state).
    """
    print("\n[test] context preservation across scale events")
    # idle_cleanup may have zero-scaled this agent during the prior test's
    # settle phase; put a replica back before we start writing.
    await register_and_run(ctx.client, agent_id)
    session_id = str(uuid.uuid4())

    await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "write", "path": "/workspace/history.txt", "content": "turn-1"},
    ])

    # Saturate with other sessions long enough to provoke a scale-up cycle.
    hold_ms = 20_000

    async def hold_session(sid: str):
        return await send_scenario(ctx.client, agent_id, sid, [
            {"type": "sleep", "ms": hold_ms},
        ])

    filler = [asyncio.create_task(hold_session(str(uuid.uuid4()))) for _ in range(5)]
    try:
        peak = await wait_until(
            lambda: replica_count(ctx.client, agent_id),
            lambda n: n >= 2,
            timeout=25,
            desc="replicas >= 2",
        )
        require(peak >= 2, f"scale-up observed (peak={peak})")
    finally:
        for t in filler:
            await t

    # After load drops, scale down to baseline; then ensure state is intact.
    await wait_until(
        lambda: replica_count(ctx.client, agent_id),
        lambda n: n <= 1,
        timeout=40,
        desc="scale back down",
    )

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/history.txt"},
        {"type": "write", "path": "/workspace/history.txt", "content": "turn-1\nturn-2"},
    ])
    combined = "".join(chunks_of(events))
    require("turn-1" in combined, "turn-1 preserved across scale events")

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/history.txt"},
    ])
    combined = "".join(chunks_of(events))
    require(
        "turn-1" in combined and "turn-2" in combined,
        "turn-1 and turn-2 both preserved",
    )


async def test_concurrent_same_session_serialized(ctx: Ctx, agent_id: str) -> None:
    """Two messages fired at the same session concurrently must be serialized
    by the per-session mutex — no interleaving of scripted steps.
    """
    print("\n[test] concurrent messages on same session serialized via mutex")
    session_id = str(uuid.uuid4())

    def scenario(marker: str) -> list[dict]:
        return [
            {"type": "exec", "cmd": ["sh", "-c", f"echo {marker}-start >> /workspace/log.txt"]},
            {"type": "sleep", "ms": 600},
            {"type": "exec", "cmd": ["sh", "-c", f"echo {marker}-end   >> /workspace/log.txt"]},
        ]

    await asyncio.gather(
        send_scenario(ctx.client, agent_id, session_id, scenario("A")),
        send_scenario(ctx.client, agent_id, session_id, scenario("B")),
    )

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/log.txt"},
    ])
    content = "".join(chunks_of(events))
    lines = [ln.strip() for ln in content.strip().split("\n") if ln.strip()]

    require(len(lines) == 4, f"expected 4 log lines, got {lines}")
    a_start = lines.index("A-start")
    b_start = lines.index("B-start")
    require(
        lines[a_start + 1] == "A-end" and lines[b_start + 1] == "B-end",
        "each message's start/end adjacent — no interleaving",
    )


async def test_persist_after_container_removal(ctx: Ctx, agent_id: str) -> None:
    """When an agent is fully torn down (idle cleanup path), the next message
    must spawn a fresh container and still see files written earlier.
    Volumes are retained on agent delete; only containers go away.
    """
    print("\n[test] persistence across full container removal (idle cleanup path)")
    session_id = str(uuid.uuid4())

    await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "write", "path": "/workspace/persist.txt", "content": "before-reset"},
    ])

    r = await ctx.client.delete(f"{SUPERVISOR_URL}/v1/agents/{agent_id}")
    r.raise_for_status()
    require(r.json().get("replicas_stopped", 0) >= 1, "replicas actually stopped")
    require(await replica_count(ctx.client, agent_id) == 0, "no running containers after teardown")

    await register_and_run(ctx.client, agent_id)

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/persist.txt"},
        {"type": "write", "path": "/workspace/persist.txt", "content": "before-reset\nafter-restart"},
    ])
    combined = "".join(chunks_of(events))
    require("before-reset" in combined, "file written before teardown visible after restart")

    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "read", "path": "/workspace/persist.txt"},
    ])
    combined = "".join(chunks_of(events))
    require(
        "before-reset" in combined and "after-restart" in combined,
        "both pre- and post-restart writes coexist",
    )


async def test_network_policy_default_blocks_egress(ctx: Ctx, agent_id: str) -> None:
    print("\n[test] network policy — default profile blocks external egress")
    session_id = str(uuid.uuid4())
    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "exec", "cmd": [
            "sh", "-c",
            "curl -s --max-time 3 -o /dev/null -w '%{http_code}' https://example.com",
        ]},
    ])
    body = "".join(chunks_of(events)).strip()
    require(body in ("000", ""), f"external HTTP blocked (curl code={body!r})")


async def test_network_policy_approved_profile_allows_egress(
    ctx: Ctx, agent_id: str,
) -> None:
    print("\n[test] network policy — approved profile grants external egress")
    session_id = str(uuid.uuid4())
    events = await send_scenario(ctx.client, agent_id, session_id, [
        {"type": "exec", "cmd": [
            "sh", "-c",
            "curl -s --max-time 5 -o /dev/null -w '%{http_code}' https://example.com",
        ]},
    ])
    body = "".join(chunks_of(events)).strip()
    require(body.startswith("2") or body.startswith("3"), f"external HTTP allowed (curl code={body!r})")


async def test_rapid_agent_churn_keeps_supervisor_responsive(ctx: Ctx) -> None:
    """Regression: synchronous `container.stop(timeout=30)` used to block the
    supervisor event loop, so back-to-back agent deletes stalled the service
    for tens of seconds. Offloading to a thread must keep `/v1/health`
    responsive during churn.
    """
    print("\n[test] rapid agent churn — supervisor stays responsive during deletes")

    n = 3
    ids = [str(uuid.uuid4()) for _ in range(n)]
    for aid in ids:
        await seed_agent(aid, "churn", min_tasks=1, max_tasks=1)

    latencies: list[float] = []
    stop_probe = asyncio.Event()

    async def probe_health() -> None:
        async with httpx.AsyncClient(timeout=3) as c:
            while not stop_probe.is_set():
                t0 = asyncio.get_event_loop().time()
                try:
                    r = await c.get(f"{SUPERVISOR_URL}/v1/health")
                    r.raise_for_status()
                    latencies.append(asyncio.get_event_loop().time() - t0)
                except Exception as e:
                    latencies.append(999.0)
                    print(f"    probe error: {type(e).__name__}")
                await asyncio.sleep(0.25)

    try:
        await asyncio.gather(*[register_and_run(ctx.client, aid) for aid in ids])
        for aid in ids:
            require(await replica_count(ctx.client, aid) == 1, f"{aid[:8]} spawned")

        probe_task = asyncio.create_task(probe_health())
        try:
            # Fire all deletes concurrently — this is what used to wedge the loop.
            await asyncio.gather(*[
                ctx.client.delete(f"{SUPERVISOR_URL}/v1/agents/{aid}")
                for aid in ids
            ])
            # Give the probe time to catch the stall window if there is one.
            await asyncio.sleep(1.0)
        finally:
            stop_probe.set()
            await probe_task

        worst = max(latencies) if latencies else 0
        require(
            worst < 2.0,
            f"health probe max latency {worst:.2f}s stayed under 2s during churn"
            f" ({len(latencies)} samples)",
        )
        for aid in ids:
            require(await replica_count(ctx.client, aid) == 0, f"{aid[:8]} replicas torn down")
    finally:
        for aid in ids:
            await cleanup_agent(ctx.client, aid)


async def test_abort(ctx: Ctx, agent_id: str) -> None:
    print("\n[test] abort mid-stream")
    session_id = str(uuid.uuid4())

    async def long_stream():
        return await send_scenario(ctx.client, agent_id, session_id, [
            {"type": "chunk", "content": "one", "delay_ms": 100},
            {"type": "sleep", "ms": 3000},
            {"type": "chunk", "content": "after-sleep"},
        ])

    task = asyncio.create_task(long_stream())
    await asyncio.sleep(0.5)
    r = await ctx.client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/sessions/{session_id}/abort")
    require(r.json().get("ok") is True, "abort returned ok")
    events = await task
    require(not any(e.get("content") == "after-sleep" for e in events),
            "post-sleep chunk did not arrive (aborted)")


# ─── runner ──────────────────────────────────────────────────────────

async def main():
    agent_a = str(uuid.uuid4())
    agent_b = str(uuid.uuid4())
    agent_scale = str(uuid.uuid4())
    agent_default_net = str(uuid.uuid4())
    agent_open_net = str(uuid.uuid4())

    await seed_agent(agent_a, "a")
    await seed_agent(agent_b, "b")
    # Scaling tests need room to grow; concurrent session limit per task is 5.
    await seed_agent(agent_scale, "scale", min_tasks=1, max_tasks=3)
    # Network policy tests: default blocks, approved profile allows egress.
    await seed_agent(agent_default_net, "netdefault")
    await seed_agent(
        agent_open_net, "netopen",
        network_policy={"extra_networks": ["aviary-egress-open"]},
    )

    async with httpx.AsyncClient(timeout=60) as client:
        ctx = Ctx(client=client)
        try:
            await register_and_run(client, agent_a)
            await register_and_run(client, agent_b)
            await register_and_run(client, agent_scale)
            await register_and_run(client, agent_default_net)
            await register_and_run(client, agent_open_net)

            await test_basic_streaming(ctx, agent_a)
            await test_session_isolation(ctx, agent_a)
            await test_session_resume(ctx, agent_a)
            await test_concurrent_same_session_serialized(ctx, agent_a)
            await test_a2a_sharing(ctx, agent_a, agent_b)
            await test_network_policy_default_blocks_egress(ctx, agent_default_net)
            await test_network_policy_approved_profile_allows_egress(ctx, agent_open_net)
            await test_abort(ctx, agent_a)
            await test_persist_after_container_removal(ctx, agent_a)
            await test_scale_up_under_load(ctx, agent_scale)
            await test_context_across_scaling(ctx, agent_scale)
            await test_rapid_agent_churn_keeps_supervisor_responsive(ctx)
        finally:
            await cleanup_agent(client, agent_a)
            await cleanup_agent(client, agent_b)
            await cleanup_agent(client, agent_scale)
            await cleanup_agent(client, agent_default_net)
            await cleanup_agent(client, agent_open_net)

    print("\n=== All tests passed! ===")


if __name__ == "__main__":
    asyncio.run(main())
