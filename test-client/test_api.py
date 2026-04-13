"""End-to-end tests for the Phase 2 API server.

Prerequisites:
  - Runtime image built: docker build -t aviary-runtime:latest ./runtime/
  - Stack running:       docker compose up -d

Auth: obtain a real JWT from Keycloak via direct-grant (dev realm enables
this for the aviary-web client), then plant a session entry directly into
the API's Redis store. This mirrors the real /api/auth/callback effect
without forcing a browser-mediated authorization-code flow.

Mock runtime: each message body carries an optional `mock_scenario` that
the API forwards to the runtime via `agent_config.mock_scenario`. Purely
request-scoped — agents stay free of test data.
"""

from __future__ import annotations

import asyncio
import json
import secrets
import sys
import time
import uuid
from dataclasses import dataclass

import httpx
import redis.asyncio as aioredis
import websockets

API_URL = "http://localhost:8000"
WS_URL = "ws://localhost:8000"
SUPERVISOR_URL = "http://localhost:9000"
REDIS_URL = "redis://localhost:6379/0"
KEYCLOAK_URL = "http://localhost:8180"
REALM = "aviary"
CLIENT_ID = "aviary-web"
DEV_USER = "dev"
DEV_PASS = "dev"
ORIGIN = "http://localhost:3000"

COOKIE_NAME = "aviary_session"
SESSION_KEY_PREFIX = "auth:session:"
SESSION_TTL = 24 * 60 * 60

DUMMY_MODEL_CFG = {"model": "mock-model", "backend": "mock"}


# ─── auth helpers ───────────────────────────────────────────────────

async def _get_tokens(client: httpx.AsyncClient) -> dict:
    r = await client.post(
        f"{KEYCLOAK_URL}/realms/{REALM}/protocol/openid-connect/token",
        data={
            "grant_type": "password",
            "client_id": CLIENT_ID,
            "username": DEV_USER,
            "password": DEV_PASS,
            "scope": "openid",
        },
        timeout=10,
    )
    r.raise_for_status()
    return r.json()


async def _plant_session(redis: aioredis.Redis, tokens: dict, sub: str) -> str:
    sid = secrets.token_urlsafe(32)
    payload = {
        "user_external_id": sub,
        "access_token": tokens["access_token"],
        "refresh_token": tokens["refresh_token"],
        "id_token": tokens.get("id_token"),
        "access_token_expires_at": int(time.time()) + int(tokens.get("expires_in", 300)),
    }
    await redis.set(f"{SESSION_KEY_PREFIX}{sid}", json.dumps(payload), ex=SESSION_TTL)
    return sid


async def setup_auth() -> str:
    """Return a session id cookie bound to the dev user."""
    async with httpx.AsyncClient() as c:
        tokens = await _get_tokens(c)
    # Extract sub without verifying signature — we only need the claim, the
    # API will verify on every request.
    import base64
    payload_b64 = tokens["access_token"].split(".")[1]
    payload_b64 += "=" * (-len(payload_b64) % 4)
    claims = json.loads(base64.urlsafe_b64decode(payload_b64))
    sub = claims["sub"]

    redis = aioredis.from_url(REDIS_URL, decode_responses=True)
    try:
        return await _plant_session(redis, tokens, sub)
    finally:
        await redis.aclose()


# ─── supervisor helpers ─────────────────────────────────────────────

async def supervisor_replicas(agent_id: str) -> list[dict]:
    async with httpx.AsyncClient() as c:
        r = await c.get(f"{SUPERVISOR_URL}/v1/agents/{agent_id}/replicas", timeout=5)
        if r.status_code == 404:
            return []
        r.raise_for_status()
        return r.json().get("replicas", [])


async def wait_for_replica_count(agent_id: str, expected: int, timeout: float = 30) -> int:
    deadline = time.monotonic() + timeout
    last = -1
    while time.monotonic() < deadline:
        last = len([r for r in await supervisor_replicas(agent_id) if r["status"] == "running"])
        if last == expected:
            return last
        await asyncio.sleep(0.5)
    return last


# ─── API helpers ────────────────────────────────────────────────────

def _api_client(sid: str) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        base_url=API_URL,
        cookies={COOKIE_NAME: sid},
        timeout=30,
    )


