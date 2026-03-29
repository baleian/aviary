# AgentBox — Multi-Tenant AI Agent Platform

## Project Overview

AgentBox is a multi-tenant AI agent platform where internal employees (thousands of users) can create, configure, deploy, and use purpose-built AI agents through a Web UI. Each agent runs in an isolated Kubernetes namespace, and each user session spawns a dedicated Pod for full kernel-level isolation.

This document serves as the complete implementation specification. Read it fully before writing any code.

---

## Architecture Summary

```
┌──────────────────────────────────────────────────────────────┐
│                         Web UI (Next.js)                      │
│  Agent Catalog / Create / Edit / Delete / Session Chat / ACL  │
└──────────────┬───────────────────────────────────────────────┘
               │ REST API + WebSocket
┌──────────────▼───────────────────────────────────────────────┐
│                    API Server (Python/FastAPI)                 │
│  ┌──────┐ ┌───────────┐ ┌───────────┐ ┌────────┐ ┌────────┐ │
│  │ Auth │ │Agent CRUD │ │Session Mgr│ │ACL/RBAC│ │Vault   │ │
│  │(OIDC)│ │+ K8s Ops  │ │+ Pod Spawn│ │Engine  │ │Client  │ │
│  └──────┘ └───────────┘ └───────────┘ └────────┘ └────────┘ │
└──────────────┬───────────────────────────────────────────────┘
               │ K8s API + Vault API
┌──────────────▼───────────────────────────────────────────────┐
│                     Kubernetes Cluster                        │
│                                                               │
│  NS: platform          NS: agent-{id}       NS: agent-{id}   │
│  ┌───────────────┐     ┌──────────────┐     ┌──────────────┐ │
│  │ API Server    │     │ ConfigMap    │     │ ConfigMap    │ │
│  │ Cred Proxy    │     │ NetPolicy   │     │ NetPolicy   │ │
│  │ PostgreSQL    │     │ ResQuota    │     │ ResQuota    │ │
│  │ Redis         │     │ Session Pods│     │ Session Pods│ │
│  │ Keycloak      │     │  (Python    │     │  (Python    │ │
│  │ Vault (dev)   │     │   runtime)  │     │   runtime)  │ │
│  └───────────────┘     └──────────────┘     └──────────────┘ │
│                                                               │
│  Inference Backends (accessible from session Pods):           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │ Claude API   │  │ Ollama       │  │ vLLM         │       │
│  │ (external)   │  │ (in-cluster) │  │ (in-cluster) │       │
│  └──────────────┘  └──────────────┘  └──────────────┘       │
└──────────────────────────────────────────────────────────────┘
```

---

## Tech Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| Web UI | Next.js 15 (App Router), TypeScript, Tailwind CSS, shadcn/ui | Agent management, chat sessions, catalog |
| API Server | Python 3.12, FastAPI, SQLAlchemy, Pydantic | Business logic, K8s orchestration, auth |
| Database | PostgreSQL 16 | Agents, users, sessions, ACL metadata |
| Cache/Queue | Redis 7 | Session routing, WebSocket pub/sub, job queue |
| Auth (Production) | Okta (OIDC/OAuth2) | SSO for internal employees |
| Auth (Local Dev) | Keycloak 25 | Okta-compatible OIDC mock |
| Secrets (Production) | HashiCorp Vault | Credential injection into session Pods |
| Secrets (Local Dev) | Vault dev mode (`-dev`) | Local Vault with in-memory storage |
| Container Orchestration | Kubernetes (K3s for local dev) | Namespace isolation, Pod lifecycle |
| Agent Runtime | Python 3.12 + Claude Agent SDK (Python) | Runs inside session Pods |
| Inference Router | Python, built into runtime | Routes LLM calls to correct backend |
| LLM Backend: Frontier | Claude API (Anthropic) | Opus, Sonnet via API |
| LLM Backend: Local (Ollama) | Ollama on WSL2 (Anthropic-compatible API) | Open-weight models with GPU (Llama, Qwen, etc.) |
| LLM Backend: Local (vLLM) | vLLM on WSL2 (OpenAI-compatible API) | High-throughput local GPU inference |
| Local Dev Orchestration | Docker Compose + K3s-in-Docker | One-command local environment |

---

## Data Model

### Core Entities

