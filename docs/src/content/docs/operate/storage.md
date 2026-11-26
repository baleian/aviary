---
title: Sessions and storage
description: When a session sleeps, what its snapshot keeps, and where its files are kept.
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

The worker that takes a snapshot keeps it, and uploads it to the store of
the snapshots after `substrate.snapshots.uploadDelay`, 5 minutes by
default. After `substrate.snapshots.detachDelay`, 30 minutes by default,
the snapshot leaves the worker: a sandbox that wakes later wakes from the
store, on any worker that has room. So the workers keep only the snapshots
of the sessions that went to sleep lately, and the store keeps those of all
the sessions that have not ended. A sandbox whose worker goes away, as when
its node is drained, is put to sleep by a Supervisor first and uploads its
snapshot at once. A sandbox that crashes is reverted to its last snapshot,
and the turn that it ran fails.

### The store

By default the chart keeps the snapshots in a volume of the namespace of
the release, through an S3 gateway of its own, the
[Versity S3 Gateway](https://github.com/versity/versitygw): each is a few
files under `buckets/snapshots/` of the volume. The volume can be any that
the cluster has, under `substrate.store.volume`, as for
[the depot](#the-depot): a disk that one node mounts, by default, with the
gateways on that node; a volume that many nodes mount, such as a network
file system; or a claim of your own, such as one of a directory of a node.
Only the API server of Substrate and its agent on the nodes reach the
gateways. Uninstalling the chart keeps the claim that it made.

`substrate.snapshots.location` keeps the snapshots in an S3 bucket instead,
such as one of Amazon S3, reached with the S3 API that
`substrate.objectStorage` describes. A bucket that a CSI driver mounts as a
volume cannot be the volume of the store: such a mount does not take the
renames and the small files of the gateway's own records.

A sandbox keeps the template that it was made with: a harness whose image or
resources the installation changes applies to new sessions.

## The depot

The depot is where the files that messages carry are kept, as objects in a
volume, until their sessions end. Its pods run the image of the Supervisor
with the command `depot`, behind one Service. They keep nothing but the
volume, which all of them mount: any of them serves any request, and a new
one starts before the one that it replaces stops. A network policy lets only
the Supervisors connect to them. A Supervisor gives the files of a message
to the runner of its sandbox before the turn of the message starts.

An object is a file at the path of its key. It is written once, under its
own name, and is never changed. So the depots need not coordinate, and the
volume needs only to let files be created, read and deleted:

- By default it is a volume of the cluster's default storage class that one
  node mounts, and the depots run on that node together.
- With `depot.volume.accessMode: ReadWriteMany` it is a volume that many
  nodes mount at once, such as a network file system, and the depots run on
  any node.
- With `depot.volume.existingClaim` it is a claim of your own, such as one
  of a bucket that a CSI driver mounts as a file system. The mount must let
  the user of the image, 65532, write and delete files.

The claim that the chart makes is kept when the chart is uninstalled. Its
storage class and access mode cannot be changed afterwards. To move the
objects to another volume, copy the files as they are and name the claim of
that volume in `depot.volume.existingClaim`.

## Try a bucket locally

To try the depot on a bucket, `make versitygw` installs two things in the
local cluster: the [Versity S3 Gateway](https://github.com/versity/versitygw),
which serves a directory as buckets, and the
[Mountpoint for Amazon S3 CSI driver](https://github.com/awslabs/mountpoint-s3-csi-driver),
which mounts a bucket as a volume. It makes the claim `aviary-depot-bucket`
of the gateway's bucket `depot`, as `hack/kind/versitygw.yaml` describes it:
the mount options name the gateway, and let the user 65532 write and delete
files. Aviary keeps its objects there when it is installed with
`make deploy VALUES=hack/kind/aviary-versitygw-values.yaml`. Each object is
then a file in the volume of the gateway, at `buckets/depot/<key>`. What the
gateway knows of an object besides its bytes, such as its ETag, is in files
under `meta`.