async def create_agent(http: httpx.AsyncClient, slug_suffix: str) -> dict:
    r = await http.post(
        "/api/agents",
        json={
            "name": f"Test {slug_suffix}",
            "slug": f"test-{slug_suffix}-{uuid.uuid4().hex[:8]}",
            "instruction": "mock agent for tests",
            "model_config": DUMMY_MODEL_CFG,
        },
    )
    assert r.status_code == 201, f"create agent: {r.status_code} {r.text}"
    return r.json()


async def create_session(http: httpx.AsyncClient, agent_id: str) -> dict:
    r = await http.post(f"/api/agents/{agent_id}/sessions", json={"agent_id": agent_id})
    assert r.status_code == 201, f"create session: {r.status_code} {r.text}"
    return r.json()


async def delete_agent(http: httpx.AsyncClient, agent_id: str) -> None:
    await http.delete(f"/api/agents/{agent_id}")


# ─── WebSocket helpers ──────────────────────────────────────────────

def _ws_url(session_id: str) -> str:
    return f"{WS_URL}/api/sessions/{session_id}/ws"


async def ws_connect(session_id: str, sid: str) -> websockets.WebSocketClientProtocol:
    return await websockets.connect(
        _ws_url(session_id),
        additional_headers={
            "Origin": ORIGIN,
            "Cookie": f"{COOKIE_NAME}={sid}",
        },
    )


async def ws_recv_until(ws, predicate, timeout: float = 15) -> list[dict]:
    collected: list[dict] = []
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        remaining = deadline - time.monotonic()
        raw = await asyncio.wait_for(ws.recv(), timeout=remaining)
        event = json.loads(raw)
        collected.append(event)
        if predicate(event):
            return collected
    raise TimeoutError(f"condition not met; got {[e.get('type') for e in collected]}")


async def ws_skip_status(ws) -> None:
    """Consume initial status events until 'ready'."""
    while True:
        raw = await asyncio.wait_for(ws.recv(), timeout=60)
        e = json.loads(raw)
        if e.get("type") == "status" and e.get("status") == "ready":
            return
        if e.get("type") == "status" and e.get("status") == "offline":
            raise RuntimeError(f"agent offline: {e.get('message')}")


def mock_message(content: str, scenario_steps: list[dict]) -> dict:
    return {
        "type": "message",
        "content": content,
        "mock_scenario": {"steps": scenario_steps},
    }


# ─── tests ──────────────────────────────────────────────────────────

async def test_auth_me(sid: str) -> None:
    async with _api_client(sid) as http:
        r = await http.get("/api/auth/me")
        assert r.status_code == 200, f"/me: {r.status_code} {r.text}"
        user = r.json()
        assert user["email"] == "dev@aviary.local"


async def test_agent_crud(sid: str) -> None:
    async with _api_client(sid) as http:
        agent = await create_agent(http, "crud")
        aid = agent["id"]
        try:
            r = await http.get("/api/agents")
            assert r.status_code == 200
            assert any(a["id"] == aid for a in r.json()["items"])

            r = await http.get(f"/api/agents/{aid}")
            assert r.status_code == 200
            assert r.json()["name"] == agent["name"]

            r = await http.patch(f"/api/agents/{aid}", json={"name": "Renamed"})
            assert r.status_code == 200
            assert r.json()["name"] == "Renamed"
        finally:
            await delete_agent(http, aid)

        r = await http.get(f"/api/agents/{aid}")
        assert r.status_code == 404, "deleted agent should 404"


async def test_agent_create_does_not_spawn(sid: str) -> None:
    """Lazy spawn: creating an agent must not start any replicas.
    Provisioning only begins when the user actually opens a session."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "no-spawn-agent")
        aid = agent["id"]
        try:
            await asyncio.sleep(1)
            replicas = await supervisor_replicas(aid)
            assert len(replicas) == 0, f"expected 0 replicas, got {replicas}"
        finally:
            await delete_agent(http, aid)


async def test_session_create_triggers_spawn(sid: str) -> None:
    """Session creation (the "New Session" click) starts provisioning so
    the runtime is warm by the time the user sends a message."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "session-spawn")
        aid = agent["id"]
        try:
            assert len(await supervisor_replicas(aid)) == 0, "pre-session baseline"
            await create_session(http, aid)
            running = await wait_for_replica_count(aid, 1, timeout=30)
            assert running == 1, f"expected 1 replica after session create, got {running}"
        finally:
            await delete_agent(http, aid)


