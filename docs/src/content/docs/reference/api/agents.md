---
title: Agents
description: The requests for agents, and for the harnesses that they can use.
---

## Agents

### Create an agent

```http
POST /v1/projects/{id}/agents
```

Creates an agent. Body: `{"slug": "...", "name": "...", "description": "...", "harness": "...", "model": {"provider": "...", "name": "..."}, "instructions": "...", "mcp_servers": ["..."]}`. The optional description says what the agent is for, and like the name is only for display. The harness is one of those that `GET /v1/harnesses` lists. The model is a provider of the project, by ID, and the provider's name for the model. The optional instructions are given to the agent before every turn. The agent acts without asking (see [Approvals](../../use/approvals.md)). The optional MCP servers, by ID, are servers of the project that the agent's sessions begin with (see [MCP servers](../../use/mcp-servers.md)). Returns 409 if another agent of the project has the slug.

### List agents

```http
GET /v1/projects/{id}/agents
```

Lists the agents of the project that are not deleted, in the order in which they were created: `{"agents": [...]}`.

### Read an agent

```http
GET /v1/agents/{id}
```

Returns the agent, also after it was deleted.

### Replace an agent

```http
PUT /v1/agents/{id}
```

Replaces the name, the description, the model, the instructions and the MCP servers of an agent together: what the body leaves out is empty afterwards, and the model is required. The slug and the harness cannot be changed, and the body may leave them out. The agent's sessions run their next turn with the change. Returns 200 with the agent.

### Delete an agent

```http
DELETE /v1/agents/{id}
```

Deletes an agent. Returns 200 with the agent, which then has `deleted_at`. Its sessions end as when a caller ends them, with the `end_reason` `agent_deleted`: their sandboxes are deleted, and their conversations stay readable. A deleted agent is no longer listed, and changing it or creating a session of it returns 409. `GET /v1/agents/{id}` still returns it, and its slug is free for another agent. Besides `agents.manage`, the caller needs `sessions.control_all`, since the sessions of every member end. Returns 409 if a turn of one of its sessions has not ended.

## Harnesses

### List harnesses

```http
GET /v1/harnesses
```

Lists the harnesses that agents can use, by name: `{"harnesses": [{"name": "codex"}, {"name": "opencode"}]}`. They are the installation's `runtime.harnesses`.
