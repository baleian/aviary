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
| `supervisor.replicas`, `egressProxy.replicas` | Number of Supervisor and Egress Proxy replicas. Any replica can serve any request. Default 1. |
| `supervisor.resources`, `egressProxy.resources` | Resource requests and limits of the Supervisor and Egress Proxy pods. |

## Supervisor

| Value | Description |
| --- | --- |
| `supervisor.database.secretName`, `.secretKey` | Secret in the release namespace that holds the database URL. The URL scheme selects the plugin: `postgresql://…` for PostgreSQL, `mysql://…` for MySQL. |
| `supervisor.events.secretName`, `.secretKey` | Secret in the release namespace that holds the event log URL: `valkey://…` for Valkey, `valkeys://…` for Valkey with TLS. |
| `supervisor.encryption.secretName`, `.key` | Secret with the keys that encrypt secrets, such as model provider API keys, before they are stored, and the entry that encrypts new secrets. Each entry is 32 random bytes in base64. To rotate, add an entry and name it in `key`, and keep the old entries. |
| `supervisor.secretStores.allowedHosts` | Hosts at which projects may register secret stores: `*` for any host, a host name for the host on the default HTTPS and HTTP ports, or a host name with a port. Empty, the default, disables secret stores. |
| `supervisor.secretStores.cacheSeconds` | Seconds that a value read from a secret store is cached. Default 60. 0 reads the store for every model request. |
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

## Egress Proxy

| Value | Description |
| --- | --- |
| `egressProxy.concurrency` | Worker threads of each Egress Proxy. Default 2. 0 lets Envoy choose: as many as the CPUs of its node, or of its CPU limit if it has one. |
| `egressProxy.allowedHosts` | Hosts that the sandboxes may reach, including those of model providers: `*` for any host, a host name for the host on the default HTTPS and HTTP ports, or a host name with a port. Subdomains are separate hosts. Default `*`. |
| `egressProxy.insideCIDRs`, `.extraEgress` | Address ranges that a NetworkPolicy blocks the Egress Proxy from connecting to, so that the sandboxes cannot use it to reach into the cluster, and additional NetworkPolicy egress rules for the proxy. A model provider or an allowed host that is in the cluster or at a private address needs such a rule. |
| `egressProxy.localhost` | The host that the Egress Proxy connects to for a model provider whose URL is at `localhost` or a loopback address: a host name or an IP address. Empty by default: such a provider reaches nothing. An installation on one machine can name that machine here. The host needs a rule in `egressProxy.extraEgress` if it is at a private address. |

## agent-sandbox

| Value | Description |
| --- | --- |
| `agent-sandbox.enabled` | Whether the chart installs agent-sandbox. Default true. The other values under `agent-sandbox` are those of its chart: `image.repository` for the image of its controller, `imagePullSecrets`, `resources` and others. |

## Sandboxes

| Value | Description |
| --- | --- |
| `sandboxes.idleSeconds` | Seconds without a running turn after which a session is put to sleep: its pod is deleted and its volume is kept, and its next turn wakes it in a new pod. Default 300. 0 disables sleeping. |
| `sandboxes.archiveAfterSeconds` | Seconds of sleep after which the home of a session is archived in the [depot](../operate/storage.md#the-depot): its sandbox is deleted with its volume, and its next turn restores the home into a new sandbox. Default 604800, a week. 0 disables archiving, and the homes that are archived are still restored. |
| `sandboxes.retentionSeconds` | Seconds after its last turn after which a session ends: its sandbox is deleted with its volume, or the archive of its home, and its conversation stays readable. A session that ends this way has the `end_reason` `expired`. Default 0, which keeps the sandbox until the session is ended. |

## Runtime

| Value | Description |
| --- | --- |
| `runtime.maxConcurrentTurns` | Maximum number of turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | Maximum number of queued turns. Beyond it, new messages get 429. Default 500. 0 means no limit. |
| `runtime.maxQueueWaitSeconds` | Seconds that a turn may stay queued before it expires. Default 120. 0 means no limit. |
| `runtime.maxStartupSeconds` | Seconds that a turn may take to reach its harness, including creating or waking the sandbox. A turn that takes longer fails. Default 120. 0 means no limit. |
| `runtime.maxRestoreSeconds` | Seconds more that a turn may take to reach its harness if it first restores the home of its session from the depot. Default 600. |
| `runtime.maxTurnSeconds` | Seconds that a turn may run after it leaves the queue, including the time that it waits for approval. A turn that runs longer fails, and its sandbox is put to sleep. Default 1800. |
| `runtime.maxToolOutputBytes` | Maximum bytes of the input or output of a tool call that turn events report. Longer text is truncated in the events. Default 65536. 0 means no limit. |
| `runtime.maxAttachmentBytes`, `.maxAttachments` | Maximum bytes of a file that a message carries, and maximum number of files of one message. Default 67108864, which is 64 MiB, and 10. With 0 files, messages carry none and uploads are refused. A proxy in front of the API must let a request body of that size through. |
| `runtime.harnesses` | Harnesses that agents can use, by name, each with its image and the `resources` of its sandbox pods, in the format of Kubernetes container resources. A change applies to a sandbox the next time it wakes. |
| `runtime.volume.size`, `.storageClass` | The session volume, mounted at `/home`: the state of the harness, the files of the agent and what the agent installs with npm or pip. Default 10Gi, in the default storage class of the cluster. The volume is deleted with its sandbox when its session is ended or expires, or when its home is archived. Whether its data is deleted too depends on the storage class: the default class of the local cluster deletes it, a class with `reclaimPolicy: Retain` keeps it, and some file storage drivers keep it unless they are configured not to. |
