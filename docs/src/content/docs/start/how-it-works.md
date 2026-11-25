---
title: How it works
description: The path of a message from the API to the sandbox of its session and back.
---

```
            messages, events              creates, wakes, puts to sleep, deletes
  client ───────────────────▶ Supervisor ─────────────────────────▶ agent-sandbox
                               │  ▲   ▲                                  │
                    database   │  │   │ HTTP: turns down,                ▼
                    event log  │  │   └─ events up ───────────────── session sandbox
                                  │                                ┌─────────────────┐
                                  │                                │ runner          │
                                  │                                │   │ ACP         │
                                  │                                │ harness         │
                                  │                                │   /home: volume │
                                  │                                └────────┬────────┘
                                  │ asks about each request:                │ all outbound traffic
                                  └─ its turn, the model API key ──── Egress Proxy ──▶ model endpoint,
                                                                                       allowed hosts
```

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
   The runner reports what the agent does, the Supervisor records it, and
   the client reads it as a stream of events. If the Supervisor goes away,
   another one continues the turn from what was recorded.
5. When the runner reports that the turn has ended, the Supervisor ends the
   turn and stores what the agent said and did. The harness and the files
   stay in the sandbox for the next turn. A session with no running turn is
   put to sleep after a while: its pod is deleted, and its volume is kept
   with the harness's memory of the conversation. The next message wakes the
   session in a new pod, and the harness continues from there. A session
   that sleeps for long gives up the volume too: its home is archived in the
   [depot](../operate/storage.md#the-depot), and the next message restores it into a new sandbox.
6. Everything that the sandbox sends out goes through the
   [Egress Proxy](../operate/egress.md).

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
