# Aviary

**Multi-Tenant AI Agent Platform**

[한국어](./README.ko.md)

Aviary is an enterprise platform where users create, configure, and chat with purpose-built AI agents through a web UI. Runtime environments are pre-provisioned as Helm releases (one Deployment pool per environment) in the Kubernetes cluster; every other service — API, admin console, agent supervisor — ships through the normal deploy unit (docker-compose in dev, whatever your platform uses in prod). Agents are isolated at the kernel level via bubblewrap and at the network level via a baseline NetworkPolicy plus optional per-environment rules.

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                        Web UI (Next.js)                        │
│       Agent Catalog · Create/Edit · Chat Sessions              │
└───────────────┬────────────────────────────────────────────────┘
                │ REST + WebSocket
┌───────────────▼────────────────────────────────────────────────┐
│                     API Server (FastAPI)                        │
│   OIDC Auth · Agent CRUD · Session Mgr · A2A (owner-only)       │
│   (reads agent.runtime_endpoint, forwards to supervisor)        │
└───┬───────────┬────────────────────────────────────────────────┘
    │           │                           ┌────────────────────┐
    │           │                           │  Admin Console     │
    │           │                           │  Agent config +    │
    │           │                           │  runtime_endpoint  │
    │           │                           │  override          │
    │           │                           └──────────┬─────────┘
    │   ┌───────▼────────────────────────────────────┐ │
    │   │            Platform Services              │ │
    │   │                                            │ │
    │   │  ┌──────────────────────────────────────┐ │ │
    │   │  │ LiteLLM Gateway (:8090)              │ │ │
    │   │  │  • /v1/messages → model routing      │◀┼─┤ Vault
    │   │  │    (per-user Anthropic key inject)   │ │ │ (API keys +
    │   │  │  • /mcp → aggregated MCP             │ │ │  tool creds)
    │   │  │    (JWT auth · tool-list filter ·    │ │ │
    │   │  │     tools/call Vault injection)      │ │ │
    │   │  └─────┬────────────────────┬───────────┘ │ │
    │   │        │                    │             │ │
    │   │  ┌─────▼─────┐ ┌────────┐ ┌─▼──────────┐ │ │
    │   │  │Claude API │ │Ollama  │ │Backend MCP │ │ │
    │   │  │Bedrock    │ │vLLM    │ │servers     │ │ │
    │   │  └───────────┘ └────────┘ └────────────┘ │ │
    │   └───────────────────────────────────────────┘ │
    │   ┌────────────────────────────────────────────────────────┐
    │   │    Agent Supervisor (docker-compose, not in K8s)       │
    │   │      SSE reverse proxy · Redis publish · assemble       │
    │   │      in-memory abort registry · /metrics               │
    │   │      (caller passes runtime_endpoint per request)      │
    │   └───────────────────────┬────────────────────────────────┘
    │                           │ HTTP via env Service (NodePort dev / ClusterIP prod)
    │   ┌───────────────────────▼────────────────────────────────┐
    │   │                   Kubernetes Cluster                    │
    │   │          (local: K3s · prod: EKS, via Helm)             │
    │   │                                                        │
    │   │  ┌─── NS: agents ────────────────────────────────────┐ │
    │   │  │ baseline NetworkPolicy (DNS + platform +          │ │
    │   │  │   LiteLLM + API + Supervisor)                     │ │
    │   │  │ shared RWX PVC: aviary-shared-workspace           │ │
    │   │  │   (every env's Deployment mounts this)            │ │
    │   │  │                                                   │ │
    │   │  │ Env release: aviary-env-default                   │ │
    │   │  │   Deployment (replicas ≥ 1) · Service             │ │
    │   │  │   runtime pool serves every agent                 │ │
    │   │  │   bwrap sandbox · shared session dir ·            │ │
    │   │  │   per-(agent,session) .claude and .venv           │ │
    │   │  │                                                   │ │
    │   │  │ Env release: aviary-env-custom  (example)         │ │
    │   │  │   Same shape · open egress · image w/ gh CLI      │ │
    │   │  └───────────────────────────────────────────────────┘ │
    │   └─────────────────────────────────────────────────────────┘
    │
    └─▶ PostgreSQL · Redis · Keycloak · Vault
```

## Key Features

- **Pre-Provisioned Runtime Environments** — Helm releases of `charts/aviary-environment` stand up the runtime pool declaratively; no per-agent Deployments, no cold starts. Environments are always on.
- **Supervisor Outside K8s** — Agent Supervisor ships through the normal deploy unit alongside API/Admin (docker-compose in dev). It reaches runtime pools via a regular Service endpoint; the only thing running in K8s is the agent runtime pool (GitOps range ≈ K8s range).
- **Client-Targeted Abort** — Supervisor keeps an in-memory registry keyed by `stream_id`; the frontend learns the id from a `stream_started` broadcast, sends it back on cancel, and the supervisor cancels the matching task. TCP closes to the pod, the pod's `res.on("close")` fires, the SDK aborts. No pod-IP tracking, no Redis signal on the runtime side.
- **Per-Agent Endpoint Override** — Agents share the default environment; the Admin Console sets `runtime_endpoint` on an agent to route it to another environment (e.g. an open-internet pool, a GPU pool) — no code change, no DB migration. Sessions migrate between environments without losing conversation history because the workspace PVC is cluster-wide shared.
- **Agent-Agnostic Runtime Pool** — Every pod in an environment serves every agent; `agent_id` arrives per-request in `agent_config`. Isolation comes from on-disk paths (`sessions/{sid}/agents/{aid}/…`) plus bubblewrap, not per-agent pods.
- **Bubblewrap Session Isolation** — Each request runs inside a kernel-level mount namespace; agents in the same session share `/workspace` for file exchange (A2A), while `.claude/` and `.venv/` are per-(agent, session).
- **Agent-to-Agent (A2A)** — Agents invoke other agents via `@mention` in instructions or chat messages; sub-agent tool calls render inline under the parent tool card in real-time. The runtime's A2A MCP server calls the supervisor directly (no API hop).
- **Unified Gateway (LiteLLM)** — [LiteLLM](https://github.com/BerriAI/litellm) is the single gateway for both LLM inference and [MCP](https://modelcontextprotocol.io/) tool aggregation. Model routing by name prefix (Anthropic / Ollama / vLLM / Bedrock) with per-user Anthropic key injection from Vault; `/mcp` aggregates every registered MCP server behind one endpoint with Keycloak JWT validation, per-user Vault-backed tool credential injection, and a guardrail extension point for future RBAC. Admins register MCP servers directly against LiteLLM (it's the catalog SoT); users bind a per-agent subset from their accessible tools.
- **Proxy UI (LiteLLM)** — DB-backed (`:8090/ui`) for keys, teams, spend, and model registry.
- **Multi-Backend Inference** — Claude API, Ollama, vLLM, AWS Bedrock; add new backends via config.
- **Layered Egress** — Baseline NetworkPolicy from `charts/aviary-platform` is always in effect; per-environment `extraEgress` in Helm values adds additional rules (K8s NP evaluates as a disjunction).
- **Redis-Decoupled Streaming** — Supervisor consumes runtime SSE and publishes to Redis; API server saves the assembled message and WebSocket clients replay from the same Redis stream independently.
- **Live Config** — Agent instruction, tools, and MCP server bindings update from DB on every message turn — no Pod restarts.
- **OIDC Auth** — Keycloak/Okta-backed login. The current access model is **owner-only**: each agent/session/workflow is visible/mutable only to its creator. RBAC (teams, roles, sharing) is scheduled as a first-class redesign.
- **Vault Secrets** — Per-user API keys and tool credentials injected at gateway level, never exposed to Pods.
- **Observability** — Supervisor exposes `/metrics`. A Prometheus + Grafana stack ships in docker-compose with a pre-provisioned dashboard (in-flight streams, request rate / error ratio, p50/p95/p99 latency, TTFB, SSE event mix, dependency health).
- **Local K3s / Prod EKS** — Same Helm charts on both. The shared workspace PVC backs onto a hostPath PV in dev and EFS (RWX) in prod — a platform-chart values flip.

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Web UI | Next.js 15, TypeScript, Tailwind CSS, shadcn/ui |
| API Server | Python, FastAPI, SQLAlchemy 2.0 (async), Pydantic v2 |
| Agent Runtime | Node.js, Python, claude-agent-sdk, Claude Code CLI |
| Agent Supervisor | Python, FastAPI, Redis, prometheus-client |
| LLM + MCP Gateway | [LiteLLM](https://github.com/BerriAI/litellm) with Aviary patches (`aviary_user_api_key.py`, `aviary_mcp_credentials.py`) |
| MCP Protocol | [MCP SDK](https://github.com/modelcontextprotocol/python-sdk) (used by runtime + platform MCP server stubs) |
| Deployment | Helm charts (`charts/aviary-platform`, `charts/aviary-environment`) |
| Infrastructure | PostgreSQL, Redis, Keycloak, Vault, Kubernetes (K3s for dev, EKS for prod) |

## Project Structure

```
aviary/
├── api/                  # API Server — user-facing REST + WebSocket + A2A
├── admin/                # Admin Console — operator-facing web UI (config + endpoint override)
├── web/                  # Web UI (Next.js)
├── runtime/              # Agent Runtime — agent-agnostic pool member
├── shared/               # Shared package (OIDC, DB models)
├── agent-supervisor/     # Stateless SSE proxy + Redis publisher + /metrics
├── mcp-servers/          # Platform-provided MCP server stubs (aggregated via LiteLLM /mcp)
├── charts/
│   ├── aviary-platform/      # Namespaces, baseline egress, shared workspace PVC, image-warmer
│   └── aviary-environment/   # One runtime environment (Deployment + Service + optional NP)
├── config/               # LiteLLM, Keycloak, K3s config
├── scripts/              # Dev setup and utilities
└── docker-compose.yml
```

## Getting Started

**Prerequisites:** Docker Desktop (or equivalent container runtime)

```bash
git clone <repository-url>
cd aviary
./scripts/setup-dev.sh
```

This single command builds all images, starts all services, runs DB migrations, and installs the Helm charts (`aviary-platform`, `aviary-env-default`, and the `aviary-env-custom` example release).

| Service | URL |
|---------|-----|
| Web UI | http://localhost:3000 |
| API Server | http://localhost:8000 |
| Admin Console | http://localhost:8001 |
| Agent Supervisor / metrics | http://localhost:9000 · http://localhost:9000/metrics |
| LiteLLM Proxy / UI | http://localhost:8090 · http://localhost:8090/ui (admin / admin) |
| Grafana (dashboards) | http://localhost:3001 |
| Prometheus | http://localhost:9090 |
| Keycloak Admin | http://localhost:8080 |

Test accounts: `user1@test.com` / `user2@test.com` (password: `password`)

```bash
docker compose up -d          # Start
docker compose down           # Stop (data preserved)
docker compose down -v        # Full reset
```

Source code is bind-mounted — edits to `api/`, `admin/`, and `web/` hot-reload.

### Adding a custom environment

The repo already ships a worked example in `charts/aviary-environment/values-custom.yaml` + `runtime/Dockerfile.custom` — a release named `aviary-env-custom` with open outbound egress and a runtime image that layers `gh` CLI on top of the base. Use it as a template:

1. Copy `values-custom.yaml` → `values-<your-name>.yaml`; adjust `name`, `service.nodePort`, `extraEgress`, and (optionally) `image.repository`.
2. If you need extra tooling in the pod, copy `Dockerfile.custom` → `Dockerfile.<your-name>`, build+tag+load into K3s, and point the chart at it.
3. Render & apply:
   ```bash
   docker run --rm -v "$PWD/charts:/charts:ro" alpine/helm:3.14.4 template \
     aviary-env-<your-name> /charts/aviary-environment \
     -f /charts/aviary-environment/values-<your-name>.yaml \
     | docker compose exec -T k8s kubectl apply -f -
   ```
4. In the Admin Console → agent detail, set **Runtime Endpoint Override** to `http://k8s:<nodePort>` (dev) or the env's in-cluster Service DNS (prod). The next chat message routes to the new pool; ongoing sessions keep their history because all envs mount the same workspace PVC.

## Testing

```bash
docker compose exec api pytest tests/ -v
docker compose exec admin pytest tests/ -v
cd agent-supervisor && uv run pytest tests/ -v
```

## Key Design Decisions

### Supervisor Outside K8s, Endpoint Injection
The supervisor ships through the normal deploy unit alongside API/Admin (docker-compose in dev) — not a K8s workload. It owns no DB connection and no K8s API access. Every caller (today the API server; tomorrow a Temporal worker or batch job) looks up `agent.runtime_endpoint` and passes it in the publish request body. Null falls back to a configured default (dev: `http://k8s:30300`, the K3s NodePort for the default env; prod: env Service DNS or LB URL). The only thing in K8s/GitOps scope is the runtime pool.

### Client-Targeted Abort
kube-proxy load-balances at connect time, so the supervisor → runtime TCP connection is pinned to one pod for its lifetime. The frontend learns its `stream_id` from the `stream_started` Redis broadcast and echoes it back on cancel; the supervisor cancels the matching task, the httpx context exits, TCP closes, the runtime pod's `res.on("close")` fires (streaming responses don't reliably fire `req.on("close")` in Node/Express — we learned this the hard way), and the SDK aborts. No pod-IP tracking, no Redis on the runtime side, no special K8s positioning required.