```sql
-- Users (synced from OIDC provider)
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id VARCHAR(255) UNIQUE NOT NULL,  -- OIDC subject
    email VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    avatar_url TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Teams
CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) UNIQUE NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE team_members (
    team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(50) DEFAULT 'member',  -- 'owner' | 'member'
    PRIMARY KEY (team_id, user_id)
);

-- Agents
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) UNIQUE NOT NULL,          -- URL-safe identifier
    description TEXT,
    owner_id UUID REFERENCES users(id) NOT NULL,

    -- Agent Definition
    instruction TEXT NOT NULL,                   -- System prompt (markdown)
    model_config JSONB NOT NULL DEFAULT '{}',    -- See ModelConfig below
    tools JSONB DEFAULT '[]',                    -- Enabled tool list
    mcp_servers JSONB DEFAULT '[]',              -- MCP server configurations

    -- Policy
    policy JSONB NOT NULL DEFAULT '{}',          -- See AgentPolicy below

    -- Catalog
    visibility VARCHAR(20) DEFAULT 'private',    -- 'public' | 'team' | 'private'
    category VARCHAR(100),
    icon VARCHAR(50),

    -- K8s
    namespace VARCHAR(255),                      -- K8s namespace name

    -- Status
    status VARCHAR(20) DEFAULT 'active',         -- 'active' | 'disabled' | 'deleted'
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Agent ACL
CREATE TABLE agent_acl (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID REFERENCES agents(id) ON DELETE CASCADE,

    -- Grantee: either user or team (one must be set)
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    team_id UUID REFERENCES teams(id) ON DELETE CASCADE,

    role VARCHAR(20) NOT NULL,  -- 'owner' | 'admin' | 'user' | 'viewer'

    created_at TIMESTAMPTZ DEFAULT now(),
    CONSTRAINT acl_grantee CHECK (
        (user_id IS NOT NULL AND team_id IS NULL) OR
        (user_id IS NULL AND team_id IS NOT NULL)
    ),
    UNIQUE (agent_id, user_id),
    UNIQUE (agent_id, team_id)
);

-- Agent Credentials (references to Vault paths)
CREATE TABLE agent_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID REFERENCES agents(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,                  -- e.g., 'GITHUB_TOKEN'
    vault_path VARCHAR(512) NOT NULL,            -- Vault secret path
    description TEXT,
    UNIQUE (agent_id, name)
);

-- Sessions
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID REFERENCES agents(id) NOT NULL,

    -- Session type
    type VARCHAR(20) DEFAULT 'private',          -- 'private' | 'team'
    created_by UUID REFERENCES users(id) NOT NULL,
    team_id UUID REFERENCES teams(id),           -- Set when type='team'; auto-exposes to team

    -- State
    title VARCHAR(255),                          -- Auto-generated from first message
    status VARCHAR(20) DEFAULT 'active',         -- 'active' | 'archived'
    pod_name VARCHAR(255),                       -- Current K8s Pod name (null if no Pod)

    -- Timestamps
    last_message_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Session Participants (for private invite + team auto-join)
CREATE TABLE session_participants (
    session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    invited_by UUID REFERENCES users(id),         -- null for team auto-join
    joined_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (session_id, user_id)
);

-- Messages
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
    sender_type VARCHAR(20) NOT NULL,            -- 'user' | 'agent'
    sender_id UUID,                              -- user_id if sender_type='user'
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',                 -- tool calls, attachments, etc.
    created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_messages_session ON messages(session_id, created_at);
```

### ModelConfig JSON Schema

```typescript
interface ModelConfig {
    backend: 'claude' | 'ollama' | 'vllm';  // Inference backend
    model: string;                            // Model identifier
    // backend='claude'  → model: 'claude-sonnet-4-20250514', 'claude-opus-4-20250514', etc.
    // backend='ollama'  → model: 'llama3.3:70b', 'qwen2.5:32b', etc.
    // backend='vllm'    → model: 'meta-llama/Llama-3.3-70B-Instruct', etc.
    temperature?: number;                     // default: 0.7
    maxTokens?: number;                       // default: 8192
}
```

Backend API compatibility:
- Claude: Anthropic API (native)
- Ollama: Anthropic-compatible API (same SDK, `base_url` override to localhost:11434)
- vLLM: OpenAI-compatible API (`base_url` override to localhost:8001)

Ollama and vLLM run externally on WSL2 with GPU — not managed by this project.

### AgentPolicy JSON Schema

```typescript
interface AgentPolicy {
    maxConcurrentSessions: number;     // default: 20
    sessionTimeout: number;            // minutes, default: 30
    maxTokensPerTurn: number;          // default: 100000
    maxMemoryPerSession: string;       // e.g., '512Mi'
    maxCpuPerSession: string;          // e.g., '500m'
    allowedDomains: string[];          // network egress whitelist
    allowShellExec: boolean;           // default: false
    allowFileWrite: boolean;           // default: true
    containerImage: string;            // default: 'agentbox-runtime:latest'
}
```

---

## ACL / RBAC Model

### Platform Roles

```
platform_admin:  Global admin. Can search ALL agents in catalog, edit/delete any agent,
                 view all sessions, override any ACL. Assigned via Keycloak/Okta role claim.
regular_user:    Default role. Subject to agent-level ACL below.
```

### Agent-Level Roles and Permissions

```
Role        | search/view | chat | view_logs | edit_config | manage_acl | delete
------------|-------------|------|-----------|-------------|------------|-------
(none)      |  depends*   |  -   |     -     |      -      |     -      |   -
user        |      O      |  O   |     -     |      -      |     -      |   -
admin       |      O      |  O   |     O     |      O      |     -      |   -
owner       |      O      |  O   |     O     |      O      |     O      |   O

* search/view visibility depends on agent's visibility setting (see Catalog ACL below)
```

### Permission Resolution Order

```
1. Check if user is platform_admin → full access to everything
2. Check if user is agent owner → full access to this agent
3. Check direct user ACL entry for this agent
4. Check team ACL entries (user's teams)
5. If agent visibility='public' → implicit 'user' role for all authenticated users
6. If agent visibility='team' → implicit 'user' role for team members only
7. Deny
```

### Catalog ACL (Agent Discovery)

```
Agents are discoverable in the catalog based on visibility + ACL:

visibility='private':
  - Only owner can see it in catalog
  - Other users with explicit ACL entry (user/admin) can also see it
  - Nobody else can search or find this agent

visibility='team':
  - Owner's team members can see it in catalog
  - Teams granted ACL access can see it
  - Users with explicit ACL entry can see it

visibility='public':
  - All authenticated users can search and find this agent
  - All authenticated users can create their own sessions
  - Only owner/admin can edit agent configuration

platform_admin:
  - Can see ALL agents regardless of visibility
  - Can edit/delete any agent
  - Can change any agent's visibility
```

