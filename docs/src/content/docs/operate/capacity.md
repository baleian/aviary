---
title: Capacity and availability
description: How many turns run at once, what waits, and what happens when a part of Aviary is replaced.
---

## Turns

`runtime.maxConcurrentTurns` is the number of turns that run at once, 50 by
default. Each of them has an awake sandbox, of the size of its harness,
which `runtime.harnesses` sets.

Further turns wait in a queue, in the order in which they were sent. Beyond
`runtime.maxQueuedTurns`, 500 by default, new messages get 429, and a turn
that is queued for longer than `runtime.maxQueueWaitSeconds`, 120 by
default, expires. A turn fails if it takes longer than
`runtime.maxStartupSeconds` to reach its harness, or runs for longer than
`runtime.maxTurnSeconds`. See
[Configuration](../reference/configuration.md#runtime) for all of them,
and [Metrics](../reference/metrics.md) for the queue and the turns as
they are.

## Sandboxes

The workers of Agent Substrate hold the sandboxes while they are awake: the
running turns, and the sessions that were in use within the last
`sandboxes.idleSeconds`. A worker has the room of its `workers.resources`,
and holds the sandboxes whose limits fit in it, so `workers.replicas` and
`workers.resources` bound the sessions that are awake at once, and the
resources that they take. A sleeping sandbox takes no room: its snapshot
is in the [store of the snapshots](./storage.md#the-store), and a session
that exists costs only that. A turn whose sandbox finds no worker with room waits for one, and fails if
none has room within `runtime.maxStartupSeconds`. To have more room, add
workers, by hand or with an autoscaler of the WorkerPool.

## Replicas

The Supervisors and the depots each run as many replicas as their
`replicas` say, and any replica can serve any request.
Turns do not belong to a Supervisor: if the one that follows a turn goes
away, another one continues the turn from what was recorded.

What is recorded of a running turn is in the event log until the turn ends.
If the log loses it, as one that keeps its data in memory does when it
starts again, the Supervisor reads the events of the turn from its runner
again and records them, so the turn goes on with all that the agent said and
did. A turn whose sandbox is gone as well fails with what the log has left
of it.

Supervisors do not wait for turns when they stop, as when their deployment
is scaled in or rolled out. Another Supervisor continues the turns from what
was recorded. A depot that stops finishes the transfers that are under way,
for as long as `runtime.maxStartupSeconds`. A worker that goes away has its
sandboxes put to sleep before it goes: with `memory` snapshots their turns go
on on another worker.
