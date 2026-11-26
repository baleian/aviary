---
title: Concepts
description: The words that the rest of the documentation uses.
---

## Project

A project holds what a team works with: its model providers, MCP servers,
secret stores and agents, and the members who may use them. A member is a
user or a group, and a [role](../use/projects.md#roles) decides what the
member may do in the project. Administrators create projects.

## Model provider

A model provider is the base URL of a model endpoint and its API key, so it
can be a provider's API, an AI gateway or your own model server. A project
registers its providers. No sandbox holds a provider's key: the egress
gateway puts it into the model requests of a turn on their way out. See
[Model providers](../use/model-providers.md).

## Agent

An agent is what a session talks to: a harness, a model at one of the
project's providers and, if it needs them, instructions, MCP servers and the
rule that it asks a person before it acts. See [Agents](../use/agents.md).

## Harness

A harness is the agent program that runs the turns of a session, such as
OpenCode, Codex or Claude Code. An installation lists the harnesses that
agents can use, each with its image. See
[Harness images](../operate/harness-images.md).

## Session

A session is one conversation with an agent. It belongs to the user who
created it and runs in a sandbox of its own, which keeps the conversation
and the files of the session between turns. See
[Sessions and turns](../use/sessions.md).

## Turn

Each message that a user sends is a turn. A turn runs at once if fewer turns
are running than the installation allows, and waits in a queue otherwise.
While it runs, what the agent says and does is streamed as events. When it
has ended, it is kept as part of the conversation.

## Sandbox

The sandbox of a session is an actor of Agent Substrate that runs the
agent's harness, with a home of its own. A session that is not in use is put
to sleep: a snapshot keeps its sandbox, which leaves its worker, and its
next turn wakes it on a worker that has room. See
[Sessions and storage](../operate/storage.md).

## Approval

An agent can be made to ask a person before it acts. Its turn then waits
until someone answers. See [Approvals](../use/approvals.md).

## MCP server

A project registers MCP servers by their URL. An agent names the servers
that its sessions begin with, and a session turns any server of the project
on or off. See [MCP servers](../use/mcp-servers.md).

## Secret store

A project that keeps its secrets in a store of its own can register the
store, and then give the location of a secret instead of the secret itself.
See [Secret stores](../use/secret-stores.md).

## The parts of an installation

- The **Supervisor** serves the API, records the turns, keeps the queue and
  has the sandboxes created, woken and put to sleep.
- The **runner** runs in each sandbox. It starts the harness and reports
  what the agent does.
- The **workers** of Agent Substrate hold the sandboxes while they are
  awake, and its **egress gateway** is the only way out of a sandbox.
- The **depot** keeps the files that messages carry.

[How it works](./how-it-works.md) shows them together.
