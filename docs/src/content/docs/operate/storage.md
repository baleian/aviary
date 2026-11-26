---
title: Sessions and storage
description: When a session sleeps, what its snapshot keeps, and where the snapshots and the files of messages are kept.
---

A session keeps its conversation and its files in the home of its sandbox,
`/home`. The installation decides how long the sandbox of a session stays
awake, what its snapshot keeps while it sleeps, and when it is deleted.

## The life of a sandbox

![What a session has while it is awake, asleep and ended, with the default settings](../../../assets/diagrams/sandbox-life.svg)

1. **Awake.** The first turn of a session creates its sandbox: an actor of
   [Agent Substrate](https://github.com/agent-substrate/substrate) that runs
   the image of the agent's harness in gVisor, on a worker that has room for
   it.
2. **Asleep.** `sandboxes.idleSeconds` without a running turn, 300 by
   default, put the session to sleep: a snapshot keeps the sandbox, and it
   leaves its worker. Its next turn wakes it from the snapshot on a worker
   that has room.
3. **Ended.** A session ends when its owner ends it, when its agent is
   deleted, or `sandboxes.retentionSeconds` after its last turn, which by
   default is never. Its sandbox is deleted with its snapshot, and its
   conversation stays readable.

Sleeping and the end after a time can each be turned off: see
[Configuration](../reference/configuration.md#sandboxes).
[Capacity and availability](./capacity.md#sandboxes) says which sandboxes
take the room of workers.

## The snapshot

`sandboxes.snapshot` says what the snapshot of a sleeping sandbox keeps:

| Value | What a sandbox wakes with |
| --- | --- |
| `memory`, the default | Its processes and their memory, its files and its home. A harness goes on where it was, and so does a turn that was running when the sandbox moved. |
| `rootfs` | Its files and its home. The harness starts again, and resumes its session from the home. |
| `volumes` | Its home. The harness starts again, and resumes its session from the home. |

The home is the durable directory of the actor: the state of the harness,
the files of the agent and what the agent installs with npm or pip. A
snapshot takes about as much as the sandbox uses, not its limits, and is
compressed when it leaves its worker.

The worker that takes a snapshot keeps it, and uploads it to the
[object storage](#the-object-storage) after
`substrate.snapshots.uploadDelay`, 5 minutes by default. After
`substrate.snapshots.detachDelay`, 30 minutes by default, the snapshot
leaves the worker: a sandbox that wakes later wakes from the object
storage, on any worker that has room. So the workers keep only the
snapshots of the sessions that went to sleep lately, and the object storage
keeps those of all the sessions that have not ended. A sandbox whose worker goes away, as when
its node is drained, is put to sleep by a Supervisor first and uploads its
snapshot at once. A sandbox that crashes is reverted to its last snapshot,
and the turn that it ran fails.

A sandbox keeps the template that it was made with: a harness whose image or
resources the installation changes applies to new sessions.

## The object storage

The object storage keeps the snapshots of sleeping sandboxes and the files
that messages carry, under `atespaces/` and `attachments/` of its location.
A file is uploaded before the message that carries it, and is kept until
its session ends; one that no message carries is deleted after an hour. A
Supervisor gives the files of a message to the runner of its sandbox before
the turn of the message starts, and a client reads them back from the API.

By default the chart keeps the objects in a volume of the namespace of the
release, through an S3 gateway of its own, the
[Versity S3 Gateway](https://github.com/versity/versitygw): each object is
a file under `buckets/aviary/` of the volume. The gateways keep nothing
else, and any of them serves any request. The volume can be any that the
cluster has, under `substrate.store.volume`:

- By default it is a volume of the cluster's default storage class that one
  node mounts, and the gateways run on that node together.
- With `substrate.store.volume.accessMode: ReadWriteMany` it is a volume
  that many nodes mount at once, such as a network file system, and the
  gateways run on any node.
- With `substrate.store.volume.existingClaim` it is a claim of your own,
  such as one of a directory of a node.

Only the API server of Substrate, its agent on the nodes and the
Supervisors reach the gateways. The claim that the chart makes is kept when
the chart is uninstalled. Its storage class and access mode cannot be
changed afterwards. To move the objects to another volume, copy the files as
they are and name the claim of that volume in
`substrate.store.volume.existingClaim`.

`substrate.objectStorage.location` keeps the objects in an S3 bucket
instead, such as one of Amazon S3, reached with the S3 API that
`substrate.objectStorage` describes. The API server of Substrate, its agent
and the Supervisors then read, write, list and delete the objects under the
location, with its key or the identity of their service accounts. A bucket
that a CSI driver mounts as a volume cannot be the volume of the store: such
a mount does not take the renames and the small files of the gateway's own
records.
