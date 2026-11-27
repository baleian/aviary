---
title: Network and egress
description: What a sandbox can reach, how the egress gateway of Agent Substrate puts the secrets of providers and MCP servers into its requests, and what is logged.
---

A sandbox reaches the outside only through the egress gateway of
[Agent Substrate](https://github.com/agent-substrate/substrate), and only the
hosts that its egress policy names.

![What leaves a sandbox, and what the egress gateway does with it](../../../assets/diagrams/egress.svg)

The Supervisor gives the sandbox of each session the policy of its turn:

- the hosts that every sandbox may reach, `sandboxes.allowedHosts`;
- the host of the model provider of the turn's agent;
- the hosts of the MCP servers that the session has on.

The gateway refuses a connection to any other host, and the sandbox resolves
only the names of the hosts that it may reach. A name is resolved as it is
given, so a Service of the cluster needs its full name, such as
`models.llm.svc.cluster.local`. A host is a name: an IP address is reached
only through a pattern that allows every host, such as `https://*`.

## Secrets

A sandbox holds no secret of a provider or of a server. In place of the
provider's API key and of each header that has a secret, the harness sends a
placeholder, `aviary`. The gateway reads the requests to such a host, with a
certificate of its own that the sandbox trusts, asks a Supervisor for the
secret, and puts it in the placeholder's place. A Supervisor gives a secret
only while a turn runs in the sandbox's session, and only one of the turn's
provider or of an MCP server that the session has on. A secret in a secret
store is read when the gateway asks for it, and cached for
`supervisor.secretStores.cacheSeconds`.

The gateway puts a secret into a header of every request to its host and
port, whatever its path. So a model provider and an MCP server, or two MCP
servers, at one host and port cannot both have a header of the same name
when one of them has a secret: such an MCP server is left out of the turn,
and the Supervisor logs that.

The connections to the other hosts pass through the gateway unread.

## What the sandboxes may reach

- `sandboxes.allowedHosts` lists the hosts that every sandbox may reach, as
  URLs such as `https://*.example.com`. By default, any host on the ports of
  HTTPS and HTTP.
- `sandboxes.localhost` names the host that stands in for `localhost` in the
  URLs of model providers and MCP servers, for an installation on one
  machine.

See [Configuration](../reference/configuration.md#sandboxes) for the values.

## The log

The gateway logs every connection that passes through unread, with the name
of its host as `sni`, and every request that it reads, with its
`authority`, `method`, `path` and `status`. Each line of JSON has the `time`
at which the connection or the request started and its `duration_ms`, and
says whom it is for, as a Supervisor answered the gateway when the sandbox
opened the connection or sent the request:

- `turn`, `session`, `agent` and `project`: the turn that runs in the
  session or, between turns, the one that ran last;
- with an identity provider, `user`, `username` and `subject`: who sent the
  turn's message, as the user ID that `GET /v1/me` returns, the user's name
  if the identity provider gives one, and the identity provider's subject.

The gateway asks about a request only once the policy of the sandbox allows
it, and a connection or a request that no Supervisor answers for fails,
after three retries. The lines are on the standard output of the `envoy`
container of the Deployment `atenet-egress` in `ate-system`.

A Supervisor logs every secret that it gives the gateway as a line of JSON
on its standard output, with the same fields and with `provider` or
`mcp_server`, whose secret it is, and `header`.

How long the lines are kept depends on the cluster's log collection.
