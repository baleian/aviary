---
title: Configuration
description: The values of the Helm chart.
---

These are the values of the Helm chart in `deploy/charts/aviary`. A values
file of your own sets them: with `--values` when the chart is
[installed](../operate/installation.md), and with
`make deploy VALUES=<file>` in the local cluster.

## Replicas and resources

| Value | Description |
| --- | --- |
| `supervisor.replicas` | Number of Supervisor replicas. Any replica can serve any request. Default 1. |
| `supervisor.resources` | Resource requests and limits of the Supervisor pods. |

## Supervisor

| Value | Description |
| --- | --- |
| `supervisor.database.secretName`, `.secretKey` | Secret in the release namespace that holds the database URL. The URL scheme selects the plugin: `postgresql://…` for PostgreSQL, `mysql://…` for MySQL. |
| `supervisor.events.secretName`, `.secretKey` | Secret in the release namespace that holds the event log URL: `valkey://…` for Valkey, `valkeys://…` for Valkey with TLS. |
| `supervisor.encryption.secretName`, `.key` | Secret with the keys that encrypt secrets, such as model provider API keys, before they are stored, and the entry that encrypts new secrets. Each entry is 32 random bytes in base64. To rotate, add an entry and name it in `key`, and keep the old entries. |
| `supervisor.secretStores.allowedHosts` | Hosts at which projects may register secret stores: `*` for any host, a host name for the host on the default HTTPS and HTTP ports, or a host name with a port. Empty, the default, disables secret stores. |
| `supervisor.secretStores.cacheSeconds` | Seconds that a value read from a secret store is cached. Default 60. 0 reads the store whenever the egress gateway asks for the secret. |
| `supervisor.auth.provider` | The identity provider that authenticates API requests: its `kind`, and `settings` for a provider of that kind. If `kind` is empty, the default, requests are not authenticated. See [Authentication](../operate/authentication.md). |
| `supervisor.auth.admins`, `.users` | Groups of the identity provider whose members administer the installation, and groups whose members may use the API besides the administrators. Both are empty by default: nobody is an administrator, and every authenticated user may use the API. |
| `supervisor.allowedOrigins` | Origins whose pages may call the API from a browser, such as `https://aviary.example.com`. `*` is every origin. Empty by default: a browser then lets only pages at the address of the API call it. |

## State metrics

| Value | Description |
| --- | --- |
| `stateMetrics.enabled`, `.resources` | Whether the installation serves its [state metrics](./metrics.md), and the resource requests and limits of the pod that serves them. Default true. |

## Depot

| Value | Description |
| --- | --- |
| `depot.replicas`, `.resources` | Number of [depot](../operate/storage.md#the-depot) replicas, and the resource requests and limits of their pods. Any replica can serve any request. Default 1. |
| `depot.volume.size`, `.storageClass` | The volume that holds the objects of the depot. Default 30Gi, in the default storage class of the cluster. Storage that has no limit of its own, such as a network file system, does not enforce the size. |
| `depot.volume.accessMode` | `ReadWriteOnce`, the default, for a volume that one node mounts: the depots run on that node together. `ReadWriteMany` for a volume that many nodes mount at once: the depots run on any node. |
| `depot.volume.existingClaim` | A claim of your own in the release namespace that holds the objects, instead of the one that the chart makes. `depot.volume.accessMode` says how it can be mounted. |

## Agent Substrate

| Value | Description |
| --- | --- |
| `substrate.namespace` | The namespace of the control plane of [Agent Substrate](../operate/installation.md#agent-substrate), whose API, router and egress gateway the Supervisors reach. Default `ate-system`. |
| `substrate.atespace` | The atespace that holds the sandboxes and their templates. Default `aviary`. |

## Workers

| Value | Description |
| --- | --- |
| `workers.replicas` | Number of the workers that hold the sandboxes while they are awake, in the namespace `aviary-runtime`. Default 2. |
| `workers.resources` | The room of each worker, as the limits of its pod: a worker holds the awake sandboxes whose limits fit in it, and a sandbox wakes on any worker that has room. Default 8 CPUs and 16Gi. See [Capacity](../operate/capacity.md). |
| `workers.image` | The image of the workers of Substrate, by its digest. |

## Sandboxes

| Value | Description |
| --- | --- |
| `sandboxes.idleSeconds` | Seconds without a running turn after which a session is put to sleep: its sandbox leaves its worker, and a snapshot keeps it. Its next turn wakes it on a worker that has room. Default 300. 0 disables sleeping. |
| `sandboxes.snapshot` | What the snapshot of a sleeping sandbox keeps: `memory` its processes and their memory, its files and its home, so a turn that was running goes on when it wakes; `rootfs` its files and its home; `volumes` its home. With `rootfs` and `volumes` the harness starts again when the sandbox wakes, and resumes its session. Default `memory`. A change applies to new sessions. |
| `sandboxes.allowedHosts` | Hosts that every sandbox may reach, besides the model provider and the MCP servers of its turn, as URLs: `https://*` for any host on the port of HTTPS, `https://*.example.com` for any host of a domain, `http://packages.example.com:8080` for a host on a port. A path is ignored. Default `["https://*", "http://*"]`. See [Network and egress](../operate/egress.md). |
| `sandboxes.localhost` | The host that the sandboxes and the Supervisors reach for a model provider or an MCP server whose URL is at `localhost` or a loopback address: a host name. Empty by default: such a provider is not reached. An installation on one machine can name that machine here. |
| `sandboxes.retentionSeconds` | Seconds after its last turn after which a session ends: its sandbox is deleted with its home, and its conversation stays readable. A session that ends this way has the `end_reason` `expired`. Default 0, which keeps the sandbox until the session is ended. |

## Runtime

| Value | Description |
| --- | --- |
| `runtime.maxConcurrentTurns` | Maximum number of turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | Maximum number of queued turns. Beyond it, new messages get 429. Default 500. 0 means no limit. |
| `runtime.maxQueueWaitSeconds` | Seconds that a turn may stay queued before it expires. Default 120. 0 means no limit. |
| `runtime.maxStartupSeconds` | Seconds that a turn may take to reach its harness, including creating or waking the sandbox and waiting for a worker with room for it. A turn that takes longer fails. Default 120. 0 means no limit. |
| `runtime.maxTurnSeconds` | Seconds that a turn may run after it leaves the queue, including the time that it waits for approval. A turn that runs longer fails, and what runs in its sandbox is stopped. Default 1800. |
| `runtime.maxToolOutputBytes` | Maximum bytes of the input or output of a tool call that turn events report. Longer text is truncated in the events. Default 65536. 0 means no limit. |
| `runtime.maxAttachmentBytes`, `.maxAttachments` | Maximum bytes of a file that a message carries, and maximum number of files of one message. Default 67108864, which is 64 MiB, and 10. With 0 files, messages carry none and uploads are refused. A proxy in front of the API must let a request body of that size through. |
| `runtime.harnesses` | Harnesses that agents can use, by name, each with its image and the `resources` of its sandboxes. Substrate takes an image only by its digest, as `registry.example.com/aviary-harness-opencode@sha256:…`. The limits of CPU and memory, both of which a harness needs, are the size of a sandbox of the harness on its worker. None by default. A change applies to new sessions. |
