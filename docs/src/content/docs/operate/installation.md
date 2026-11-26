---
title: Installation
description: What a cluster needs to run Aviary, and how its Helm chart is installed.
---

Aviary is installed with the Helm chart in `deploy/charts/aviary`. The
[quick start](../start/quick-start.md) installs it in a local cluster
with `make`, and this page describes what the chart needs in a cluster of
your own. Aviary has only run on a local cluster so far: see
[Limitations](../start/overview.md#limitations).

## What the cluster needs

- **A database and an event log**, which the chart does not install.
  [Plugins](./plugins.md#tested-products) lists the products
  and versions that Aviary is tested with.
- **[Agent Substrate](#agent-substrate)**, which runs the sandboxes. The
  chart does not install it.
- **Storage for a volume** for the depot: see
  [Sessions and storage](./storage.md#the-depot).

## Agent Substrate

[Agent Substrate](https://github.com/agent-substrate/substrate) runs the
sandbox of each session as an actor, on the workers of a pool that the chart
makes, and keeps the snapshots of the sleeping ones in its object storage.
Aviary is written for its release v1.0.1, which its installer, `ate-setup`,
installs in the namespace `ate-system`. Its own documentation says what it
needs of a cluster: a recent Kubernetes with pod certificates and cluster
trust bundles, nodes on which its workers can run gVisor, PostgreSQL and
object storage.

Substrate takes three settings for Aviary, which name the namespace of the
release, `aviary-system` here:

```sh
ate-setup deploy ate-system \
  --credential-provider='{"name": "aviary.io", "address": "aviary-supervisor.aviary-system.svc:50051"}' \
  --ingress-allowed-spiffe-ids=spiffe://cluster.local/ns/aviary-system/sa/aviary-supervisor \
  --ateapi-authz-bootstrap-owners=system:serviceaccount:aviary-system:aviary-supervisor
```

- The Supervisors are the credential provider `aviary.io` of its egress
  gateway, which asks them for the secrets of the sandboxes' requests: see
  [Network and egress](./egress.md#secrets).
- Its router lets the Supervisors reach the runners of the sandboxes.
- The Supervisors manage the actors of their atespace, `substrate.atespace`,
  and read the workers, to put to sleep the sandboxes of a worker that goes
  away. An owner of every atespace may do both. In place of that, an
  installation can make the atespace itself, and give the Supervisors an
  access policy that makes them its owner and a viewer of the workers.

The namespace of the release needs the label `ate.dev/api-access=true`, with
which Substrate lets its pods reach its API:

```sh
kubectl label namespace aviary-system ate.dev/api-access=true
```

## Images

A cluster of your own pulls the images that the values of the chart name:
`supervisor.image`, `workers.image`, and the `image` of each harness in
`runtime.harnesses`. Substrate pulls the image of a sandbox itself, by its
digest: from a registry that it reaches without credentials, or with those
of the kubelet's credential provider.

`make build` builds the images of this repository, the Supervisor as
`aviary-supervisor:dev` and each harness as `aviary-harness-<name>:dev`.
Push them to a registry that your cluster pulls from, and name them in your
values, each harness by its digest. In a network that does not reach the
public registries, see [Mirrors](./mirrors.md).

To run a harness that is not in this repository, see
[Harness images](./harness-images.md). To connect Aviary to a
database, an event log or a secret store that it has no plugin for, see
[Plugins](./plugins.md).

## Secrets

The chart reads three Secrets in the namespace of the release, which exist
before it is installed:

| Secret | Default name | Contents |
| --- | --- | --- |
| The database | `aviary-database` | Under the key `uri`, the URL of the database, whose scheme selects the plugin: `postgresql://…` for PostgreSQL, `mysql://…` for MySQL. |
| The event log | `aviary-events` | Under the key `uri`, the URL of the event log: `valkey://…` for Valkey, `valkeys://…` for Valkey with TLS. |
| The encryption keys | `aviary-encryption` | The keys that encrypt secrets, such as model provider API keys, before they are stored. Each entry is 32 random bytes in base64, and `supervisor.encryption.key` names the entry that encrypts new secrets, `k1` by default. A secret that was encrypted with a key cannot be decrypted without it. |

```sh
kubectl create namespace aviary-system
kubectl --namespace aviary-system create secret generic aviary-database \
  --from-literal=uri='postgresql://aviary:...@postgres.example.com:5432/aviary'
kubectl --namespace aviary-system create secret generic aviary-events \
  --from-literal=uri='valkey://default:...@valkey.example.com:6379'
kubectl --namespace aviary-system create secret generic aviary-encryption \
  --from-literal=k1="$(openssl rand -base64 32)"
```

The names and the keys are values of the chart: see
[Configuration](../reference/configuration.md#supervisor).

## Installing

```sh
make chart
helm upgrade --install aviary .out/aviary-0.1.0.tgz \
  --namespace aviary-system --values my-values.yaml --wait
```

Before every install and upgrade, the chart migrates the database: see
[Upgrades and rollbacks](./upgrades.md). The workers of the sandboxes run in
the namespace `aviary-runtime`, which the chart creates, as pods of
Substrate.

The Supervisors serve the API on the Service `aviary-supervisor` of the
release namespace, on port 80. What exposes it outside the cluster must not
buffer its responses, which include event streams, and must take the files
that messages carry: request bodies of up to `runtime.maxAttachmentBytes`.

## Then

- [Configuration](../reference/configuration.md): the values of the chart.
- [Authentication](./authentication.md): who may call the API.
- [Network and egress](./egress.md): what the sandboxes reach.
- [Sessions and storage](./storage.md): when sessions sleep,
  and where their files are kept.
- [Capacity and availability](./capacity.md): how many sandboxes the
  workers hold.
- [Web UI](./web-ui.md): the chart does not install it.
- [Observability](./observability.md): metrics and logs.
