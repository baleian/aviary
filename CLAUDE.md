# Aviary

Multi-tenant AI agent platform. Users will create/configure agents via Web UI; each agent runs in isolated runtime containers (Docker locally, Fargate in production) driven by claude-agent-sdk.

## Rearchitecting status (in progress)

The codebase is being rewritten from scratch. v1 used K3s-in-Docker + per-agent namespaces/PVCs/NetworkPolicy; the rewrite replaces that with a `RuntimeBackend` abstraction that runs on plain Docker locally and AWS Fargate in production. **The v1 code is archived at [v1/](v1/) for reference** (also tagged `v1-archive` in git).

### Phase 1 — decoupled runtime (DONE)
- Agent Supervisor with `RuntimeBackend` abstraction ([DockerBackend](agent-supervisor/app/backends/docker.py), [FargateBackend stub](agent-supervisor/app/backends/fargate.py))
- Multi-replica per agent, consistent-hash load balancing, auto-scaling, idle cleanup
- Shared-volume storage model (subpath per agent) that maps 1:1 to Fargate + EFS Access Points
- Runtime with scenario-driven mock mode for integration tests (same image as prod)
- Network policy via `agent.policy.network_profile` — profile-to-networks mapping in supervisor config (Docker `internal` network = default-sg blocked egress; extra networks = approved-SG analogue)
- End-to-end test suite covering streaming, isolation, resume, A2A, abort, scaling, mutex, persistence, network policy

