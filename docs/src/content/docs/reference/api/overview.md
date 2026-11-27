---
title: API overview
description: How the API authenticates and names things, and all of its requests.
---

The Supervisors serve the API under `/v1`. In the local cluster of the
[quick start](../../start/quick-start.md) it is at http://localhost:8080.

## Callers

With an identity provider, every request needs a token of that provider in
the `Authorization: Bearer <token>` header (see
[Authentication](../../operate/authentication.md)). A caller has the permissions that a
project grants to the caller and to the caller's groups (see
[Roles](../../use/projects.md#roles)). A project that the caller is not a member of returns 404,
and a request that needs a permission that the caller lacks returns 403.
Administrators may do everything. Without an identity provider, requests are
not authenticated, and every caller is an administrator.

## Names

Paths name a project, an agent or an MCP server by its ID. Each also has a
slug, chosen when it is created: 1 to 63 lowercase letters, digits and
hyphens, beginning with a letter and not ending with a hyphen. A project's
slug is unique in the installation, and the slug of an agent or an MCP
server is unique in its project. Slugs cannot be changed. The name of each
is optional and only for display; a client shows the slug when the name is
empty.

## Requests

| Request | |
| --- | --- |
| `POST /v1/projects` | [Create a project](./projects.md#create-a-project) |
| `GET /v1/projects` | [List projects](./projects.md#list-projects) |
| `GET /v1/projects/{id}` | [Read a project](./projects.md#read-a-project) |
| `GET /v1/projects/{id}/members` | [List members](./projects.md#list-members) |
| `POST /v1/projects/{id}/members` | [Add a member](./projects.md#add-a-member) |
| `PUT /v1/members/{id}` | [Change a member](./projects.md#change-a-member) |
| `DELETE /v1/members/{id}` | [Remove a member](./projects.md#remove-a-member) |
| `GET /v1/roles` | [List roles](./projects.md#list-roles) |
| `PUT /v1/roles/{name}` | [Change a role](./projects.md#change-a-role) |
| `GET /v1/me` | [Read the caller](./projects.md#read-the-caller) |
| `POST /v1/projects/{id}/providers` | [Create a model provider](./connections.md#create-a-model-provider) |
| `GET /v1/projects/{id}/providers` | [List model providers](./connections.md#list-model-providers) |
| `GET /v1/providers/{id}` | [Read a model provider](./connections.md#read-a-model-provider) |
| `PUT /v1/providers/{id}` | [Replace a model provider](./connections.md#replace-a-model-provider) |
| `POST /v1/projects/{id}/providers/models` | [List the models of an endpoint](./connections.md#list-the-models-of-an-endpoint) |
| `GET /v1/providers/{id}/models` | [List the models of a provider](./connections.md#list-the-models-of-a-provider) |
| `POST /v1/projects/{id}/mcp-servers` | [Register an MCP server](./connections.md#register-an-mcp-server) |
| `GET /v1/projects/{id}/mcp-servers` | [List MCP servers](./connections.md#list-mcp-servers) |
| `GET /v1/mcp-servers/{id}` | [Read an MCP server](./connections.md#read-an-mcp-server) |
| `PUT /v1/mcp-servers/{id}` | [Replace an MCP server](./connections.md#replace-an-mcp-server) |
| `DELETE /v1/mcp-servers/{id}` | [Delete an MCP server](./connections.md#delete-an-mcp-server) |
| `POST /v1/projects/{id}/mcp-servers/tools` | [List the tools of a server](./connections.md#list-the-tools-of-a-server) |
| `POST /v1/mcp-servers/{id}/tools` | [Refresh the tools of a server](./connections.md#refresh-the-tools-of-a-server) |
| `POST /v1/projects/{id}/secret-stores` | [Register a secret store](./connections.md#register-a-secret-store) |
| `GET /v1/projects/{id}/secret-stores` | [List secret stores](./connections.md#list-secret-stores) |
| `GET /v1/secret-stores/{id}` | [Read a secret store](./connections.md#read-a-secret-store) |
| `PUT /v1/secret-stores/{id}` | [Replace a secret store](./connections.md#replace-a-secret-store) |
| `POST /v1/projects/{id}/agents` | [Create an agent](./agents.md#create-an-agent) |
| `GET /v1/projects/{id}/agents` | [List agents](./agents.md#list-agents) |
| `GET /v1/agents/{id}` | [Read an agent](./agents.md#read-an-agent) |
| `PUT /v1/agents/{id}` | [Replace an agent](./agents.md#replace-an-agent) |
| `DELETE /v1/agents/{id}` | [Delete an agent](./agents.md#delete-an-agent) |
| `GET /v1/harnesses` | [List harnesses](./agents.md#list-harnesses) |
| `POST /v1/agents/{id}/sessions` | [Create a session](./sessions.md#create-a-session) |
| `GET /v1/agents/{id}/sessions` | [List sessions](./sessions.md#list-sessions) |
| `GET /v1/sessions/{id}` | [Read a session](./sessions.md#read-a-session) |
| `PATCH /v1/sessions/{id}` | [Change the title of a session](./sessions.md#change-the-title-of-a-session) |
| `DELETE /v1/sessions/{id}` | [End a session](./sessions.md#end-a-session) |
| `PUT /v1/sessions/{id}/mcp-servers/{server}` | [Turn an MCP server on or off](./sessions.md#turn-an-mcp-server-on-or-off) |
| `DELETE /v1/sessions/{id}/mcp-servers` | [Reset the MCP servers of a session](./sessions.md#reset-the-mcp-servers-of-a-session) |
| `POST /v1/sessions/{id}/messages` | [Send a message](./sessions.md#send-a-message) |
| `POST /v1/sessions/{id}/attachments` | [Upload a file](./sessions.md#upload-a-file) |
| `GET /v1/attachments/{id}` | [Download a file](./sessions.md#download-a-file) |
| `GET /v1/limits` | [Read the limits](./sessions.md#read-the-limits) |
| `GET /v1/sessions/{id}/files` | [List a directory](./sessions.md#list-a-directory) |
| `GET /v1/sessions/{id}/files/search` | [Search the workspace](./sessions.md#search-the-workspace) |
| `GET /v1/sessions/{id}/files/content` | [Read a file](./sessions.md#read-a-file) |
| `PUT /v1/sessions/{id}/files/content` | [Write a file](./sessions.md#write-a-file) |
| `GET /v1/sessions/{id}/files/archive` | [Download a directory](./sessions.md#download-a-directory) |
| `GET /v1/sessions/{id}/turns` | [List the turns of a session](./turns.md#list-the-turns-of-a-session) |
| `GET /v1/turns/{id}` | [Read a turn](./turns.md#read-a-turn) |
| `POST /v1/turns/{id}/cancel` | [Cancel a turn](./turns.md#cancel-a-turn) |
| `POST /v1/turns/{id}/approvals/{request}` | [Answer an approval request](./turns.md#answer-an-approval-request) |
| `GET /v1/turns/{id}/events` | [Follow a turn](./events.md#turn-events) |
| `GET /v1/projects/{id}/events` | [Follow the sessions of a project](./events.md#session-changes) |