### Agent Configuration Edit Permissions

```
Who can edit agent definition (instruction, tools, model, policy, credentials)?

Private agent:   owner only
Team agent:      owner + team members with 'admin' role on this agent
Public agent:    owner + users with 'admin' role on this agent

Session-invited users (participants):
  - Can ONLY chat within their session
  - CANNOT view or edit agent configuration
  - CANNOT see other sessions they weren't invited to

platform_admin:
  - Can edit any agent configuration
```

### Session ACL

```
Session creation:
  - Any user with 'user'+ role on the agent can create a session
  - Creator chooses: 'private' or 'team' mode

Private session:
  - Only creator can access by default
  - Creator can invite other users by email address
  - Invited users see the session in their session list upon login
  - Invited users must have at least 'user' role on the agent
  - Invited users can chat but CANNOT edit agent config

Team session:
  - Automatically visible to all members of creator's team(s)
  - All team members can join and chat in the same session
  - Team members must have at least 'user' role on the agent
  - Messages show sender name (multi-user conversation)

Shared session concurrency:
  - Multiple users can be connected via WebSocket simultaneously
  - Messages are serialized per session (one LLM turn at a time)
  - All participants see messages in real-time via WebSocket pub/sub (Redis)

Session participant management:
  - Creator can invite (email) / remove participants
  - For team sessions, team membership changes auto-reflect
```

### Session Invitation Flow

```
1. Creator clicks "Invite" in session UI → enters email address
2. API Server:
   a. Look up user by email (must exist in system, i.e., logged in at least once)
   b. Verify invitee has 'user'+ role on the agent
   c. Add to session_participants table
3. Invitee:
   a. On next login (or immediately if online), session appears in their list
   b. Real-time notification via WebSocket if currently connected
   c. Can join and chat immediately
```

---

## Kubernetes Resource Management

### Agent Creation → K8s Resources

When an agent is created via the API, the following K8s resources are automatically provisioned:

#### 1. Namespace

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: agent-{agent_id}
  labels:
    agentbox/agent-id: "{agent_id}"
    agentbox/owner: "{owner_id}"
    agentbox/managed: "true"
```

#### 2. ConfigMap (Agent Definition)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: agent-config
  namespace: agent-{agent_id}
data:
  instruction.md: |
    {agent instruction content}
  tools.json: |
    ["read_file", "write_file", "run_command", "web_search"]
  policy.json: |
    {"maxConcurrentSessions": 20, "sessionTimeout": 30, ...}
  mcp-servers.json: |
    [{"name": "github", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"]}]
```

#### 3. NetworkPolicy

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: session-egress
  namespace: agent-{agent_id}
spec:
  podSelector:
    matchLabels:
      agentbox/role: session
  policyTypes: ["Egress", "Ingress"]
  ingress: []  # No inbound traffic to session pods
  egress:
    - to:  # DNS
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - port: 53
          protocol: UDP
    - to:  # Credential proxy in platform namespace
        - namespaceSelector:
            matchLabels:
              agentbox/namespace: platform
          podSelector:
            matchLabels:
              agentbox/service: credential-proxy
      ports:
        - port: 8080
          protocol: TCP
    # Additional egress rules generated from policy.allowedDomains
```

#### 4. ResourceQuota

```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: session-quota
  namespace: agent-{agent_id}
spec:
  hard:
    pods: "20"                    # from policy.maxConcurrentSessions
    requests.cpu: "10"
    requests.memory: "10Gi"
    limits.cpu: "20"
    limits.memory: "20Gi"
    persistentvolumeclaims: "100"
```

#### 5. ServiceAccount (for session pods)

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: session-runner
  namespace: agent-{agent_id}
automountServiceAccountToken: false  # Pods cannot access K8s API
```

### Session Pod Specification

When a user sends a message and no active Pod exists for that session:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: session-{session_id_short}
  namespace: agent-{agent_id}
  labels:
    agentbox/role: session
    agentbox/session-id: "{session_id}"
    agentbox/user-id: "{user_id}"
spec:
  serviceAccountName: session-runner
  restartPolicy: Never

  containers:
  - name: agent-runtime
    image: agentbox-runtime:latest
    ports:
      - containerPort: 3000    # HTTP server for receiving messages
    env:
      - name: SESSION_ID
        value: "{session_id}"
      - name: AGENT_ID
        value: "{agent_id}"
      - name: CREDENTIAL_PROXY_URL
        value: "http://credential-proxy.platform.svc:8080"

    volumeMounts:
      - name: session-workspace
        mountPath: /workspace
      - name: agent-config
        mountPath: /agent/config
        readOnly: true

    resources:
      requests:
        cpu: "250m"
        memory: "256Mi"
      limits:
        cpu: "500m"
        memory: "512Mi"

    livenessProbe:
      httpGet:
        path: /health
        port: 3000
      initialDelaySeconds: 5
      periodSeconds: 30

    readinessProbe:
      httpGet:
        path: /ready
        port: 3000
      initialDelaySeconds: 3

  volumes:
    - name: session-workspace
      persistentVolumeClaim:
        claimName: session-{session_id_short}
    - name: agent-config
      configMap:
        name: agent-config

  # Idle timeout: API Server monitors and deletes idle pods
  # (not handled by K8s natively — implemented in API Server)
```

### Session PVC

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: session-{session_id_short}
  namespace: agent-{agent_id}
  labels:
    agentbox/session-id: "{session_id}"
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 1Gi
  storageClassName: local-path   # K3s default
```

---

## Agent Runtime Container

### Image: `agentbox-runtime`

