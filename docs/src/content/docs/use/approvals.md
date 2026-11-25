---
title: Approvals
description: How an agent asks a person before it acts, and what the answers do.
---

An agent created with `"approval": "ask"` asks a person before it acts.
Before the harness acts, the turn emits `approval.requested`, and the
harness waits. A client answers with `POST /v1/turns/{id}/approvals/{request}`
and one of the options that the harness offered. The harness continues with
that answer, and the turn emits `approval.resolved`, which says who
answered. While a request waits, it is listed in the turn's `approvals`,
both in `GET /v1/turns/{id}` and in the session's list of turns, so a client
that connects later can find it.

In the web UI, the request shows in the conversation with what the agent is
about to do and the answers that its harness offers, and the overview of the
project says that an agent is waiting for you.

![A request for approval in a conversation](../../../assets/screens/approval.png)

- **What needs approval depends on the harness** and on how its image
  configures it. Aviary passes the requests and the answers on unchanged. In
  the images in this repository, OpenCode asks before it runs a command or
  changes a file, Claude Code before it runs a command that does more than
  read the workspace or changes a file, and Codex before a command runs
  outside its sandbox: see [Harness images](../operate/harness-images.md). None of
  them asks before its own tool reads a file that a message carries.
- **A request waits as long as its turn may run.** The time spent waiting
  counts toward `runtime.maxTurnSeconds`, because the sandbox keeps running
  and the turn keeps its place among the running turns. A turn whose time
  runs out while it waits fails as `timed_out`, and the session's next
  message continues the conversation. Cancelling the turn ends its requests
  as `cancelled`.
- **What an option does depends on the harness too.** `allow_always` lasts
  as long as the harness remembers it: in OpenCode until the harness process
  ends, for example when the session sleeps, and in Codex and Claude Code for
  the rest of the session. After a refusal, OpenCode and Codex end the turn,
  Codex with the stop reason `cancelled`, while Claude Code tells the model
  and goes on.
- **Approval is not a security boundary.** The harness that asks runs in
  the sandbox, next to the agent's tools and as the same user. Approval
  keeps a well-meaning agent from acting on a mistake. What a sandbox can
  reach is limited by the network policy and the Egress Proxy, whatever was
  approved.

An agent without `approval` asks nobody. If its harness asks for permission
anyway, the runner allows the action once.
