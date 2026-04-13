# Aviary

Multi-tenant AI agent platform. Users will create/configure agents via Web UI; each agent runs in isolated runtime containers (Docker locally, Fargate in production) driven by claude-agent-sdk.

## Rearchitecting status (in progress)

The codebase is being rewritten from scratch. v1 used K3s-in-Docker + per-agent namespaces/PVCs/NetworkPolicy; the rewrite replaces that with a `RuntimeBackend` abstraction that runs on plain Docker locally and AWS Fargate in production. **The v1 code is archived at [v1/](v1/) for reference** (also tagged `v1-archive` in git).

### Phase 1 — decoupled runtime (DONE)
- Agent Supervisor with `RuntimeBackend` abstraction ([DockerBackend](agent-supervisor/app/backends/docker.py), [FargateBackend stub](agent-supervisor/app/backends/fargate.py))
- Multi-replica per agent, consistent-hash load balancing, auto-scaling, idle cleanup
- Shared-volume storage model (subpath per agent) that maps 1:1 to Fargate + EFS Access Points
- Runtime with scenario-driven mock mode for integration tests (same image as prod)
- End-to-end test suite covering streaming, isolation, resume, A2A, abort, scaling, mutex, persistence

### Phase 2 — next up (not yet started)
- API server rewrite (OIDC, WebSocket streaming from Redis Pub/Sub, agent CRUD, sessions)
- Web frontend reconnect
- LLM gateway + MCP gateway reintroduction
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

## Services (Phase 1)

Only the runtime execution layer is active. Docker Compose (`docker-compose.yml`) runs:

- **PostgreSQL** (`:5432`) — agents, policies, sessions, messages
- **Redis** (`:6379`) — Pub/Sub (realtime streaming) + Streams (durable events)
- **Agent Supervisor** (`:9000`) — container lifecycle + API
- **db-migrate** — Alembic migrations (one-shot)

Not in Phase 1: Keycloak, Vault, LiteLLM, Portkey, MCP gateway, web, admin, K3s, egress proxy.

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

## Multi-replica + load balancing

Containers named `aviary-agent-{agent_id}-{replica_idx}`. Labels: `aviary.agent-id`, `aviary.replica`, `aviary.managed=true`, `aviary.role=agent-runtime`.

- **Scaling** ([scaling.py](agent-supervisor/app/services/scaling.py), ported from v1): every `SCALING_CHECK_INTERVAL` (5s dev / 30s default), compute `sessions_per_replica = sum(sessions_active) / replicas`. Above `sessions_per_task_scale_up` (default 3) → +1 replica (up to `policies.max_tasks`). Below `sessions_per_task_scale_down` (default 1) AND no streaming → −1 (down to `policies.min_tasks`).
- **LB** ([docker.py `pick_replica`](agent-supervisor/app/backends/docker.py)): SHA256 hash of `session_id` anchors to a replica; if at capacity, walks the ring for the next available. Session stickiness prevents concurrent JSONL writes to the same session's `.claude/{session_id}/` state; shared storage means any replica can pick up if the primary is gone.
- **Idle cleanup** ([cleanup.py](agent-supervisor/app/services/cleanup.py)): every `IDLE_CLEANUP_INTERVAL` (30s dev / 300s default), agents whose `policies.last_activity_at` is older than `AGENT_IDLE_TIMEOUT` (7 days) get `stop_all_replicas` — volumes retained, containers go away. Next message triggers re-creation.

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
In tests, you must drain the `subscribe` confirmation message before `get_message(ignore_subscribe_messages=True, timeout=N)` will reliably return published events. See [test_e2e.py](test-client/test_e2e.py) `test_basic_streaming`.

### Session stickiness + shared storage
pick_replica is deterministic per session_id, so sequential messages go to the same replica (prevents concurrent JSONL writes to `.claude/{sid}/`). But because AGENTS_WORKSPACE_VOLUME is shared across replicas, if a replica dies or the hash remaps after scale events, any other replica sees the same history — no per-replica local state.

