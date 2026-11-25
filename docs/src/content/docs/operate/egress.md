---
title: Network and egress
description: What a sandbox can reach, how the Egress Proxy handles its requests, and what it logs.
---

Everything that a sandbox sends out goes through the Egress Proxy.

The Egress Proxy is [Envoy](https://www.envoyproxy.io), which the chart
installs. A network policy allows a sandbox to connect only to the proxy,
and the sandbox has no DNS.

A sandbox holds no model API key, only the token of its session. For each
request, the proxy asks the Supervisor which turn runs in the token's
session:

- For a model request, the Supervisor also returns the endpoint of the
  agent's provider, with its API key and the headers that it needs. The
  proxy puts the key in place of the token, sets the headers, and forwards
  the request to that endpoint.
- Any other request is let through to the hosts that the installation
  allows.
- A request from a session in which no turn runs is refused.

A response to a model request that the proxy makes itself, as when it does
not reach the endpoint, has the header `x-aviary-proxy`. The answers of an
endpoint do not.

## What the sandboxes may reach

- `egressProxy.allowedHosts` lists the hosts that the sandboxes may reach,
  those of model providers and MCP servers included. By default they reach
  any host.
- `egressProxy.insideCIDRs` are the address ranges that the Egress Proxy
  does not connect to, so that the sandboxes cannot use it to reach into the
  cluster. A model provider or an allowed host that is in the cluster or at
  a private address needs a rule in `egressProxy.extraEgress`.
- `egressProxy.localhost` names the host that stands in for `localhost` in
  the URLs of model providers, for an installation on one machine.

See [Configuration](../reference/configuration.md#egress-proxy) for the
values.

## The request log

The proxy logs every request as a line of JSON on its standard output. Each
line identifies the origin of its request on its own:

- `turn`, `session`, `agent` and `project`. The requests that list the
  [models of a provider](../use/model-providers.md) or the
  [tools of an MCP server](../use/mcp-servers.md) have only the `project`.
- With an identity provider, `user` and `subject`: who sent the turn's
  message, as the user ID that `GET /v1/me` returns and as the identity
  provider's subject, which identifies the user without Aviary's records.
- For a model request, `provider`: the model provider that it went to. For
  a request to an MCP server, `mcp_server`: the server.
- `time`, `host`, `path` (except for HTTPS URLs), `status`,
  `request_bytes`, `response_bytes` and `duration_ms`.

How long the lines are kept depends on the cluster's log collection.

## Metrics

An Egress Proxy serves the metrics of Envoy: see
[Metrics](../reference/metrics.md#egress-proxy).
