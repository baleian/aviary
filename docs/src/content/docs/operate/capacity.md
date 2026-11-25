---
title: Capacity and availability
description: How many turns run at once, what waits, and what happens when a part of Aviary is replaced.
---

## Turns

`runtime.maxConcurrentTurns` is the number of turns that run at once, 50 by
default. Each of them has a sandbox pod with the resources of its harness,
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

Sandbox pods exist for the running turns and for the sessions that were in
use within the last `sandboxes.idleSeconds`. Volumes exist for the sessions
that have not ended and whose homes are not archived. Aviary sets no other
limit on them; to set one, use a ResourceQuota on `aviary-runtime`.

## Replicas

The Supervisors, the Egress Proxies and the depots each run as many
replicas as their `replicas` say, and any replica can serve any request.
Turns do not belong to a Supervisor: if the one that follows a turn goes
away, another one continues the turn from what was recorded.

Supervisors and Egress Proxies do not wait for turns when they stop, as when
their deployment is scaled in or rolled out. Another Supervisor continues
the turns from what was recorded, and the requests that a stopping proxy was
forwarding fail. A depot that stops finishes the transfers that are under
way, for as long as `runtime.maxRestoreSeconds`.