The container image that runs inside each session Pod. Built with Python for consistency with the API Server.

```dockerfile
FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY runtime/ /app/
RUN pip install --no-cache-dir -r requirements.txt

USER nobody
EXPOSE 3000

CMD ["python", "-m", "uvicorn", "app.server:app", "--host", "0.0.0.0", "--port", "3000"]
```

### Agent Runner (inside container)

The runtime is a Python FastAPI HTTP server that:
1. Receives messages from API Server via HTTP POST
2. Reads agent config from mounted ConfigMap (`/agent/config/`)
3. Reads model config to determine inference backend
4. Calls Claude Agent SDK (Python) `query()` with:
   - instruction from `/agent/config/instruction.md`
   - tools from `/agent/config/tools.json`
   - MCP servers from `/agent/config/mcp-servers.json`
   - `cwd: /workspace` (session-isolated PVC)
   - model backend routing (see Inference Router section)
5. Streams response back via Server-Sent Events (SSE)
6. Persists conversation history to `/workspace/.history/`
7. Reports health/ready status

```
Session Pod HTTP API:
  POST /message          — Send message, receive SSE stream response
  GET  /health           — Liveness check
  GET  /ready            — Readiness check
  POST /shutdown         — Graceful shutdown
```

### Runtime Dependencies (requirements.txt)

```
claude-agent-sdk
anthropic
fastapi
uvicorn[standard]
httpx
pydantic
```

### Credential Injection

Session Pods do NOT have direct access to secrets. Instead:

```
Agent calls external API (e.g., GitHub)
  → Request goes to Credential Proxy (platform namespace)
  → Proxy looks up session → agent → vault path mapping
  → Proxy fetches secret from Vault
  → Proxy injects Authorization header
  → Proxy forwards request to target API
```

The Credential Proxy is a shared service in the platform namespace, accessible via NetworkPolicy from all agent namespaces.

---

## Inference Router

### Overview

Each agent specifies a `model_config` with a `backend` and `model` name. The Inference Router resolves this to the correct API endpoint.