### Per-session mutex (runtime)
`SessionManager` gives each session its own mutex. Two messages racing on the same session serialize automatically. Verified by [test_concurrent_same_session_serialized](test-client/test_e2e.py).

## Testing

```bash
docker build -t aviary-runtime:latest ./runtime/
docker compose up -d
uv run python test-client/test_e2e.py
```

Test suite ([test-client/test_e2e.py](test-client/test_e2e.py)):

1. `test_basic_streaming` — chunk/thinking/result SSE, Redis Pub/Sub realtime, Redis Streams durable
2. `test_session_isolation` — sandbox-level file isolation
3. `test_session_resume` — files persist across sequential messages
4. `test_concurrent_same_session_serialized` — per-session mutex (no interleaving)
5. `test_a2a_sharing` — cross-agent file sharing within a session; cross-session isolation
6. `test_abort` — abort in the middle of a sleep step
7. `test_persist_after_container_removal` — DELETE agent → re-run → state intact
8. `test_scale_up_under_load` — 5 concurrent sessions trigger replica scale-up then scale-down
9. `test_context_across_scaling` — session files survive scale-up/scale-down cycles

Full suite ~90s (bounded by scaling interval + scenario sleep durations).

## Key env vars

| Service | Var | Default | Notes |
|---------|-----|---------|-------|
| agent-supervisor | `RUNTIME_BACKEND` | `docker` | `docker` or `fargate` |
| agent-supervisor | `RUNTIME_IMAGE` | `aviary-runtime:latest` | Image for spawned containers |
| agent-supervisor | `DOCKER_NETWORK` | auto | Defaults to supervisor's own compose network |
| agent-supervisor | `DATABASE_URL` | postgres://... | asyncpg DSN |
| agent-supervisor | `REDIS_URL` | redis://redis:6379/0 | Pub/Sub + Streams |
| agent-supervisor | `SCALING_CHECK_INTERVAL` | 5 (dev) / 30 | Scaling loop period (seconds) |
| agent-supervisor | `IDLE_CLEANUP_INTERVAL` | 30 (dev) / 300 | Idle scan period (seconds) |
| agent-supervisor | `AGENT_IDLE_TIMEOUT` | 604800 | 7 days |
| agent-supervisor | `MAX_CONCURRENT_SESSIONS_PER_TASK` | 5 | Per-container session cap |
| runtime | `AGENT_ID` | required | UUID from supervisor |
| runtime | `MAX_CONCURRENT_SESSIONS` | 5 | From supervisor settings |
| runtime | `INFERENCE_ROUTER_URL` | `https://api.anthropic.com` | Real agent path (Phase 2 will point to LiteLLM) |
| runtime | `ANTHROPIC_API_KEY` | "" | Real agent path only |

## DB schema (Phase 1, simplified from v1)

- `agents` — id, name, slug, owner_id, instruction, model_config (JSONB), tools (JSONB), status, policy_id
- `policies` — id, min_tasks, max_tasks, resource_limits (JSONB), policy_rules (JSONB), last_activity_at
- `sessions` — id, agent_id, created_by, title, status, last_message_at
- `messages` — id, session_id, sender_type, sender_id, content, metadata (JSONB)

Phase 2 will add back users/teams, ACL, workflows, MCP tool catalog from v1.

## Project layout

```
agent-supervisor/     # Supervisor service (Phase 1 rewrite)
runtime/              # Runtime container (claude-agent-sdk + mock + bwrap)
shared/               # SQLAlchemy models, Redis utils, naming helpers
test-client/          # End-to-end test suite
scripts/              # setup-dev.sh, init-db.sql
docker-compose.yml    # Phase 1 stack: postgres, redis, db-migrate, agent-supervisor
v1/                   # Archived v1 codebase (reference only)
```

## Commit & workflow conventions

- Single-line commit messages, no Co-Authored-By footer.
- Always use `uv run` for Python commands (never direct pip/python).
- Default to writing no comments. Only add when the WHY is non-obvious.
- No fallback logic or hardcoded maps — reject bad requests, fix root causes.
- Weigh code/dep cost against proven workflow need before proposing power-user features.
