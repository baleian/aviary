---
title: How it works
description: The parts of Aviary, and the path of a message from the API to the sandbox of its session and back.
---

![The parts of Aviary, and what each of them talks to](../../../assets/diagrams/architecture.svg)

Clients call the API of the **Supervisor**. The Supervisor has
[Agent Substrate](https://github.com/agent-substrate/substrate) run a
**sandbox** for each session, an actor that runs the agent's harness in
gVisor with a home of its own, on one of the **workers** of the
installation, and gives the turns of the session to the **runner** in the
sandbox through the router of Substrate. When a session sleeps, a snapshot
of its sandbox and its home is kept in the **object storage**, a volume or
an S3 bucket, and the session can wake from it on any worker that has room.
The Supervisor keeps the files that messages carry there as well.

A sandbox reaches the outside only through the **egress gateway** of
Substrate, and only the hosts of its turn: the gateway asks a Supervisor for
the secrets of the turn's provider and servers, and puts them into the
requests. The Supervisors run as many replicas as the installation gives
them, and any replica serves any request.

## The path of a turn

![The path of a turn, from the message of a client to its last event](../../../assets/diagrams/turn.svg)

1. A client creates a project with a model provider and an agent that uses
   one of the provider's models, creates a session of the agent, and sends
   the session a message.
2. The **Supervisor** records the message as a turn and queues it. A
   Supervisor has Substrate create the session's **sandbox** from the
   template of the agent's harness, or wake it if it sleeps, on a worker
   that has room for it, and the turn leaves the queue. Queued turns get
   their places in the order in which they were sent, and when no worker
   has room, the sandbox that has been idle the longest is put to sleep to
   make room.
3. The Supervisor follows the turn until it ends, and gives it to the
   **runner** in the sandbox. The runner starts the **harness** on the
   first turn and talks to it over the
   [Agent Client Protocol](https://agentclientprotocol.com), so it does not
   depend on which harness it runs.
4. The harness works on the message in its home, `/home`. It reaches the
   agent's provider and the session's MCP servers at their URLs, through
   the [egress gateway](../operate/egress.md), which puts their secrets in
   place of the placeholders that the harness sends.
5. The runner reports what the agent does, the Supervisor records it, and
   the client reads it as a stream of events. If the Supervisor goes away,
   another one continues the turn from what was recorded.
6. When the runner reports that the turn has ended, the Supervisor ends the
   turn and stores what the agent said and did. The harness and the files
   stay in the sandbox for the next turn. A session with no running turn is
   put to sleep after a while, or when a queued turn needs its room: a
   [snapshot](../operate/storage.md#the-snapshot) keeps its sandbox, which
   leaves its worker. The next message wakes the
   session on a worker that has room, and the harness continues from there.

## What the Supervisor is connected to

The Supervisor keeps its records in a database and the events of running
turns in an event log, and reaches each through a plugin. Plugins for
PostgreSQL, MySQL and Valkey are included. They need only a URL, so both can
be managed services. [Plugins](../operate/plugins.md) lists the tested products and
versions, and describes how to connect other products. A model provider is a
URL and an API key, so it can be a provider's API, an AI gateway or your own
model server.

## Read on

- [Network and egress](../operate/egress.md): what a sandbox reaches, and
  how the egress gateway puts the secrets of providers and servers into its
  requests.
- [Sessions and storage](../operate/storage.md): how sessions sleep, what
  their snapshots keep, and where their files are kept.
- [Harness images](../operate/harness-images.md): how the runner talks to
  a harness, and what an image must provide.
- [Plugins](../operate/plugins.md): how the Supervisor reaches its
  database, its event log, secret stores and the identity provider.
