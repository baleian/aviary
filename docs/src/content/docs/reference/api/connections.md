---
title: Connections
description: The requests for the model providers, MCP servers and secret stores of a project.
---

[Model providers](../../use/model-providers.md), [MCP servers](../../use/mcp-servers.md) and
[Secret stores](../../use/secret-stores.md) describe how each of them behaves.

## Model providers

### Create a model provider

```http
POST /v1/projects/{id}/providers
```

Creates a model provider in the project. Body: `{"name": "...", "url": "...", "api_key": {"value": "..."}}`. The URL is the base URL of a model endpoint, and the key is sent to it as a bearer token. A key in a secret store of the project is given by its location: `"api_key": {"store": "...", "path": "...", "key": "..."}`. An endpoint that takes no key has no `api_key`. An endpoint that needs other headers is given them: `"headers": [{"name": "...", "value": "..."}]`, or with a secret in place of a value, given as a key is: `{"name": "...", "secret": {"value": "..."}}`. See [Model providers](../../use/model-providers.md).

### List model providers

```http
GET /v1/projects/{id}/providers
```

Lists the model providers of the project in the order in which they were created: `{"providers": [...]}`.

### Read a model provider

```http
GET /v1/providers/{id}
```

Returns the provider.

### Replace a model provider

```http
PUT /v1/providers/{id}
```

Replaces the name, the URL, the headers and the key of a provider together: a provider given no headers or no key has none afterwards. While the URL stays the same, `{"stored": true}` in place of a secret keeps the one that is stored.

### List the models of an endpoint

```http
POST /v1/projects/{id}/providers/models
```

Lists the models of a model endpoint that need not be registered. Body: the `url`, the `headers` and the `api_key` of a provider, which is not stored, and the `provider` that it would replace, if there is one: `{"stored": true}` then keeps a secret of that provider as `PUT` would. Aviary asks the endpoint for its models, `GET models` under the URL, with the headers and the key and through the Egress Proxy, as the model requests of a turn go. Returns `{"reached": true, "status": 200, "models": ["..."], "duration_ms": 12}`: the status of the endpoint's answer, and the IDs of its models if the answer is a list of models as the OpenAI API gives it. An endpoint that did not answer returns `{"reached": false, "reason": "...", "duration_ms": 5003}`.

### List the models of a provider

```http
GET /v1/providers/{id}/models
```

Lists the models of a provider in the same way, with the URL, the headers and the key that it has.

## MCP servers

### Register an MCP server

```http
POST /v1/projects/{id}/mcp-servers
```

Registers an MCP server of the project. Body: `{"slug": "...", "name": "...", "url": "...", "transport": "http"}`, or `"transport": "sse"` for a server that is reached by SSE. Returns 409 if another server of the project has the slug. A server that needs headers is given them as a provider is, `authorization` among them. See [MCP servers](../../use/mcp-servers.md).

### List MCP servers

```http
GET /v1/projects/{id}/mcp-servers
```

Lists the MCP servers of the project in the order in which they were registered: `{"mcp_servers": [...]}`.

### Read an MCP server

```http
GET /v1/mcp-servers/{id}
```

Returns the server.

### Replace an MCP server

```http
PUT /v1/mcp-servers/{id}
```

Replaces all of an MCP server but its slug. While the URL stays the same, `{"stored": true}` in place of a secret keeps the one that is stored.

### Delete an MCP server

```http
DELETE /v1/mcp-servers/{id}
```

Deletes an MCP server.

### List the tools of a server

```http
POST /v1/projects/{id}/mcp-servers/tools
```

Lists the tools of an MCP server that need not be registered. Body: the `url`, the `transport` and the `headers` of a server, which is not stored, and the `server` that it would replace, if there is one: `{"stored": true}` then keeps a secret of that server as `PUT` would. Returns `{"reached": true, "status": 200, "tools": [{"name": "...", "description": "..."}], "duration_ms": 230}`, the `status` of the answer that was not a success, or `{"reached": false, "reason": "...", "duration_ms": 5003}` for a server that did not answer.

### Refresh the tools of a server

```http
POST /v1/mcp-servers/{id}/tools
```

Lists the tools of an MCP server again in the same way, and keeps them if it lists them.

## Secret stores

### Register a secret store

```http
POST /v1/projects/{id}/secret-stores
```

Registers a secret store of the project. Body: `{"name": "...", "kind": "...", "settings": {...}, "credential": {"value": "..."}}`. See [Secret stores](../../use/secret-stores.md).

### List secret stores

```http
GET /v1/projects/{id}/secret-stores
```

Lists the secret stores of the project in the order in which they were registered: `{"secret_stores": [...]}`.

### Read a secret store

```http
GET /v1/secret-stores/{id}
```

Returns the store.

### Replace a secret store

```http
PUT /v1/secret-stores/{id}
```

Replaces the name, the kind, the settings and the credential of a secret store together.