### Phase 2 — in progress
- **LLM gateway (DONE)** — LiteLLM on `aviary-agent-default` + `platform` networks; runtime → `http://litellm:4000` (Anthropic `/v1/messages` passthrough). LiteLLM config in [config/litellm/config.yaml](config/litellm/config.yaml). Local model backend is developer-specific (llama-swap + llama-server on host, scripts in [scripts/llama-swap/](scripts/llama-swap/)); in prod LiteLLM is an external service reached via SG allowlist.
- **API server (MVP DONE)** — [api/](api/) FastAPI service (`:8000`): OIDC auth (Keycloak PKCE), user/agent/session CRUD, WebSocket chat streaming via supervisor SSE → Redis Pub/Sub + list-buffered replay. Owner-only authorization (no ACL yet — RBAC redesign deferred). **Lazy spawn**: agent create only prepares storage; session create triggers `/run` (prewarm); WS message falls back to `/run` as a re-spawn safety net. **Soft-delete lifecycle**: DELETE on an agent with active sessions flips `status=deleted` and keeps replicas serving orphan sessions (detail still viewable, new sessions blocked); the last session removal cascades to hard-delete (DB row, policy, replicas, agent workspace subpath). Deferred from v1: A2A, ACL/teams, MCP catalog, credentials/Vault, workflows, search, uploads.
- Web frontend reconnect
- RBAC redesign (proper role/permission model replacing v1's agent-owner/user/viewer)
- MCP gateway reintroduction
- Temporal for durable workflow execution
- Fargate backend implementation + Terraform for production deploy

## Architecture

```
Browser (future) → API (future, Phase 2) → Agent Supervisor (:9000) → Docker/Fargate runtime containers
                                                  ↓
                                         PostgreSQL / Redis

Supervisor responsibilities:
  - Agent-centric REST API (register/run/wait/message/abort/cleanup/metrics)
  - RuntimeBackend: replica lifecycle, consistent-hash LB, scale-to, idle cleanup
  - SSE proxy from runtime → Redis Pub/Sub (realtime) + Redis Streams (durable)

Runtime container (agent-runtime):
  - Express server with multi-session mutex
  - claude-agent-sdk query() — real LLM path
  - Mock agent — scenario-driven, same bwrap sandbox as prod (dispatched per-request)
  - bubblewrap sandbox for session-level filesystem isolation
```

## Services

Docker Compose (`docker-compose.yml`) runs:

- **PostgreSQL** (`:5432`) — agents, policies, sessions, messages, users
- **Redis** (`:6379`) — Pub/Sub (realtime streaming) + list-buffer replay + auth session store
- **Keycloak** (`:8180` host → `:8080` container) — OIDC provider; realm `aviary` auto-imported from [config/keycloak/aviary-realm.json](config/keycloak/aviary-realm.json); dev user `dev/dev`
- **API** (`:8000`) — FastAPI user-facing REST + WebSocket
- **Agent Supervisor** (`:9000`) — container lifecycle + internal API
- **LiteLLM** (`:4000`) — LLM gateway, attached to `platform` and `aviary-agent-default`
- **db-migrate** — Alembic migrations (one-shot)

Not yet in stack: Vault, Portkey, MCP gateway, web, admin, Temporal.

## RuntimeBackend abstraction

[agent-supervisor/app/backends/protocol.py](agent-supervisor/app/backends/protocol.py) defines the protocol. Key methods:

- `list_replicas(agent_id)` / `create_replica(...)` / `stop_replica(...)` / `scale_to(...)` — lifecycle
- `pick_replica(agent_id, session_id)` — consistent hash with capacity-aware ring walk
- `stream_message(...)` / `abort_session(...)` / `cleanup_session(...)` — auto-pick replica
- `ensure_storage(agent_id)` — volume + subpath preparation
- `get_replica_metrics(task)` / `wait_for_ready(task)` — health

DockerBackend is implemented; FargateBackend is a stub (Phase 2).

## Storage model (mirrors Fargate + EFS)

Two named volumes, both subpath-compatible:

- `aviary-agents-workspace` — per-agent subpath `{agent_id}/` → mounted at `/workspace` in runtime. All replicas of the same agent share this subpath, so conversation history (`.claude/`) lives on shared storage. Enables horizontal scale-out and replica substitution.
- `aviary-sessions-workspace` — mounted whole at `/workspace-shared`. Per-session subdirs (`{session_id}/`) enable A2A file sharing across agents in the same session. bwrap in the runtime tmpfs-overlays this path so other sessions are invisible from within the sandbox.

Uses **Docker volume subpath mount** (`VolumeOptions.Subpath`), requires Docker 25.0+. On Fargate this maps to **EFS Access Points** with root_directory per agent — no code change needed beyond the `FargateBackend` implementation.

## Network policy (egress control)

Every agent container joins `AGENT_PRIMARY_NETWORK` (`aviary-agent-default`), which is `internal: true` on Docker (= default-sg with all egress denied on Fargate). Per-agent overrides live in `policies.policy_rules.network`:

```json
{"network": {"extra_networks": ["aviary-egress-open"], "subnets": ["subnet-xyz"]}}
```

- `extra_networks` — Docker network names / Fargate SG IDs to attach in addition to the primary.
- `subnets` — Fargate subnet IDs; ignored by DockerBackend.

Admin enters these raw infrastructure names directly. Supervisor never auto-creates — if a referenced network doesn't exist, `create_replica` fails. Networks must be pre-provisioned by docker-compose (local) or Terraform (production).

Policy changes are picked up by the next `create_replica` (via `/run`, scale-up, or post-idle re-spawn). **Running replicas are not mutated** — a rolling restart is required to apply changes (Phase 2 admin endpoint).

## Multi-replica + load balancing

Containers named `aviary-agent-{agent_id}-{replica_idx}`. Labels: `aviary.agent-id`, `aviary.replica`, `aviary.managed=true`, `aviary.role=agent-runtime`.

- **Scaling** ([scaling.py](agent-supervisor/app/services/scaling.py), ported from v1): every `SCALING_CHECK_INTERVAL` (5s dev / 30s default), compute `sessions_per_replica = sum(sessions_active) / replicas`. Above `sessions_per_task_scale_up` (default 3) → +1 replica (up to `policies.max_tasks`). Below `sessions_per_task_scale_down` (default 1) AND no streaming → −1, but never below `policies.min_tasks`. Soft-deleted agents are included so orphan sessions keep scaling normally.
- **LB** ([docker.py `pick_replica`](agent-supervisor/app/backends/docker.py)): SHA256 hash of `session_id` anchors to a replica; if at capacity, walks the ring for the next available. Session stickiness prevents concurrent JSONL writes to the same session's `.claude/{session_id}/` state; shared storage means any replica can pick up if the primary is gone.
- **Idle cleanup / zero-scale** ([cleanup.py](agent-supervisor/app/services/cleanup.py)): the only path that takes replicas **below `min_tasks`**. Every `IDLE_CLEANUP_INTERVAL` (10s dev / 300s default), any agent (active or soft-deleted) whose `policies.last_activity_at` is older than `AGENT_IDLE_TIMEOUT` (20s dev / 7 days default) gets `stop_all_replicas` — volumes retained, next session-create `/run` re-spawns. Keeping this separate from the scaling loop means scale-down during active conversations is UX-neutral (floor stays at `min_tasks=1`).

## Runtime: single image, per-request mock dispatch

One image (`aviary-runtime:latest`) is used in both prod and tests. Dispatch in [server.ts](runtime/src/server.ts):

```ts
function pickProcessMessage(agentConfig) {
  return agentConfig?.mock_scenario ? processMessageMock : processMessageReal;
}
```

- **Real path** ([agent.ts](runtime/src/agent.ts)) — invokes claude-agent-sdk with LiteLLM routing via `ANTHROPIC_BASE_URL` env.
- **Mock path** ([mock-agent.ts](runtime/src/mock-agent.ts)) — executes scripted steps (`chunk`, `thinking`, `tool_use`, `write`, `read`, `ls`, `exec`, `sleep`, `error`). File I/O steps go through the **same** bwrap sandbox as prod, so session isolation, resume, and A2A are tested end-to-end.

No `RUNTIME_MODE` env var, no separate image, no docker-compose override. Tests just include `agent_config.mock_scenario` in the message body.

## Sandbox (single source of truth)

bwrap flags live in [scripts/sandbox.sh](runtime/scripts/sandbox.sh). Two entry points share it:

- **[claude-sandbox.sh](runtime/scripts/claude-sandbox.sh)** — installed as `/usr/local/bin/claude`; the SDK invokes `claude` → `sandbox.sh claude-real "$@"`.
- **mock-agent.ts `runInSandbox`** — spawns `/app/scripts/sandbox.sh <cmd>` with `SESSION_WORKSPACE`/`SESSION_CLAUDE_DIR`/`SESSION_TMP` env.

Changes to bwrap setup apply to both paths.

## Supervisor API endpoints (`/v1`)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/agents/{id}/register` | Ensure storage (volumes + subpath) |
| DELETE | `/agents/{id}` | `stop_all_replicas` (volumes retained) |
| DELETE | `/agents/{id}?purge=true` | stop replicas **and** remove the agent's workspace subpath from shared storage (used by API hard-delete) |
| POST | `/agents/{id}/run` | `scale_to(min_replicas)` |
| GET | `/agents/{id}/replicas` | List all replicas |
| GET | `/agents/{id}/ready` | Readiness summary |
| GET | `/agents/{id}/wait?timeout=N` | Long-poll until first replica ready |
| POST | `/agents/{id}/sessions/{sid}/message` | SSE proxy + Redis publish |
| POST | `/agents/{id}/sessions/{sid}/abort` | Abort active stream |
| DELETE | `/agents/{id}/sessions/{sid}` | Cleanup session workspace (broadcast to all replicas) |
| GET | `/agents/{id}/metrics` | Aggregated metrics across replicas |
| GET | `/health` | Backend health |

## Critical patterns & gotchas

### Docker network auto-detection
DockerBackend inspects its own container (`HOSTNAME` env → `self.containers.get(hostname)`) to find the compose network and spawns agent containers on the same one. Set `DOCKER_NETWORK` env to override.

### Volume ownership on first mount
Named volumes Docker-copy the image's target directory contents (including root ownership) on first mount. Three guards prevent this clobbering supervisor-prepared subpath permissions:
1. Runtime Dockerfile `chown node:node /workspace /workspace-shared`
2. `Mount(..., no_copy=True)` in DockerBackend
3. `_ensure_agent_subpath` init container `chown 1000:1000 /mnt/{agent_id}`

### bwrap + Docker seccomp
bubblewrap needs unprivileged user namespaces which the default Docker seccomp profile blocks. Agent containers are spawned with `security_opt=["seccomp=unconfined"]`.

### Sessions volume root ownership
`aviary-sessions-workspace` root is chowned to `1000:1000` once per supervisor process (tracked in `DockerBackend._chowned`) so the `node` user can mkdir session subdirs.

### Redis Pub/Sub subscribe timing
In tests, you must drain the `subscribe` confirmation message before `get_message(ignore_subscribe_messages=True, timeout=N)` will reliably return published events. See [test_e2e.py](tests/e2e/test_supervisor.py) `test_basic_streaming`.

### Session stickiness + shared storage
pick_replica is deterministic per session_id, so sequential messages go to the same replica (prevents concurrent JSONL writes to `.claude/{sid}/`). But because AGENTS_WORKSPACE_VOLUME is shared across replicas, if a replica dies or the hash remaps after scale events, any other replica sees the same history — no per-replica local state.

### Per-session mutex (runtime)
`SessionManager` gives each session its own mutex. Two messages racing on the same session serialize automatically. Verified by [test_concurrent_same_session_serialized](tests/e2e/test_supervisor.py).

## Testing

```bash
docker build -t aviary-runtime:latest ./runtime/
docker compose up -d
./scripts/test.sh        # runs both API + e2e suites end-to-end
```

`scripts/test.sh` recreates the supervisor with short loop intervals
(`SCALING_CHECK_INTERVAL=5`, `IDLE_CLEANUP_INTERVAL=10`,
`AGENT_IDLE_TIMEOUT=20`) and exports the same values so the test code
can size its waits proportionally. The compose file's defaults are
production-sensible (30 / 300 / 604800 seconds); restore them with
`docker compose up -d --force-recreate agent-supervisor` after testing.

Supervisor + runtime e2e suite ([tests/e2e/test_supervisor.py](tests/e2e/test_supervisor.py)):

1. `test_basic_streaming` — chunk/thinking/result SSE, Redis Pub/Sub realtime, Redis Streams durable
2. `test_session_isolation` — sandbox-level file isolation
3. `test_session_resume` — files persist across sequential messages
4. `test_concurrent_same_session_serialized` — per-session mutex (no interleaving)
5. `test_a2a_sharing` — cross-agent file sharing within a session; cross-session isolation
6. `test_network_policy_default_blocks_egress` — default profile: external HTTP returns `000` (blocked)
7. `test_network_policy_approved_profile_allows_egress` — `egress-open` profile: external HTTP succeeds
8. `test_abort` — abort in the middle of a sleep step
9. `test_persist_after_container_removal` — DELETE agent → re-run → state intact
10. `test_scale_up_under_load` — 5 concurrent sessions trigger replica scale-up then scale-down
11. `test_context_across_scaling` — session files survive scale-up/scale-down cycles
12. `test_rapid_agent_churn_keeps_supervisor_responsive` — concurrent agent deletes must not stall the supervisor event loop (regression: sync Docker SDK offloaded via `asyncio.to_thread`)

Full suite ~90s (bounded by scaling interval + scenario sleep durations).

### Phase 2 API suite ([tests/e2e/test_api.py](tests/e2e/test_api.py))

```bash
docker compose up -d
uv run python tests/e2e/test_api.py
```

Obtains a real JWT from Keycloak via direct-grant (dev realm, `aviary-web`
client with `directAccessGrantsEnabled: true`), then plants a session
entry directly into Redis using the API's session format. All subsequent
calls flow through the API normally.

Mock runtime: each WS message body carries optional `mock_scenario`
that the API forwards verbatim as `agent_config.mock_scenario` — purely
request-scoped so agent definitions stay clean.

Scenarios covered:
1. `test_auth_me` — cookie → `/api/auth/me` resolves the dev user
2. `test_agent_crud` — create/list/get/patch/delete
3. `test_agent_create_does_not_spawn` — agent creation alone must not start any replica
4. `test_session_create_triggers_spawn` — "New Session" prewarms a replica via `/run`
5. `test_ws_message_round_trip` — user message → chunks → done + persisted to DB
6. `test_ws_reconnect_replay` — disconnect mid-stream → reconnect replays buffered events
7. `test_ws_cancel` — `{type: cancel}` → cancelled event
8. `test_multi_session_isolation` — two concurrent sessions on one agent produce isolated responses
9. `test_session_delete_blocks_reaccess` — DELETE session → 404 on re-GET + WS rejected
10. `test_delete_one_session_keeps_other_working` — partial session delete leaves siblings functional
11. `test_agent_delete_with_no_sessions_hard_deletes` — DELETE on an agent with zero sessions skips soft-delete
12. `test_agent_soft_delete_keeps_orphan_sessions_working` — soft-deleted agent: detail viewable, new sessions blocked, orphan sessions chat
13. `test_last_session_delete_hard_deletes_soft_deleted_agent` — cascade: final session delete hard-deletes agent + replicas + workspace
14. `test_idle_agent_zero_scales` — idle_cleanup drops replicas to 0 once `last_activity_at` exceeds `AGENT_IDLE_TIMEOUT`
15. `test_unauthorized_ws` — missing/wrong origin or cookie rejected at handshake

## Key env vars

| Service | Var | Default | Notes |
|---------|-----|---------|-------|
| agent-supervisor | `RUNTIME_BACKEND` | `docker` | `docker` or `fargate` |
| agent-supervisor | `RUNTIME_IMAGE` | `aviary-runtime:latest` | Image for spawned containers |
| agent-supervisor | `AGENT_PRIMARY_NETWORK` | `aviary-agent-default` | Internal network every agent joins (egress blocked) |
| agent-supervisor | `DATABASE_URL` | postgres://... | asyncpg DSN |
| agent-supervisor | `REDIS_URL` | redis://redis:6379/0 | Pub/Sub + Streams |
| agent-supervisor | `SCALING_CHECK_INTERVAL` | 5 (dev) / 30 | Scaling loop period (seconds) |
| agent-supervisor | `IDLE_CLEANUP_INTERVAL` | 30 (dev) / 300 | Idle scan period (seconds) |
| agent-supervisor | `AGENT_IDLE_TIMEOUT` | 604800 | 7 days |
| agent-supervisor | `MAX_CONCURRENT_SESSIONS_PER_TASK` | 5 | Per-container session cap |
| agent-supervisor | `LITELLM_API_KEY` | `sk-aviary-dev` | Forwarded to runtime as `ANTHROPIC_API_KEY` |
| litellm | `LITELLM_MASTER_KEY` | `sk-aviary-dev` | Must match `LITELLM_API_KEY` |
| runtime | `AGENT_ID` | required | UUID from supervisor |
| runtime | `MAX_CONCURRENT_SESSIONS` | 5 | From supervisor settings |
| runtime | `INFERENCE_ROUTER_URL` | `http://litellm:4000` | Used as `ANTHROPIC_BASE_URL` by claude-agent-sdk |
| runtime | `ANTHROPIC_API_KEY` | "" | Injected by supervisor from `LITELLM_API_KEY` |
| api | `SUPERVISOR_URL` | `http://agent-supervisor:9000` | Internal supervisor endpoint |
| api | `OIDC_ISSUER` | `http://localhost:8180/realms/aviary` | Browser-facing issuer URL |
| api | `OIDC_INTERNAL_ISSUER` | `http://keycloak:8080/realms/aviary` | Container-to-container URL |
| api | `OIDC_CLIENT_ID` | `aviary-web` | Public client in Keycloak realm |
| api | `CORS_ORIGINS` | `["http://localhost:3000"]` | SPA origins (JSON array) |
| api | `COOKIE_SECURE` | `false` | Set true behind HTTPS |

## DB schema (Phase 1, simplified from v1)

- `agents` — id, name, slug, owner_id, instruction, model_config (JSONB), tools (JSONB), status, policy_id
- `policies` — id, min_tasks, max_tasks, resource_limits (JSONB), policy_rules (JSONB — includes `network.extra_networks`, `network.subnets`), last_activity_at
- `sessions` — id, agent_id, created_by, title, status, last_message_at
- `messages` — id, session_id, sender_type, sender_id, content, metadata (JSONB)

Phase 2 will add back users/teams, ACL, workflows, MCP tool catalog from v1.

## Project layout

```
api/                  # User-facing API (FastAPI, OIDC, WebSocket chat)
agent-supervisor/     # Internal supervisor (container lifecycle, SSE relay)
runtime/              # Runtime container (claude-agent-sdk + mock + bwrap)
shared/               # SQLAlchemy models, OIDC validator, redis/http helpers
tests/                # Test suites — `tests/e2e/` for whole-stack tests; `tests/unit/` reserved for future
config/litellm/       # LiteLLM gateway config
config/keycloak/      # Keycloak realm auto-import (dev)
scripts/              # setup-dev.sh, init-db.sql, llama-swap/ (dev-only host-run LLM)
docker-compose.yml    # postgres, redis, db-migrate, keycloak, api, agent-supervisor, litellm
v1/                   # Archived v1 codebase (reference only)
```

## API module boundaries

[api/app/](api/app/) is layered to keep future features additive:

- **[routers/](api/app/routers/)** — thin HTTP/WS handlers. No business logic.
- **[services/](api/app/services/)** — one module per concern: `agents`, `sessions`, `supervisor` (typed client), `stream/` (package).
- **[services/stream/](api/app/services/stream/)** — all authoritative state lives in Redis so any API replica can service any stream:
  - `events.py` — wire constants.
  - `buffer.py` — Pub/Sub + list-based replay buffer + a control channel for cross-pod signals, keyed by an opaque `stream_id` (currently `== session_id`; future workflow runs can use composite ids without changes here).
  - `lock.py` — exclusive per-`stream_id` lease with a fencing token (CAS Lua for refresh/release). TTL-bounded so a crashed holder's lease expires automatically.
  - `manager.py` — cancel-and-replace start (publishes `cancel` on the control channel, waits up to 3 s for the previous owner to release, then acquires the lease), background `_run` that heartbeats the lease every 5 s while streaming and always releases it in `finally`.
  - `relay.py` — WebSocket ↔ Pub/Sub forwarding + reconnect replay (reads buffer state via Redis, no pod-local coupling).
- **[auth/](api/app/auth/)** — `oidc.py` (singleton validator), `session_store.py` (Redis-backed sessions with auto-refresh), `dependencies.py` (single entry point: `get_current_user` for REST, `authenticate_ws` for WebSocket).
- **[schemas/](api/app/schemas/)** — Pydantic request/response contracts, isolated from SQLAlchemy models.

Design choices vs v1: (1) typed supervisor client instead of scattered httpx calls, (2) single auth dependency covering REST and WS instead of duplicating cookie parsing, (3) stream package split into producer (`manager`) + consumer (`relay`) + transport (`buffer`) so each can be replaced in isolation, (4) **no process-local streaming state** — the API is horizontally scalable because the lease, control channel, and replay buffer all live in Redis; cross-pod cancel/preempt/reconnect work uniformly regardless of which replica serves each request.

Pod-identity helper: [api/app/identity.py](api/app/identity.py) returns a
`hostname-{uuid8}` stable per process boot. Works identically on Docker
(container short id) and ECS/Fargate (task DNS), so lock holder-ids stay
meaningful after backend swap without any business-logic changes.

## Commit & workflow conventions

- Single-line commit messages, no Co-Authored-By footer.
- Always use `uv run` for Python commands (never direct pip/python).
- Default to writing no comments. Only add when the WHY is non-obvious.
- No fallback logic or hardcoded maps — reject bad requests, fix root causes.
- Weigh code/dep cost against proven workflow need before proposing power-user features.
