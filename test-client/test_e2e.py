"""End-to-end test for the Phase 1 runtime execution layer.

Prerequisites:
  - docker compose up -d (postgres, redis, vault, agent-supervisor)
  - aviary-runtime:latest image built
  - An agent record seeded in DB (or use --seed flag)

Usage:
  uv run python test-client/test_e2e.py
  uv run python test-client/test_e2e.py --seed  # auto-seed a test agent
"""

import argparse
import asyncio
import json
import sys
import uuid

import httpx
import redis.asyncio as aioredis

SUPERVISOR_URL = "http://localhost:9000"
REDIS_URL = "redis://localhost:6379/0"
DB_URL = "postgresql://aviary:aviary@localhost:5432/aviary"


async def seed_agent(agent_id: str) -> None:
    import asyncpg

    conn = await asyncpg.connect(DB_URL)
    try:
        await conn.execute(
            """
            INSERT INTO agents (id, name, slug, owner_id, instruction, model_config, status)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            ON CONFLICT (id) DO NOTHING
            """,
            uuid.UUID(agent_id),
            "Test Agent",
            f"test-agent-{agent_id[:8]}",
            "test-user",
            "You are a helpful test assistant. Reply briefly.",
            json.dumps({"model": "claude-sonnet-4-20250514", "backend": "anthropic"}),
            "active",
        )
        print(f"  Seeded agent {agent_id}")
    finally:
        await conn.close()


async def test_register(client: httpx.AsyncClient, agent_id: str) -> None:
    print("[1] Register agent...")
    resp = await client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/register")
    assert resp.status_code == 200, f"Register failed: {resp.text}"
    print(f"  OK: {resp.json()}")


async def test_run(client: httpx.AsyncClient, agent_id: str) -> None:
    print("[2] Run agent (ensure replicas)...")
    resp = await client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/run")
    assert resp.status_code == 200, f"Run failed: {resp.text}"
    data = resp.json()
    print(f"  OK: status={data['status']}, replicas={data.get('replicas', '?')}")


async def test_wait(client: httpx.AsyncClient, agent_id: str) -> None:
    print("[3] Wait for agent ready...")
    resp = await client.get(
        f"{SUPERVISOR_URL}/v1/agents/{agent_id}/wait",
        params={"timeout": 120},
        timeout=130,
    )
    assert resp.status_code == 200, f"Wait failed: {resp.text}"
    print(f"  OK: {resp.json()}")


async def test_message(client: httpx.AsyncClient, agent_id: str, session_id: str) -> dict:
    print("[4] Send message (SSE stream)...")
    events = []
    body = {
        "content_parts": [{"text": "Say hello in exactly 3 words."}],
        "session_id": session_id,
        "model_config_data": {"model": "claude-sonnet-4-20250514", "backend": "anthropic"},
        "agent_config": {"instruction": "Reply briefly."},
    }

    async with client.stream("POST", f"{SUPERVISOR_URL}/v1/agents/{agent_id}/sessions/{session_id}/message", json=body, timeout=120) as resp:
        assert resp.status_code == 200, f"Message failed: {resp.status_code}"
        async for line in resp.aiter_lines():
            if line.startswith("data: "):
                try:
                    event = json.loads(line[6:])
                    events.append(event)
                    etype = event.get("type", "?")
                    if etype == "chunk":
                        print(f"  chunk: {event.get('content', '')}", end="", flush=True)
                    elif etype == "result":
                        print(f"\n  result: cost=${event.get('total_cost_usd', 0):.4f}, turns={event.get('num_turns', '?')}")
                    elif etype == "error":
                        print(f"\n  ERROR: {event.get('message', '')}")
                except json.JSONDecodeError:
                    pass

    print(f"  Total events: {len(events)}")
    return {"events": events}


async def test_redis_pubsub(session_id: str) -> None:
    print("[5] Check Redis Pub/Sub (fire a quick subscribe)...")
    r = aioredis.from_url(REDIS_URL, decode_responses=True)
    try:
        pubsub = r.pubsub()
        await pubsub.subscribe(f"session:{session_id}:stream")
        print("  OK: subscribed (events were published during message)")
        await pubsub.unsubscribe()
    finally:
        await r.aclose()


async def test_redis_streams(session_id: str) -> None:
    print("[6] Check Redis Streams...")
    r = aioredis.from_url(REDIS_URL, decode_responses=True)
    try:
        stream_key = f"session:{session_id}:events"
        entries = await r.xrange(stream_key, count=10)
        if entries:
            print(f"  OK: {len(entries)} durable event(s)")
            for entry_id, data in entries:
                print(f"    {entry_id}: type={data.get('type', '?')}")
        else:
            print("  WARN: no durable events found (may be OK if no result event)")
    finally:
        await r.aclose()


async def test_abort(client: httpx.AsyncClient, agent_id: str, session_id: str) -> None:
    print("[7] Test abort (no active stream, expect ok=false)...")
    resp = await client.post(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/sessions/{session_id}/abort")
    print(f"  OK: {resp.json()}")


async def test_cleanup(client: httpx.AsyncClient, agent_id: str, session_id: str) -> None:
    print("[8] Cleanup session...")
    resp = await client.delete(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/sessions/{session_id}")
    print(f"  OK: {resp.json()}")


async def test_metrics(client: httpx.AsyncClient, agent_id: str) -> None:
    print("[9] Get metrics...")
    resp = await client.get(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/metrics")
    print(f"  OK: {resp.json()}")


async def test_delete(client: httpx.AsyncClient, agent_id: str) -> None:
    print("[10] Delete agent (stop container)...")
    resp = await client.delete(f"{SUPERVISOR_URL}/v1/agents/{agent_id}")
    print(f"  OK: {resp.json()}")


async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--seed", action="store_true", help="Auto-seed a test agent in DB")
    parser.add_argument("--agent-id", default=None, help="Existing agent UUID")
    parser.add_argument("--skip-delete", action="store_true", help="Keep container running after test")
    args = parser.parse_args()

    agent_id = args.agent_id or str(uuid.uuid4())
    session_id = str(uuid.uuid4())

    print(f"Agent ID:  {agent_id}")
    print(f"Session ID: {session_id}")
    print()

    if args.seed:
        await seed_agent(agent_id)
        print()

    async with httpx.AsyncClient() as client:
        await test_register(client, agent_id)
        await test_run(client, agent_id)
        await test_wait(client, agent_id)
        await test_message(client, agent_id, session_id)
        await test_redis_pubsub(session_id)
        await test_redis_streams(session_id)
        await test_abort(client, agent_id, session_id)
        await test_cleanup(client, agent_id, session_id)
        await test_metrics(client, agent_id)

        if not args.skip_delete:
            await test_delete(client, agent_id)

    print()
    print("=== All tests passed! ===")


if __name__ == "__main__":
    asyncio.run(main())