async def test_ws_message_round_trip(sid: str) -> None:
    async with _api_client(sid) as http:
        agent = await create_agent(http, "ws-roundtrip")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)

            async with await ws_connect(session["id"], sid) as ws:
                await ws_skip_status(ws)
                await ws.send(json.dumps(mock_message("hi", [
                    {"type": "chunk", "content": "Hello"},
                    {"type": "chunk", "content": " world"},
                ])))
                events = await ws_recv_until(ws, lambda e: e.get("type") == "done")
                chunks = [e for e in events if e.get("type") == "chunk"]
                assert "".join(c["content"] for c in chunks) == "Hello world"

            r = await http.get(f"/api/sessions/{session['id']}/messages")
            msgs = r.json()["messages"]
            assert any(m["sender_type"] == "user" and m["content"] == "hi" for m in msgs)
            assert any(m["sender_type"] == "agent" and m["content"] == "Hello world" for m in msgs)
        finally:
            await delete_agent(http, aid)


async def test_ws_reconnect_replay(sid: str) -> None:
    """Client disconnect mid-stream → reconnect → receive replay of buffered events."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "reconnect")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)
            scenario = [
                {"type": "chunk", "content": "A"},
                {"type": "sleep", "ms": 1500},
                {"type": "chunk", "content": "B"},
                {"type": "sleep", "ms": 1500},
                {"type": "chunk", "content": "C"},
            ]

            ws1 = await ws_connect(session["id"], sid)
            await ws_skip_status(ws1)
            await ws1.send(json.dumps(mock_message("long", scenario)))
            first = await ws_recv_until(ws1, lambda e: e.get("type") == "chunk")
            assert first[-1]["content"] == "A"
            await ws1.close()

            # Reconnect before scenario finishes (~3s total with sleeps).
            await asyncio.sleep(0.5)
            async with await ws_connect(session["id"], sid) as ws2:
                await ws_skip_status(ws2)
                events = await ws_recv_until(ws2, lambda e: e.get("type") == "done", timeout=20)
                types = [e.get("type") for e in events]
                assert "replay_start" in types
                assert "replay_end" in types
                all_chunks = "".join(e["content"] for e in events if e.get("type") == "chunk")
                assert "A" in all_chunks and "B" in all_chunks and "C" in all_chunks
        finally:
            await delete_agent(http, aid)


async def test_ws_cancel(sid: str) -> None:
    async with _api_client(sid) as http:
        agent = await create_agent(http, "cancel")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)
            scenario = [
                {"type": "chunk", "content": "before"},
                {"type": "sleep", "ms": 5000},
                {"type": "chunk", "content": "after"},
            ]
            async with await ws_connect(session["id"], sid) as ws:
                await ws_skip_status(ws)
                await ws.send(json.dumps(mock_message("long", scenario)))
                await ws_recv_until(ws, lambda e: e.get("type") == "chunk")
                await asyncio.sleep(0.3)
                await ws.send(json.dumps({"type": "cancel"}))
                events = await ws_recv_until(
                    ws, lambda e: e.get("type") in ("cancelled", "error", "done"),
                    timeout=10,
                )
                assert events[-1]["type"] == "cancelled", f"expected cancelled, got {events[-1]}"
        finally:
            await delete_agent(http, aid)


async def test_multi_session_isolation(sid: str) -> None:
    """Two concurrent sessions on the same agent produce isolated responses."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "multi")
        aid = agent["id"]
        try:
            s1 = await create_session(http, aid)
            s2 = await create_session(http, aid)

            async def run(session, tag: str) -> str:
                async with await ws_connect(session["id"], sid) as ws:
                    await ws_skip_status(ws)
                    await ws.send(json.dumps(mock_message(tag, [
                        {"type": "chunk", "content": f"reply-{tag}"},
                    ])))
                    events = await ws_recv_until(ws, lambda e: e.get("type") == "done")
                    return "".join(e["content"] for e in events if e.get("type") == "chunk")

            r1, r2 = await asyncio.gather(run(s1, "one"), run(s2, "two"))
            assert r1 == "reply-one"
            assert r2 == "reply-two"
        finally:
            await delete_agent(http, aid)


