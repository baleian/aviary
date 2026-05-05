# Aviary

**A self-hosted platform for building, running, and orchestrating AI agents.**

[한국어](./README.ko.md)

Aviary lets a team build their own AI agents in a web UI — give an agent an instruction, a model, and a set of tools (including [MCP](https://modelcontextprotocol.io/) servers), then chat with it or chain several agents into a workflow. Agents run inside a sandboxed runtime container, so it is safe to let them touch code, data, and the internet.

The platform is designed to drop into an existing organization: it plugs into your IdP (any OIDC provider), your secrets store (HashiCorp Vault), and your model providers (Anthropic, AWS Bedrock, Ollama, vLLM, …).

## Highlights

- **Web UI for the whole lifecycle** — create, configure, chat with, and delete agents; compose them into workflows; review past runs.
- **Bring your own models** — switch between hosted Anthropic, AWS Bedrock, and self-hosted Ollama or vLLM by changing a model name.
- **First-class MCP tool integration** — register MCP servers, pick which tools each agent can use, and inject per-user credentials from Vault.
- **Agent-to-Agent (A2A)** — agents can call other agents as sub-tools via `@mention`; the sub-agent's activity renders inline in the parent conversation.
- **Workflows** — chain agents and deterministic steps into DAGs on top of [Temporal](https://temporal.io/); resume, replay, and inspect runs from the UI.
- **Safe by default** — every agent turn runs inside a [bubblewrap](https://github.com/containers/bubblewrap) sandbox.
- **Multi-tenant, per-user** — OIDC login, per-user API keys and tool credentials in Vault, isolated workspaces per session.

## Architecture

```
        ┌──────────────────────────────────────────────────────┐
        │                      Web UI                          │
        │     Agents · Workflows · Chat · Runs · Admin         │
        └──────────────────────────┬───────────────────────────┘
                                   │ single origin
                                   │ (REST + WebSocket)
                  ┌────────────────▼────────────────┐
                  │       Edge proxy (Caddy)        │
                  │   /api/* → API ·  /* → Web      │
                  └──────┬──────────────────┬───────┘
                         │                  │
        ┌────────────────▼───────┐  ┌───────▼─────────────────┐
        │      API Server        │  │      Admin Console      │
        │   Auth · CRUD · Chat   │  │  Agent / workflow defs  │
        └──────┬─────────────┬───┘  └─────────────────────────┘
               │             │
               │    ┌────────▼─────────────────────────────────┐
               │    │           Platform Services              │
               │    │   LiteLLM Gateway                        │
               │    │    ├─ LLM routing (Anthropic / Bedrock / │
               │    │    │   Ollama / vLLM …)                  │
               │    │    └─ Aggregated MCP endpoint            │
               │    │   Vault · Keycloak · Postgres · Redis    │
               │    │   Temporal · Prometheus · Grafana        │
               │    └──────────────────┬───────────────────────┘
               │                       │
        ┌──────▼───────────────────┐   │
        │    Agent Supervisor      │   │
        │   SSE proxy · abort ·    │   │
        │   metrics                │   │
        └──────────────┬───────────┘   │
                       │ HTTP          │
        ┌──────────────▼───────────────▼───────────────────────┐
        │              Agent Runtime                           │
        │   In-compose `runtime` container — agent-agnostic;   │
        │   isolation is per-request via bubblewrap + per-     │
        │   session workspace paths.                           │
        └──────────────────────────────────────────────────────┘
```

## Components

| Component | What it does |
|-----------|--------------|
| **Web UI** ([web/](web/)) | Next.js frontend for end users and operators. |
| **API Server** ([api/](api/)) | OIDC-authenticated REST + WebSocket API for agents, sessions, messages, and workflows. |
| **Admin Console** ([admin/](admin/)) | Local-only operator UI for agent and workflow definitions. |
| **Agent Supervisor** ([agent-supervisor/](agent-supervisor/)) | Streams agent output from the runtime, fans events out via Redis, injects per-user credentials, handles abort. |
| **Workflow Worker** ([workflow-worker/](workflow-worker/)) | Temporal worker that drives workflow execution. |
| **Agent Runtime** ([runtime/](runtime/)) | Node.js + [claude-agent-sdk](https://github.com/anthropics/claude-agent-sdk-typescript) container that actually runs the agent. |
| **LiteLLM Gateway** ([local-infra/config/litellm/](local-infra/config/litellm/)) | Single entry point for both LLM inference and MCP tool calls; routes by model name and injects per-user secrets. |
| **Shared Python package** ([shared/](shared/)) | SQLAlchemy models, migrations, and OIDC helpers used by API + Admin + Supervisor. |

## Tech stack

| Layer | Technology |
|-------|-----------|
| Web UI | Next.js, TypeScript, Tailwind CSS, shadcn/ui |
| API + Admin + Supervisor | Python, FastAPI, SQLAlchemy 2.0 (async), Pydantic v2 |
| Agent Runtime | Node.js, [claude-agent-sdk](https://github.com/anthropics/claude-agent-sdk-typescript), Claude Code CLI |
| Workflows | [Temporal](https://temporal.io/) |
| LLM + MCP Gateway | [LiteLLM](https://github.com/BerriAI/litellm) |
| Identity | OIDC (any provider; [Keycloak](https://www.keycloak.org/) shipped for local) |
| Secrets | [HashiCorp Vault](https://www.vaultproject.io/) |
| Data | PostgreSQL, Redis |
| Observability | OpenTelemetry, Prometheus, Grafana |
| Sandbox | [bubblewrap](https://github.com/containers/bubblewrap) |

## Installation

### Prerequisites

- Docker (Docker Desktop, OrbStack, Rancher Desktop, …) with `docker compose v2`
- ~10 GB free disk for images and volumes
- Linux / macOS / WSL2

### Repo layout — two compose stacks

| Stack / command | What it covers | Source |
|-----------------|----------------|--------|
| `service` group | web, api, admin, supervisor, workflow-worker, runtime, proxy | Project root [compose.yml](compose.yml) |
| `infra` group | postgres, redis, temporal, temporal-ui, db-migrate, keycloak, vault, litellm, prometheus, grafana, otel-collector, sample MCP servers | [local-infra/compose.yml](local-infra/compose.yml) |

`infra` is required (postgres / redis / temporal live there); `service` adds the user-facing components on top.

### One-shot bring-up

```bash
git clone <repository-url>
cd aviary
cp .env.example .env                  # edit if you need to override anything
./scripts/setup-dev.sh                # build + start infra + service
```

`setup-dev.sh` accepts a comma-separated subset of `infra` / `service`:

```bash
./scripts/setup-dev.sh infra                # platform deps only (postgres, redis, temporal, keycloak, …)
./scripts/setup-dev.sh infra,service        # platform deps + user-facing services
./scripts/setup-dev.sh                      # both
```

The script symlinks `local-infra/.env` to the root `.env`, then runs `docker compose build` → `docker compose up -d` for each requested group. Volumes are preserved across re-runs.

### Day-to-day script reference

```bash
./scripts/start-dev.sh  [groups]          # start stopped compose containers
./scripts/stop-dev.sh   [groups]          # stop compose containers (volumes kept)
./scripts/clean-dev.sh  [groups]          # remove compose containers AND volumes (full wipe)
./scripts/logs.sh       {infra|service}   # tail logs for one compose group
```

For finer-grained iteration, talk to the compose stacks directly:

```bash
docker compose up -d --build api                   # rebuild + restart one root service
docker compose restart supervisor                  # restart without rebuild
cd local-infra && docker compose restart litellm   # tweak a LiteLLM patch / config
```

Source for `api/`, `admin/`, `web/`, `agent-supervisor/`, and `workflow-worker/` is bind-mounted, so most changes hot-reload via `--reload` / `npm run dev` (see [compose.override.yml](compose.override.yml)).

## Usage

```bash
cp .env.example .env                        # edit if you need to override anything
./scripts/setup-dev.sh                      # infra + service
```

Open the Web UI, log in via Keycloak, save your Anthropic API key under `/settings?tab=credentials`, create an agent, chat. See [Service endpoints](#service-endpoints) below.

Test users `user1@test.com` / `user2@test.com` (password `password`) are seeded in the Keycloak realm.

### Pause / resume / wipe

```bash
./scripts/stop-dev.sh                # stop both compose groups; volumes preserved
./scripts/start-dev.sh               # bring them back, no rebuild
./scripts/clean-dev.sh service       # wipe app DB + Redis but keep Vault / Keycloak state
./scripts/clean-dev.sh               # nuke both compose groups
```

## Service endpoints

After `setup-dev.sh` finishes, these URLs are reachable on the host. Endpoints per group:

### `service` group

| Service | URL | Purpose |
|---------|-----|---------|
| Browser entry (Caddy proxy) | http://localhost:3000 | `/api/*` → API · `/` → Web — single same-origin entry |
| Admin Console | http://localhost:8001 | Operator UI (no auth, local-only) |

The API and Supervisor live on internal compose DNS only — `api:8000` / `supervisor:9000`. They are not published to the host; reach them via `docker compose exec` or hit them through the Caddy proxy.

### `infra` group

| Service | URL | Login / purpose |
|---------|-----|-----------------|
| Postgres | localhost:5432 | App + LiteLLM + Keycloak + Temporal databases |
| Redis | localhost:6379 | Pub/sub, unread counters |
| Temporal | localhost:7233 | gRPC (workers connect here) |
| Temporal UI | http://localhost:8233 | Workflow inspector |
| Keycloak | http://localhost:8080 | `admin` / `admin` |
| Vault | http://localhost:8200 | Token: `dev-root-token` |
| LiteLLM Proxy | http://localhost:8090 | Master key `sk-aviary-dev` |
| LiteLLM UI | http://localhost:8090/ui | `admin` / `admin` |
| LiteLLM MCP endpoint | http://localhost:8090/mcp | Aggregated MCP |
| Grafana | http://localhost:3001 | Anonymous admin; Aviary Supervisor dashboard auto-provisioned |
| Prometheus | http://localhost:9090 | — |
| OTel Collector | localhost:4317 (gRPC) / 4318 (HTTP) | OTLP receiver |

Test accounts (in Keycloak realm `aviary`): `user1@test.com`, `user2@test.com`, password `password`.

## Configuration

- **Single .env** — both compose stacks share [.env](.env.example) at the project root; `local-infra/.env` is auto-symlinked. `OIDC_ISSUER`, `LLM_GATEWAY_URL` / `MCP_GATEWAY_URL`, and `VAULT_ADDR` / `VAULT_TOKEN` are required — boot fails fast when unset.
- **LiteLLM** — model routing and platform-wide MCP servers in [local-infra/config/litellm/config.yaml](local-infra/config/litellm/config.yaml); per-server Vault key map in [mcp-secret-injection.yaml](local-infra/config/litellm/mcp-secret-injection.yaml).
- **Secrets** — per-user credentials live at `secret/aviary/credentials/{user_sub}/{namespace}/{key}` in Vault.

## Testing

```bash
docker compose exec api pytest tests/ -v
docker compose exec admin pytest tests/ -v
cd agent-supervisor && uv run pytest tests/ -v
```

API/Admin tests run against a dedicated `aviary_test` Postgres database with `NullPool`, no lifespan.

## License

See [LICENSE](./LICENSE).
