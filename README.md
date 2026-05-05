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
| **LiteLLM Gateway** ([infra/config/litellm/](infra/config/litellm/)) | Single entry point for both LLM inference and MCP tool calls; routes by model name and injects per-user secrets. |
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

### Repo layout — single project, two compose files

One Compose project (`aviary`) split into two files (both declare `name: aviary` so they share the same network/volumes/.env):

| File | What it covers |
|------|----------------|
| [compose.yml](compose.yml) | App services — web, api, admin, supervisor, workflow-worker, runtime, proxy |
| [compose.infra.yml](compose.infra.yml) | Infra deps (required) — postgres, redis, temporal, temporal-ui, db-migrate, keycloak, vault, litellm, prometheus, grafana, otel-collector, sample MCP servers |
| [compose.override.yml](compose.override.yml) | Dev-only overrides for app services (bind mounts, `--reload`, web `target: dev`) |

App services dial infra via Docker service DNS (`postgres:5432`, `redis:6379`, `temporal:7233`, `keycloak:8080`, `vault:8200`, `litellm:4000`).

### One-shot bring-up

```bash
git clone <repository-url>
cd aviary
cp .env.example .env                  # edit if you need to override anything
./scripts/setup-dev.sh                # dev all — build + up + hot reload
```

### `setup-dev.sh` — single entrypoint

```
setup-dev.sh [SUBCMD] [SCOPE]
  SUBCMD: dev (default) | build | deploy | run | down | clean | logs | ps
  SCOPE : all (default) | app | infra
```

| Command | Effect |
|---------|--------|
| `./scripts/setup-dev.sh` | dev all — build + up + hot reload (override auto-applied) |
| `./scripts/setup-dev.sh dev app` | dev app only (assumes infra is up) |
| `./scripts/setup-dev.sh build [scope]` | build images |
| `./scripts/setup-dev.sh deploy [scope]` | prod-mode up (no override → web runs `next start`) |
| `./scripts/setup-dev.sh run [scope]` | build + deploy |
| `./scripts/setup-dev.sh run app` | **rebuild + redeploy app, infra stays as-is** |
| `./scripts/setup-dev.sh down [scope]` | stop + remove containers (volumes preserved) |
| `./scripts/setup-dev.sh clean [scope]` | down + drop volumes |
| `./scripts/setup-dev.sh logs [scope] [svc…]` | follow logs |
| `./scripts/setup-dev.sh ps [scope]` | container status |

Source for `api/`, `admin/`, `web/`, `agent-supervisor/`, and `workflow-worker/` is bind-mounted in `compose.override.yml`, so most changes hot-reload via `--reload` / `npm run dev`.

For finer-grained iteration, talk to compose directly:

```bash
docker compose up -d --build api                              # rebuild one app service (override auto-loaded)
docker compose restart supervisor                             # restart without rebuild
docker compose -f compose.infra.yml restart litellm           # tweak a LiteLLM patch / config
```

## Usage

```bash
cp .env.example .env                        # edit if you need to override anything
./scripts/setup-dev.sh                      # dev all
```

Open the Web UI, log in via Keycloak, save your Anthropic API key under `/settings?tab=credentials`, create an agent, chat. See [Service endpoints](#service-endpoints) below.

Test users `user1@test.com` / `user2@test.com` (password `password`) are seeded in the Keycloak realm.

### Pause / resume / wipe

```bash
./scripts/setup-dev.sh down                # stop+remove all containers; volumes preserved
./scripts/setup-dev.sh                     # bring them back (rebuild applied)
./scripts/setup-dev.sh clean app           # wipe app + runtime-workspace; infra Vault/Keycloak/DB intact
./scripts/setup-dev.sh clean               # nuke everything
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

- **Single .env** — the project loads `.env` from the repo root automatically — no symlinks. `OIDC_ISSUER`, `LLM_GATEWAY_URL` / `MCP_GATEWAY_URL`, and `VAULT_ADDR` / `VAULT_TOKEN` are required — boot fails fast when unset.
- **LiteLLM** — model routing and platform-wide MCP servers in [infra/config/litellm/config.yaml](infra/config/litellm/config.yaml); per-server Vault key map in [mcp-secret-injection.yaml](infra/config/litellm/mcp-secret-injection.yaml).
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
