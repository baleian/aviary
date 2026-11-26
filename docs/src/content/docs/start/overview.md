---
title: What is Aviary
description: What Aviary does, who it is for and where it stands.
---

Aviary runs AI agent harnesses, such as OpenCode, Codex and Claude Code, on
Kubernetes. Each message that a user sends is a turn, and each session runs
its turns in a sandbox of its own, which keeps the conversation and the
files of the session between turns: an actor of
[Agent Substrate](https://github.com/agent-substrate/substrate) that runs
the agent's harness in gVisor.

## What it does

Aviary is built for running agents for many users on infrastructure of your
own:

- **Controlled egress.** An agent reaches only the hosts that the
  installation allows and those of its turn, and every secret that goes
  into a request is logged with the turn and the user that it came from.
- **No keys in sandboxes.** Each agent names its model at a model provider
  that its project registers, and no sandbox holds the provider's API key.
- **Persistent sessions.** Sessions continue across turns, and their
  conversations can be read back through the API.
- **Sleeping sessions.** A session that is not in use is put to sleep: a
  snapshot keeps it, so it uses no CPU or memory. Its next turn wakes it on
  any worker that has room.
- **A bounded pool.** The workers that hold the awake sandboxes are of the
  number and the size that the installation gives them.
- **A queue.** The number of turns that run at once is a setting. Further
  turns wait in order.
- **Approval.** An agent can be made to ask a person before it acts.
- **Access control.** With an identity provider, callers are authenticated
  and limited to what they may do in each project.
- **Replaceable Supervisors.** Supervisors can be replaced while turns run.

[How it works](./how-it-works.md) follows a message from the API to
its sandbox and back.

## Who it is for

- **People who give agents work.** They connect their model providers,
  describe their agents, and work with them in the web UI. See
  [Use Aviary](../use/web-ui.md). Aviary runs in a Kubernetes cluster,
  and the [quick start](./quick-start.md) creates one on your
  machine.
- **Platform engineers.** They install Aviary on a cluster for the users of
  an organization, connect it to its database, identity provider and
  storage, and decide what the sandboxes run and reach. See
  [Operate Aviary](../operate/installation.md).
- **Contributors.** They want to know how the parts fit together, build and
  test them, and extend them. See [Contribute](../contribute/overview.md).

## Status

Aviary is at an early stage and has only run on a local cluster.

## Limitations

- **No inspection of what an agent sends.** Aviary does not read what a
  sandbox sends over HTTPS, and the egress gateway reads only the requests
  into which it puts secrets.
- **No measured token usage.** The usage of a turn is what its harness
  reports.
- **No copy of the files of an ended session.** Its sandbox is deleted with
  its snapshot, and Aviary keeps no copy.
- **Secrets by host.** The egress gateway puts a secret into a header of
  every request to its host, whatever the path: see
  [Network and egress](../operate/egress.md#secrets).
- **Tested only on a local cluster.** Aviary has not run on a cloud
  provider's Kubernetes or under load, and its depot has not run on a
  network file system or a mounted bucket.