### Helm-Declared Environments
Runtime infrastructure lives entirely in `charts/aviary-environment` (per-env Deployment + Service + optional NetworkPolicy) and `charts/aviary-platform` (namespaces, baseline egress, shared workspace PVC). Spinning up a new environment = `helm template | kubectl apply`. Local dev differs from production only in a values file (hostPath PV vs. EFS, NodePort vs. LoadBalancer). There are no dynamic K8s operations in application code.

### Shared Workspace PVC
All envs mount a single cluster-wide RWX PVC (`aviary-shared-workspace`). Envs are capability boundaries (image + egress); data boundaries are `(agent_id, session_id)` on-disk paths. Swapping an agent's `runtime_endpoint` to another env keeps the conversation history intact — Claude CLI JSONL, shared files, and per-(agent, session) venv all live on the one PVC.

### Unified LiteLLM Gateway (inference + MCP)
Agent Pods never call LLM backends or MCP servers directly. [LiteLLM](https://github.com/BerriAI/litellm) is the single gateway:

- **`/v1/messages`** — model routing by name prefix (Claude API, Ollama, vLLM, Bedrock) with per-user Anthropic API-key injection from Vault. The `aviary_user_api_key.py` custom hook validates the user's Keycloak JWT and swaps in their personal key for Anthropic calls.
- **`/mcp`** — aggregated [MCP](https://modelcontextprotocol.io/) Streamable-HTTP endpoint fanning out to every backend MCP server (YAML-declared public servers + Admin-registered dynamic ones in LiteLLM's Prisma DB). Admin writes directly to LiteLLM via `POST /v1/mcp/server` with the master key — LiteLLM is the MCP catalog source of truth, Aviary DB only stores per-agent tool bindings.
- **Security (all in `aviary_mcp_credentials.py`):** request-ingress JWT gate (plugs LiteLLM OSS's OAuth2-passthrough fail-open), per-agent tool visibility filter via `X-Aviary-Allowed-Tools` header, Vault-backed tool credential injection on `tools/call`, schema stripping so injected args never reach the model, and a no-op RBAC stub (`_rbac_filter_tools`) as the documented extension point for future per-user access control.
- **UI** — LiteLLM's proxy UI (`:8090/ui`) is DB-backed for keys/teams/spend/model registry.

### Agent-to-Agent (A2A)
Agents call other agents as sub-agents via `@mention`. The runtime exposes a per-message HTTP MCP server with one tool per accessible agent. A2A calls route **runtime → supervisor** directly (the supervisor validates the Bearer JWT and resolves the sub-agent's runtime_endpoint), skipping the API hop. Sub-agent tool calls publish to the parent session's Redis channel and render inline. Agents in the same session share `/workspace` via the shared workspace PVC.

### Session Isolation
Claude CLI runs inside a bubblewrap mount namespace. The shared PVC is structured as `/workspace-root/sessions/{sid}/shared/` (cross-agent session area) and `/workspace-root/sessions/{sid}/agents/{aid}/{.claude,.venv}/` (per-(agent, session) overlays). bwrap rewrites these onto `/workspace`, `/workspace/.claude`, `/workspace/.venv` and tmpfs-masks `/workspace-root` so sibling sessions are invisible inside the sandbox.

### Access Model (current)
Owner-only: each agent, session, and workflow is visible and mutable only to its creator. There are no teams, no shared visibility, no platform-admin role. RBAC (teams, roles, invited participants) will return as a dedicated redesign rather than as patches on the old ACL tables.