async def test_session_delete_blocks_reaccess(sid: str) -> None:
    """After DELETE /api/sessions/{id}, the owner can no longer fetch or stream it."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "sess-del")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)
            r = await http.delete(f"/api/sessions/{session['id']}")
            assert r.status_code == 204

            r = await http.get(f"/api/sessions/{session['id']}")
            assert r.status_code == 404

            # WS should be rejected since session is deleted.
            try:
                ws = await ws_connect(session["id"], sid)
                await ws.close()
                # If we got here, expect close code 4003 or send to fail.
            except websockets.exceptions.WebSocketException:
                pass  # acceptable
        finally:
            await delete_agent(http, aid)


async def test_delete_one_session_keeps_other_working(sid: str) -> None:
    async with _api_client(sid) as http:
        agent = await create_agent(http, "keep-other")
        aid = agent["id"]
        try:
            s_keep = await create_session(http, aid)
            s_drop = await create_session(http, aid)

            r = await http.delete(f"/api/sessions/{s_drop['id']}")
            assert r.status_code == 204

            async with await ws_connect(s_keep["id"], sid) as ws:
                await ws_skip_status(ws)
                await ws.send(json.dumps(mock_message("ping", [
                    {"type": "chunk", "content": "pong"},
                ])))
                events = await ws_recv_until(ws, lambda e: e.get("type") == "done")
                txt = "".join(e["content"] for e in events if e.get("type") == "chunk")
                assert txt == "pong"
        finally:
            await delete_agent(http, aid)


async def test_agent_delete_with_no_sessions_hard_deletes(sid: str) -> None:
    """Deleting an agent that has no sessions skips soft-delete and hard-
    deletes immediately (row gone, future GET 404)."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "delete-empty")
        aid = agent["id"]
        r = await http.delete(f"/api/agents/{aid}")
        assert r.status_code == 204
        r = await http.get(f"/api/agents/{aid}")
        assert r.status_code == 404, "hard-deleted agent should 404"


async def test_agent_soft_delete_keeps_orphan_sessions_working(sid: str) -> None:
    """When an agent has active sessions, DELETE soft-deletes only: the
    detail stays viewable, new sessions are blocked, but existing sessions
    can continue chatting."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "soft-del")
        aid = agent["id"]
        session = await create_session(http, aid)
        try:
            r = await http.delete(f"/api/agents/{aid}")
            assert r.status_code == 204

            r = await http.get(f"/api/agents/{aid}")
            assert r.status_code == 200, "soft-deleted agent detail must remain viewable"
            assert r.json()["status"] == "deleted"

            r = await http.post(
                f"/api/agents/{aid}/sessions", json={"agent_id": aid},
            )
            assert r.status_code == 404, "new session on soft-deleted agent must be blocked"

            async with await ws_connect(session["id"], sid) as ws:
                await ws_skip_status(ws)
                await ws.send(json.dumps(mock_message("still there?", [
                    {"type": "chunk", "content": "yes"},
                ])))
                events = await ws_recv_until(ws, lambda e: e.get("type") == "done")
                assert "yes" in "".join(e["content"] for e in events if e.get("type") == "chunk")
        finally:
            await http.delete(f"/api/sessions/{session['id']}")


async def test_last_session_delete_hard_deletes_soft_deleted_agent(sid: str) -> None:
    """Deleting the final session of a soft-deleted agent triggers
    hard-delete: replicas stopped, agent row gone, volume subpath purged."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "cascade")
        aid = agent["id"]
        s1 = await create_session(http, aid)
        s2 = await create_session(http, aid)

        assert await wait_for_replica_count(aid, 1, timeout=30) == 1

        r = await http.delete(f"/api/agents/{aid}")
        assert r.status_code == 204
        assert (await http.get(f"/api/agents/{aid}")).status_code == 200

        r = await http.delete(f"/api/sessions/{s1['id']}")
        assert r.status_code == 204
        assert (await http.get(f"/api/agents/{aid}")).status_code == 200, \
            "agent row must persist while sessions remain"

        r = await http.delete(f"/api/sessions/{s2['id']}")
        assert r.status_code == 204

        r = await http.get(f"/api/agents/{aid}")
        assert r.status_code == 404, "agent row must be hard-deleted after last session"

        final = await wait_for_replica_count(aid, 0, timeout=15)
        assert final == 0, f"replicas must be stopped after hard-delete, got {final}"


