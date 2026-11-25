---
title: Sessions and storage
description: When a session sleeps, when its home is archived, and where its files are kept.
---

A session keeps its conversation and its files in the home of its sandbox,
`/home`, which is a volume. The installation decides how long the pod and
the volume of a session stay.

## The life of a sandbox

![What a session has while it is awake, asleep and archived, with the default settings](../../../assets/diagrams/sandbox-life.svg)

1. **Awake.** The first turn of a session creates its sandbox: a pod that
   runs the image of the agent's harness, with the session volume.
2. **Asleep.** `sandboxes.idleSeconds` without a running turn, 300 by
   default, put the session to sleep: its pod is deleted and its volume is
   kept, and its next turn wakes it in a new pod.
3. **Archived.** `sandboxes.archiveAfterSeconds` of sleep, a week by
   default, archive the home of the session in the [depot](#the-depot): its
   sandbox is deleted with its volume, and its next turn restores the home
   into a new sandbox.
4. **Ended.** A session ends when its owner ends it, when its agent is
   deleted, or `sandboxes.retentionSeconds` after its last turn, which by
   default is never. Its sandbox is deleted with its volume, or the archive
   of its home, and its conversation stays readable.

Sleeping, archiving and the end after a time can each be turned off: see
[Configuration](../reference/configuration.md#sandboxes).
[Capacity and availability](./capacity.md#sandboxes) says which
pods and volumes exist at a time.

## The session volume

`runtime.volume` describes the session volume, which is mounted at `/home`:
the state of the harness, the files of the agent and what the agent installs
with npm or pip. It is 10Gi by default, in the default storage class of the
cluster.

The volume is deleted with its sandbox when its session is ended or expires,
or when its home is archived. Whether its data is deleted too depends on the
storage class: the default class of the local cluster deletes it, a class
with `reclaimPolicy: Retain` keeps it, and some file storage drivers keep it
unless they are configured not to.

## The depot

The depot is where the files that outlive a sandbox are kept, as objects in
a volume. Its pods run the image of the Supervisor with the command `depot`,
behind one Service. They keep nothing but the volume, which all of them
mount: any of them serves any request, and a new one starts before the one
that it replaces stops. A network policy lets only the Supervisors connect
to them.

The objects are the files that messages carry, each kept from its upload
until its session ends, and the homes of the sessions whose sandboxes have
slept for `sandboxes.archiveAfterSeconds`. A Supervisor wakes such a sandbox, has its
runner pack the home, writes the archive to the depot, and deletes the
sandbox with its volume. A message that arrives meanwhile is not held up:
its turn takes the sandbox, and the home is archived later. The next turn of
a session whose home is archived gets a new sandbox, in which the runner
unpacks the archive before the harness starts. The archive is then deleted.
An archive has the directories, files and links of the home, with their
modes and times. A home that cannot be archived stays in its volume and is
tried again later, and an archive that cannot be unpacked stays in the depot
while the turn fails.

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
then a file in the volume of the gateway, at `depot/<key>`.