Key insight: **Ollama natively supports the Anthropic API format** (see [Ollama Anthropic Compatibility](https://docs.ollama.com/api/anthropic-compatibility)), so both Claude and Ollama use the same Anthropic SDK — just with different `base_url`. vLLM uses the OpenAI-compatible endpoint.

### Backend Configuration

```
┌──────────────────────────────────────────────────────────────┐
│                    Inference Router                            │
│                                                                │
│  model_config.backend = 'claude'                              │
│    → Anthropic API (https://api.anthropic.com)                │
│    → Anthropic SDK (native)                                    │
│    → API key from Vault via Credential Proxy                  │
│    → Models: claude-opus-4-20250514,                          │
│              claude-sonnet-4-20250514, etc.                    │
│                                                                │
│  model_config.backend = 'ollama'                              │
│    → Anthropic-compatible endpoint (http://localhost:11434)    │
│    → Anthropic SDK with base_url override                     │
│    → No API key required (local GPU, WSL2)                    │
│    → Models: llama3.3:70b, qwen2.5:32b, etc.                 │
│                                                                │
│  model_config.backend = 'vllm'                                │
│    → OpenAI-compatible endpoint (http://localhost:8001)        │
│    → Anthropic SDK with base_url override                        │
│    → No API key required (local GPU, WSL2)                    │
│    → Models: meta-llama/Llama-3.3-70B-Instruct, etc.         │
└──────────────────────────────────────────────────────────────┘

Ollama and vLLM run externally on WSL2 with GPU acceleration.
They are NOT managed by Docker Compose — just pre-existing local endpoints.
```

### SDK Integration

```python
from claude_agent_sdk import query

# Claude backend (default) — native Anthropic API
if model_config["backend"] == "claude":
    result = query(
        prompt=message,
        options={
            "model": model_config["model"],
            "cwd": "/workspace",
        }
    )

# Ollama backend — Anthropic-compatible API (same SDK, different base_url)
elif model_config["backend"] == "ollama":
    result = query(
        prompt=message,
        options={
            "model": model_config["model"],     # e.g., "llama3.3:70b"
            "apiKeyHelper": "echo ollama",       # Ollama ignores key but SDK requires one
            "modelProviderBaseUrl": OLLAMA_URL,  # http://localhost:11434
            "cwd": "/workspace",
        }
    )

# vLLM backend — OpenAI-compatible API
elif model_config["backend"] == "vllm":
    result = query(
        prompt=message,
        options={
            "model": model_config["model"],
            "apiKeyHelper": "echo dummy",
            "modelProviderBaseUrl": VLLM_URL,    # http://localhost:8001
            "cwd": "/workspace",
        }
    )
```

### Environment Variables for Inference Endpoints

```env
# Inference backends — Ollama and vLLM run on WSL2 locally, not in Docker
INFERENCE_OLLAMA_URL=http://localhost:11434       # Ollama (Anthropic-compatible)
INFERENCE_VLLM_URL=http://localhost:8001          # vLLM (OpenAI-compatible)
```

### Note on Local Inference Setup

Ollama and vLLM are **pre-existing services** running on WSL2 with GPU acceleration. They are not part of the Docker Compose stack. The API Server and session Pods connect to them via `localhost` (or host network equivalent when running inside K3s — use `host.k3s.internal` or equivalent host gateway).

### Web UI: Model Selection in Agent Config

```
Agent Create/Edit Form:

■ Model Configuration
  Backend:  [Claude API ▾]  |  [Ollama ▾]  |  [vLLM ▾]
  Model:    [claude-sonnet-4-20250514 ▾]
            (dropdown populated from backend's available models)
  Temperature: [0.7    ]
  Max Tokens:  [8192   ]

When backend = 'ollama':
  → API call to GET /api/inference/ollama/models → list available models
When backend = 'vllm':
  → API call to GET /api/inference/vllm/models → list available models
When backend = 'claude':
  → Static list of Claude model IDs
```

### API Endpoints for Inference

```
GET /api/inference/backends               — List available backends + status
GET /api/inference/{backend}/models       — List available models for backend
POST /api/inference/{backend}/health      — Check backend connectivity
```

---

## Credential Proxy & Vault Integration

### Credential Proxy Service (platform namespace)

```
Deployment: credential-proxy
  - Receives HTTP requests from session Pods
  - Extracts SESSION_ID header from request
  - Queries DB: session → agent → agent_credentials
  - Fetches actual secret value from Vault
  - Injects into outbound request headers
  - Forwards to target API
```

### Vault Integration

**Production:** HashiCorp Vault with AppRole or Kubernetes auth
**Local Dev:** Vault dev mode with pre-seeded test secrets

```
Vault Secret Layout:
  secret/data/agentbox/agents/{agent_id}/credentials/{credential_name}
    → { "value": "ghp_xxxx..." }

Vault Policy (per agent):
  path "secret/data/agentbox/agents/{agent_id}/*" {
    capabilities = ["read"]
  }
```

### Local Dev Vault Seeding

On startup, the dev environment auto-seeds Vault with test credentials:

```bash
vault kv put secret/agentbox/agents/test-agent/credentials/GITHUB_TOKEN value="ghp_test_token_12345"
vault kv put secret/agentbox/agents/test-agent/credentials/SLACK_TOKEN value="xoxb-test-token-12345"
```

---

## Authentication

### OIDC Flow

```
Web UI → Keycloak (local) or Okta (production)
  → Authorization Code Flow with PKCE
  → ID Token + Access Token
  → API Server validates token
  → Creates/updates user in DB
  → Issues session cookie or returns JWT
```

### Keycloak Local Setup (Okta Mock)

Keycloak is configured to mimic Okta's OIDC behavior:

```
Realm: agentbox
Client: agentbox-web
  - Client Protocol: openid-connect
  - Access Type: public
  - Valid Redirect URIs: http://localhost:3000/*
  - Web Origins: http://localhost:3000

Test Users (pre-seeded):
  - admin@test.com / password (admin role)
  - user1@test.com / password (regular user)
  - user2@test.com / password (regular user)
```

### API Server Auth Middleware

```python
# Auth is provider-agnostic via OIDC discovery
OIDC_ISSUER = os.getenv("OIDC_ISSUER")
# Local: http://localhost:8080/realms/agentbox
# Prod:  https://company.okta.com/oauth2/default

# Middleware validates JWT from Authorization header
# Extracts user info from token claims
# Creates user in DB on first login
```

---

## API Server Endpoints

### Auth
```
GET  /api/auth/config          — OIDC provider configuration for frontend
POST /api/auth/callback        — Exchange auth code for tokens
GET  /api/auth/me              — Current user info
POST /api/auth/logout          — Logout
```

### Agents
```
GET    /api/agents                — List agents (filtered by ACL + catalog)
POST   /api/agents                — Create agent (+ K8s namespace provisioning)
GET    /api/agents/{id}           — Get agent details
PUT    /api/agents/{id}           — Update agent (+ K8s resource update)
DELETE /api/agents/{id}           — Delete agent (+ K8s namespace deletion)
POST   /api/agents/{id}/deploy    — Apply changes to running sessions
```

### Agent ACL
```
GET    /api/agents/{id}/acl       — List ACL entries
POST   /api/agents/{id}/acl       — Grant access (user or team)
PUT    /api/agents/{id}/acl/{aclId} — Update role
DELETE /api/agents/{id}/acl/{aclId} — Revoke access
```

### Agent Credentials
```
GET    /api/agents/{id}/credentials       — List credential names (not values)
POST   /api/agents/{id}/credentials       — Add credential (stores in Vault)
DELETE /api/agents/{id}/credentials/{name} — Remove credential
```

### Sessions
```
GET    /api/agents/{id}/sessions          — List user's sessions for this agent
POST   /api/agents/{id}/sessions          — Create new session (body: { type: 'private' | 'team' })
GET    /api/sessions/{id}                 — Get session details + messages
DELETE /api/sessions/{id}                 — Archive session
POST   /api/sessions/{id}/invite          — Invite user by email address
DELETE /api/sessions/{id}/participants/{userId} — Remove participant
GET    /api/sessions/invited              — List sessions where current user was invited
```

### Chat (WebSocket)
```
WS /api/sessions/{id}/ws                 — Real-time chat
  → Client sends: { type: 'message', content: '...' }
  ← Server sends: { type: 'chunk', content: '...' }        (streaming)
  ← Server sends: { type: 'tool_use', name: '...', ... }   (tool calls)
  ← Server sends: { type: 'done', messageId: '...' }       (complete)
  ← Server sends: { type: 'error', message: '...' }        (error)
```

### Catalog
```
GET /api/catalog                          — Browse public/team agents
GET /api/catalog/categories               — List categories
GET /api/catalog/search?q=...             — Search agents
```

---

## Session Lifecycle

### Message Flow

```
1. User sends message via WebSocket
2. API Server validates:
   - User authenticated
   - User has 'user'+ role on this agent
   - Session belongs to user (or user is participant)
3. Check if session has active Pod:
   a. Yes → Forward message to Pod via HTTP POST
   b. No  → Spawn new Pod in agent namespace
           → Wait for readiness probe
           → Forward message
4. Pod processes message:
   - Load conversation history from /workspace/.history/
   - Call Claude Agent SDK query()
   - Stream response chunks
5. API Server relays chunks to user via WebSocket
6. Save complete message pair to PostgreSQL
```

### Pod Idle Management

```
API Server runs a background task (every 60 seconds):
  1. Query all sessions with active pods
  2. Check last_message_at
  3. If now - last_message_at > sessionTimeout:
     a. Delete the Pod (kubectl delete pod)
     b. Set session.pod_name = null
     c. PVC remains (workspace preserved)
  4. Next message will spawn a new Pod with same PVC
```

### Session State Preservation

```
Pod dies (idle timeout, crash, node drain):
  - PVC persists: /workspace/ survives
  - Conversation history: in /workspace/.history/ AND in PostgreSQL
  - Next Pod spawn: mounts same PVC, reads history, continues

What's in the PVC (/workspace/):
  ├── .history/
  │   ├── messages.jsonl     — Full conversation history for SDK
  │   └── session-state.json — SDK session ID, cursors
  ├── .claude/               — Claude Agent SDK session data
  │   └── (managed by SDK)
  └── (user files)           — Files created by agent during work
```

---

## Agent-to-Agent Communication

Agents can invoke other agents as tools, enabling orchestration:

```
Agent A (orchestrator) has tool: "call_agent"
  → call_agent({ agentId: "agent-b", message: "review this code" })
  → API Server validates A has permission to invoke B
  → Creates a transient session for A→B communication
  → Returns B's response as tool result to A
```

### Implementation

```
1. Agent A's tool list includes: call_agent
2. When Agent A calls this tool:
   a. MCP server in Agent A's pod sends request to API Server
   b. API Server checks agent-to-agent ACL
   c. API Server routes to Agent B (existing pod or new spawn)
   d. Agent B processes, returns result
   e. Result returned to Agent A as tool output
3. Agent-to-agent ACL is separate from user ACL:
   - Configured in agent definition: allowedAgents: ["agent-b-id"]
```

---

## Local Development Environment

### Prerequisites

```
- Docker Desktop (or equivalent)
- Node.js 22+
- Python 3.12+
- kubectl
```

### Architecture (Local)

```
Docker Compose orchestrates:
  ┌─────────────────────────────────────────────┐
  │ docker-compose.yml                           │
  │                                              │
  │  ┌──────────┐  ┌───────────┐  ┌───────────┐ │
  │  │PostgreSQL│  │  Redis    │  │ Keycloak  │ │
  │  │  :5432   │  │  :6379   │  │  :8080    │ │
  │  └──────────┘  └───────────┘  └───────────┘ │
  │                                              │
  │  ┌──────────┐  ┌───────────────────────────┐ │
  │  │  Vault   │  │  K3s (server + agent)     │ │
  │  │ (dev)    │  │  K8s API: :6443           │ │
  │  │  :8200   │  │  Traefik disabled         │ │
  │  └──────────┘  └───────────────────────────┘ │
  │                                              │
  └─────────────────────────────────────────────┘

  Run separately (host):
  ┌──────────────┐  ┌──────────────┐
  │ Next.js Dev  │  │ FastAPI Dev  │
  │ :3000        │  │ :8000        │
  └──────────────┘  └──────────────┘
```

### Docker Compose Services

```yaml
# docker-compose.yml

services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: agentbox
      POSTGRES_USER: agentbox
      POSTGRES_PASSWORD: agentbox
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./scripts/init-db.sql:/docker-entrypoint-initdb.d/init.sql

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"

  keycloak:
    image: quay.io/keycloak/keycloak:25.0
    command: start-dev --import-realm
    environment:
      KC_DB: postgres
      KC_DB_URL: jdbc:postgresql://postgres:5432/keycloak
      KC_DB_USERNAME: agentbox
      KC_DB_PASSWORD: agentbox
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: admin
    ports:
      - "8080:8080"
    volumes:
      - ./config/keycloak/realm-export.json:/opt/keycloak/data/import/realm-export.json
    depends_on:
      - postgres

  vault:
    image: hashicorp/vault:1.17
    command: server -dev -dev-root-token-id=dev-root-token
    environment:
      VAULT_DEV_ROOT_TOKEN_ID: dev-root-token
      VAULT_DEV_LISTEN_ADDRESS: "0.0.0.0:8200"
    ports:
      - "8200:8200"
    cap_add:
      - IPC_LOCK

  vault-init:
    image: hashicorp/vault:1.17
    depends_on:
      - vault
    environment:
      VAULT_ADDR: "http://vault:8200"
      VAULT_TOKEN: "dev-root-token"
    entrypoint: /bin/sh
    command: >
      -c "
        sleep 2 &&
        vault secrets enable -version=2 -path=secret kv 2>/dev/null || true &&
        vault kv put secret/agentbox/agents/test-agent/credentials/GITHUB_TOKEN value=ghp_test_12345 &&
        vault kv put secret/agentbox/agents/test-agent/credentials/SLACK_TOKEN value=xoxb-test-12345 &&
        echo 'Vault initialized with test secrets'
      "

  k3s:
    image: rancher/k3s:v1.31.4-k3s1
    command: server --disable=traefik --write-kubeconfig-mode=644
    privileged: true
    environment:
      K3S_TOKEN: agentbox-dev-token
    ports:
      - "6443:6443"
    volumes:
      - k3sdata:/var/lib/rancher/k3s
      - ./config/k3s/registries.yaml:/etc/rancher/k3s/registries.yaml

volumes:
  pgdata:
  k3sdata:
```

### Local Dev Startup Procedure

```bash
# 1. Start infrastructure
docker compose up -d

# 2. Configure kubectl to use K3s
export KUBECONFIG=$(docker inspect k3s --format '{{ .Mounts }}' | ...)
# Or: copy kubeconfig from K3s container
docker cp agentbox-k3s-1:/etc/rancher/k3s/k3s.yaml ./kubeconfig.yaml
sed -i 's/127.0.0.1/localhost/' ./kubeconfig.yaml
export KUBECONFIG=./kubeconfig.yaml

# 3. Build and load agent runtime image into K3s
docker build -t agentbox-runtime:latest ./runtime/
docker save agentbox-runtime:latest | docker exec -i agentbox-k3s-1 ctr images import -

# 4. Create platform namespace and base resources
kubectl apply -f ./k8s/platform/

# 5. Start API Server
cd api && pip install -e ".[dev]" && uvicorn app.main:app --reload --port 8000

# 6. Start Web UI
cd web && npm install && npm run dev
```

---

## Project Directory Structure

```
agentbox/
├── CLAUDE.md                          # This file
├── docker-compose.yml                 # Local infrastructure
│
├── web/                               # Next.js Web UI
│   ├── package.json
│   ├── next.config.ts
│   ├── tailwind.config.ts
│   ├── src/
│   │   ├── app/
│   │   │   ├── layout.tsx
│   │   │   ├── page.tsx               # Landing / catalog
│   │   │   ├── login/page.tsx
│   │   │   ├── agents/
│   │   │   │   ├── page.tsx           # Agent list / catalog
│   │   │   │   ├── new/page.tsx       # Create agent
│   │   │   │   └── [id]/
│   │   │   │       ├── page.tsx       # Agent detail
│   │   │   │       ├── edit/page.tsx  # Edit agent
│   │   │   │       ├── sessions/page.tsx
│   │   │   │       └── settings/page.tsx  # ACL, credentials
│   │   │   └── sessions/
│   │   │       └── [id]/page.tsx      # Chat session
│   │   ├── components/
│   │   │   ├── chat/                  # Chat UI components
│   │   │   ├── agents/                # Agent management components
│   │   │   └── ui/                    # shadcn/ui components
│   │   ├── lib/
│   │   │   ├── api.ts                 # API client
│   │   │   ├── auth.ts                # OIDC auth helpers
│   │   │   └── websocket.ts           # WebSocket client
│   │   └── types/
│   │       └── index.ts               # Shared TypeScript types
│   └── public/
│
├── api/                               # Python FastAPI API Server
│   ├── pyproject.toml
│   ├── app/
│   │   ├── main.py                    # FastAPI app entry
│   │   ├── config.py                  # Environment configuration
│   │   ├── db/
│   │   │   ├── models.py             # SQLAlchemy models
│   │   │   ├── session.py            # DB session management
│   │   │   └── migrations/           # Alembic migrations
│   │   ├── auth/
│   │   │   ├── oidc.py               # OIDC token validation
│   │   │   ├── middleware.py         # Auth middleware
│   │   │   └── dependencies.py       # FastAPI dependencies
│   │   ├── routers/
│   │   │   ├── agents.py             # Agent CRUD endpoints
│   │   │   ├── sessions.py           # Session endpoints + WebSocket
│   │   │   ├── acl.py                # ACL management
│   │   │   ├── credentials.py        # Credential management
│   │   │   ├── catalog.py            # Agent catalog / search
│   │   │   └── auth.py               # Auth endpoints
│   │   ├── services/
│   │   │   ├── agent_service.py      # Agent business logic
│   │   │   ├── session_service.py    # Session + Pod lifecycle
│   │   │   ├── k8s_service.py        # K8s namespace/pod operations
│   │   │   ├── vault_service.py      # Vault read/write
│   │   │   ├── acl_service.py        # Permission checks
│   │   │   └── credential_proxy.py   # Credential proxy logic
│   │   └── schemas/
│   │       ├── agent.py              # Pydantic schemas
│   │       ├── session.py
│   │       └── common.py
│   └── tests/
│
├── runtime/                           # Agent Runtime (runs in session Pods, Python)
│   ├── Dockerfile
│   ├── requirements.txt
│   ├── pyproject.toml
│   └── app/
│       ├── server.py                  # FastAPI HTTP server (receives messages)
│       ├── agent.py                   # Claude Agent SDK wrapper + inference routing
│       ├── history.py                 # Conversation history manager
│       └── health.py                  # Health/readiness probes
│
├── credential-proxy/                  # Credential Proxy Service
│   ├── Dockerfile
│   ├── pyproject.toml
│   └── app/
│       ├── main.py                    # Proxy HTTP server
│       ├── vault_client.py            # Vault integration
│       └── session_resolver.py        # Session → credential mapping
│
├── config/                            # Configuration files
│   ├── keycloak/
│   │   └── realm-export.json          # Pre-configured realm with test users
│   └── k3s/
│       └── registries.yaml            # Local registry config
│
├── k8s/                               # K8s manifests
│   └── platform/
│       ├── namespace.yaml
│       ├── credential-proxy.yaml      # Deployment + Service
│       └── image-warmer.yaml          # DaemonSet for image caching
│
└── scripts/
    ├── init-db.sql                    # Keycloak DB + AgentBox DB init
    ├── setup-dev.sh                   # One-command dev setup
    └── seed-test-data.py              # Seed test agents/users
```

---

## Implementation Order

### Phase 1: Foundation
1. Set up project structure (monorepo)
2. Docker Compose with PostgreSQL, Redis, Keycloak, Vault (dev), K3s
3. FastAPI boilerplate with OIDC auth (Keycloak)
4. Database models + Alembic migrations
5. Next.js boilerplate with auth flow (login/logout)

### Phase 2: Agent Management
6. Agent CRUD API endpoints
7. K8s service: namespace creation, ConfigMap, NetworkPolicy, ResourceQuota
8. Agent management Web UI (create, edit, delete)
9. ACL API + UI
10. Agent catalog (list, search, categories)

### Phase 3: Session & Chat
11. Session CRUD API
12. Agent runtime container image (HTTP server + Claude Agent SDK)
13. Pod spawning: session → Pod lifecycle management
14. WebSocket chat endpoint (API Server ↔ Pod ↔ Client)
15. Chat UI (streaming, tool call display)
16. Pod idle timeout manager (background task)
17. PVC management for session workspace persistence

### Phase 4: Credentials & Security
18. Vault integration in API Server
19. Credential proxy service (platform namespace)
20. Agent credential management UI
21. NetworkPolicy enforcement per agent

### Phase 5: Advanced Features
22. Shared sessions (multi-user chat with same agent)
23. Agent-to-agent communication
24. Session history browsing
25. Agent versioning

---

## Key Design Decisions

1. **Per-session Pod isolation** — Each user session runs in its own K8s Pod. This provides kernel-level filesystem and process isolation without needing application-level path validation. The Claude Agent SDK's built-in tools (Read, Write, Bash) work without restriction because the Pod's filesystem IS the sandbox.

2. **Namespace per agent** — Each agent gets its own K8s namespace. This enables per-agent NetworkPolicy, ResourceQuota, and RBAC. Deleting a namespace cleans up all resources automatically.

3. **PVC per session** — Session workspace persists across Pod restarts. When a Pod is terminated (idle timeout) and re-created, it mounts the same PVC to restore state.

4. **Credential Proxy pattern** — Secrets never enter session Pods. A shared proxy service in the platform namespace intercepts API calls, injects credentials from Vault, and forwards requests. Session Pods only know the proxy URL.

5. **Keycloak as Okta mock** — Keycloak supports the same OIDC protocol as Okta. The API Server uses provider-agnostic OIDC validation, so switching from Keycloak (local) to Okta (production) requires only changing the OIDC_ISSUER environment variable.

6. **Vault dev mode** — HashiCorp Vault runs in dev mode locally (in-memory, pre-seeded). Production uses the same Vault API with proper authentication (AppRole or K8s auth method).

7. **K3s in Docker** — Provides a real Kubernetes API for local development. Session Pods, NetworkPolicies, and ResourceQuotas work identically to production. The agent runtime image is loaded directly into K3s's containerd.

8. **Multi-backend Inference Router** — Agents can use Claude API (frontier), Ollama (local GPU), or vLLM (local GPU). Ollama natively supports the Anthropic API format, so Claude and Ollama use the same Anthropic SDK with different `base_url`. vLLM uses OpenAI-compatible endpoint. Ollama and vLLM are pre-existing WSL2 services, not managed by this project. This allows cost optimization (local models for simple tasks, Claude for complex reasoning) and air-gapped deployments.

9. **Python Agent Runtime** — The agent runtime inside session Pods uses Python + FastAPI, matching the API Server's language. Claude Agent SDK's Python package supports `query()` with the same capabilities as the Node.js version. This simplifies the codebase (one language for backend + runtime) and makes it easier for the team to contribute.

10. **Session visibility model** — Private sessions support email-based invitation; team sessions auto-expose to team members. Invited users can chat but never see/edit agent configuration. This separates "agent builder" (owner/admin) from "agent consumer" (user/invitee) concerns.

11. **Catalog ACL** — Agent discoverability follows visibility setting. Private agents are invisible to non-ACL users. Team agents are visible to team members. Public agents are visible to everyone. Platform admins bypass all visibility restrictions. Users who can see an agent in the catalog can create their own sessions without needing explicit permission.

---

## Environment Variables

### API Server (.env)

```env
# Database
DATABASE_URL=postgresql://agentbox:agentbox@localhost:5432/agentbox

# Redis
REDIS_URL=redis://localhost:6379/0

# Auth (OIDC)
OIDC_ISSUER=http://localhost:8080/realms/agentbox
OIDC_CLIENT_ID=agentbox-web
OIDC_AUDIENCE=agentbox-api

# Vault
VAULT_ADDR=http://localhost:8200
VAULT_TOKEN=dev-root-token

# K8s
KUBECONFIG=./kubeconfig.yaml

# Agent Runtime
AGENT_RUNTIME_IMAGE=agentbox-runtime:latest
DEFAULT_SESSION_TIMEOUT=1800         # 30 minutes
DEFAULT_MAX_SESSIONS_PER_AGENT=20

# Inference Backends (Ollama/vLLM run on WSL2 locally, not in Docker)
ANTHROPIC_API_KEY=sk-ant-...
INFERENCE_OLLAMA_URL=http://localhost:11434
INFERENCE_VLLM_URL=http://localhost:8001
```

### Web UI (.env.local)

```env
NEXT_PUBLIC_API_URL=http://localhost:8000
NEXT_PUBLIC_WS_URL=ws://localhost:8000
NEXT_PUBLIC_OIDC_ISSUER=http://localhost:8080/realms/agentbox
NEXT_PUBLIC_OIDC_CLIENT_ID=agentbox-web
```