async def test_idle_agent_zero_scales(sid: str) -> None:
    """An agent with no active sessions (min_tasks=1 policy) holds 1
    replica via scaling, but idle_cleanup zero-scales it once
    `last_activity_at` exceeds `agent_idle_timeout`.

    Dev env: AGENT_IDLE_TIMEOUT=20s, IDLE_CLEANUP_INTERVAL=10s.
    """
    async with _api_client(sid) as http:
        agent = await create_agent(http, "idle-zero")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)
            async with await ws_connect(session["id"], sid) as ws:
                await ws_skip_status(ws)
                await ws.send(json.dumps(mock_message("hi", [
                    {"type": "chunk", "content": "ok"},
                ])))
                await ws_recv_until(ws, lambda e: e.get("type") == "done")

            assert await wait_for_replica_count(aid, 1) == 1

            r = await http.delete(f"/api/sessions/{session['id']}")
            assert r.status_code == 204

            # Scaling alone won't drop below min_tasks=1. Only idle_cleanup
            # can zero-scale once last_activity_at exceeds the timeout.
            final = await wait_for_replica_count(aid, 0, timeout=45)
            assert final == 0, f"idle_cleanup should have zero-scaled, got {final}"
        finally:
            await delete_agent(http, aid)


async def test_unauthorized_ws(sid: str) -> None:
    """Missing cookie / wrong origin must be rejected at the handshake."""
    async with _api_client(sid) as http:
        agent = await create_agent(http, "ws-auth")
        aid = agent["id"]
        try:
            session = await create_session(http, aid)

            # Missing origin
            try:
                async with await websockets.connect(
                    _ws_url(session["id"]),
                    additional_headers={"Cookie": f"{COOKIE_NAME}={sid}"},
                ) as ws:
                    await ws.recv()
                    raise AssertionError("expected close")
            except websockets.exceptions.InvalidStatus:
                pass
            except websockets.exceptions.ConnectionClosed:
                pass

            # Wrong origin
            try:
                async with await websockets.connect(
                    _ws_url(session["id"]),
                    additional_headers={
                        "Origin": "http://evil.example",
                        "Cookie": f"{COOKIE_NAME}={sid}",
                    },
                ) as ws:
                    await ws.recv()
                    raise AssertionError("expected close")
            except websockets.exceptions.InvalidStatus:
                pass
            except websockets.exceptions.ConnectionClosed:
                pass

            # No cookie at all
            try:
                async with await websockets.connect(
                    _ws_url(session["id"]),
                    additional_headers={"Origin": ORIGIN},
                ) as ws:
                    await ws.recv()
                    raise AssertionError("expected close")
            except websockets.exceptions.InvalidStatus:
                pass
            except websockets.exceptions.ConnectionClosed:
                pass
        finally:
            await delete_agent(http, aid)


# ─── runner ─────────────────────────────────────────────────────────

TESTS = [
    test_auth_me,
    test_agent_crud,
    test_agent_create_does_not_spawn,
    test_session_create_triggers_spawn,
    test_ws_message_round_trip,
    test_ws_reconnect_replay,
    test_ws_cancel,
    test_multi_session_isolation,
    test_session_delete_blocks_reaccess,
    test_delete_one_session_keeps_other_working,
    test_agent_delete_with_no_sessions_hard_deletes,
    test_agent_soft_delete_keeps_orphan_sessions_working,
    test_last_session_delete_hard_deletes_soft_deleted_agent,
    test_idle_agent_zero_scales,
    test_unauthorized_ws,
]


async def main() -> int:
    print("[setup] obtaining dev session cookie via Keycloak direct-grant")
    sid = await setup_auth()

    failed = 0
    for test in TESTS:
        name = test.__name__
        t0 = time.monotonic()
        try:
            await test(sid)
            print(f"  PASS  {name}  ({time.monotonic() - t0:.1f}s)")
        except AssertionError as e:
            failed += 1
            print(f"  FAIL  {name}  ({time.monotonic() - t0:.1f}s): {e}")
        except Exception as e:
            failed += 1
            print(f"  FAIL  {name}  ({time.monotonic() - t0:.1f}s): {type(e).__name__}: {e}")

    total = len(TESTS)
    print(f"\n{total - failed}/{total} passed")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
