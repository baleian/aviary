---
title: How it works
description: The parts of Aviary, and the path of a message from the API to the sandbox of its session and back.
---

![The parts of Aviary, and what each of them talks to](../../../assets/diagrams/architecture.svg)

Clients call the API of the **Supervisor**. The Supervisor has
[agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) create a
**sandbox** for each session, a pod that runs the agent's harness with a
volume as its home, and gives the turns of the session to the **runner** in
that pod. It keeps the files that outlive a sandbox in the **depot**.

A network policy closes the sandboxes: only the Supervisor connects to a
sandbox, and a sandbox connects only to the **Egress Proxy**, which asks the
Supervisor about each request before it lets the request through. The
Supervisors, the Egress Proxies and the depots each run as many replicas as
the installation gives them, and any replica serves any request.

## The path of a turn

![The path of a turn, from the message of a client to its last event](../../../assets/diagrams/turn.svg)

1. A client creates a project with a model provider and an agent that uses
   one of the provider's models, creates a session of the agent, and sends
   the session a message.
2. The **Supervisor** records the message as a turn. If fewer turns are
   running than the limit allows, a Supervisor takes the turn and follows it
   until it ends. Otherwise the turn is queued, and queued turns run in the
   order in which they were sent.
3. The Supervisor has agent-sandbox create the session's **sandbox**, a pod
   that runs the agent's harness with the session volume, or wake it if it
   sleeps. It then gives the turn to the **runner** in the sandbox. The
   runner starts the **harness** on the first turn and talks to it over the
   [Agent Client Protocol](https://agentclientprotocol.com), so it does not
   depend on which harness it runs.
4. The harness works on the message with the volume as its home, `/home`.
   Everything that the sandbox sends out goes through the
   [Egress Proxy](../operate/egress.md). The proxy asks the Supervisor whose
   request it is, and puts the key of the agent's provider into a request to
   the model, in place of the token of the session.
5. The runner reports what the agent does, the Supervisor records it, and
   the client reads it as a stream of events. If the Supervisor goes away,
   another one continues the turn from what was recorded.
6. When the runner reports that the turn has ended, the Supervisor ends the
   turn and stores what the agent said and did. The harness and the files
   stay in the sandbox for the next turn. A session with no running turn is
   put to sleep after a while: its pod is deleted, and its volume is kept
   with the harness's memory of the conversation. The next message wakes the
   session in a new pod, and the harness continues from there. A session
   that sleeps for long gives up the volume too: its home is archived in the
   [depot](../operate/storage.md#the-depot), and the next message restores it into a new sandbox.

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
  how the Egress Proxy puts a provider's key in place of the session's token.
- [Sessions and storage](../operate/storage.md): how sessions sleep, and
  where their files are kept.
- [Harness images](../operate/harness-images.md): how the runner talks to
  a harness, and what an image must provide.
- [Plugins](../operate/plugins.md): how the Supervisor reaches its
  database, its event log, secret stores and the identity provider.
