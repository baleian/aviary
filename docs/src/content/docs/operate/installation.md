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
- **What [Agent Substrate](#agent-substrate) needs**, which the chart
  installs to run the sandboxes: a PostgreSQL database, and nodes and a
  Kubernetes that it runs on.
- **Storage for a volume** of the object storage, which keeps the
  snapshots of sleeping sandboxes and the files that messages carry, unless
  they are kept in an S3 bucket: see
  [Sessions and storage](./storage.md#the-object-storage).

## Agent Substrate

[Agent Substrate](https://github.com/agent-substrate/substrate) runs the
sandbox of each session as an actor, on the workers of a pool that the chart
makes. The chart installs its control plane in the namespace `ate-system`,
from the manifests of its release v1.0.1, with the values under `substrate`
(see [Configuration](../reference/configuration.md#agent-substrate)). It
needs:

- **A PostgreSQL database**, in which its API server keeps its records, in a
  schema of its own, as an owner user and a read-write user. The script
  `pkg/postgressetup/setup.sql` of the release makes the users, their roles
  and the schema, with `psql` variables for their names and passwords. The
  chart takes their connection strings from a Secret in `ate-system`, under
  the keys `owner` and `readWrite`.
- **An object storage** for the snapshots of sleeping sandboxes, which
  keeps the files that messages carry as well: see
  [Sessions and storage](./storage.md#the-object-storage). By default the
  chart keeps them in a volume, through an S3 gateway of its own. With
  `substrate.objectStorage.location` they are kept in an S3 bucket, which
  the API server and the agent of Substrate on the nodes, atelet, and the
  Supervisors reach with an access key, or with the identity of their
  service accounts, which `substrate.serviceAccountAnnotations` and
  `supervisor.serviceAccountAnnotations` can give a role.
- **The issuer of the service account tokens** of the cluster,
  `substrate.kubernetesIssuer`, against which its API checks the tokens of
  the Supervisors. On EKS, it is the OpenID Connect issuer of the cluster.
- **Nodes and a Kubernetes that it runs on**: nodes on which atelet, which
  runs privileged, runs the sandboxes in gVisor, and a Kubernetes with pod
  certificates and cluster trust bundles. Its own documentation says more.
  atelet downloads the releases of gVisor from the public bucket
  `gs://gvisor`, or from the object storage: see
  [Mirrors](./mirrors.md#the-releases-of-gvisor).
- **The image `substrate.keys.image`** of the Job that makes the signing
  keys and certificate authorities of Substrate, each the first time:
  `kubectl-ate` of the release. `make build` builds it as
  `aviary-substrate-keys:dev`.

The chart renders the same for the same values, without reading the
cluster, so that it can be applied by a GitOps tool as well as by Helm. Its
resources need the CRDs of Substrate, which are in the chart, before they
can be checked: a tool that checks every resource before it applies them
needs to skip those of the CRDs that it applies at the same time.

The namespace of the release needs the label `ate.dev/api-access=true`, with
which Substrate lets its pods reach its API:

```sh
kubectl label namespace aviary-system ate.dev/api-access=true
```

With `substrate.install: false` the chart leaves Substrate out, for a
cluster that has it installed for Aviary: with the Supervisors as its
credential provider `aviary.io` at
`aviary-supervisor.<namespace>.svc:50051`, its router letting in
`spiffe://cluster.local/ns/<namespace>/sa/aviary-supervisor`, and the
service account `aviary-supervisor` as an owner of every atespace.

## Images

A cluster of your own pulls the images that the values of the chart name:
`supervisor.image`, `workers.image`, `substrate.keys.image`, the images of
the release of Substrate at `substrate.images.registry`, without a location
of the snapshots `substrate.store.image`, the `image` of each harness in
`runtime.harnesses` and `runtime.runner.image`. Substrate pulls the images
of a sandbox itself, by their digests: from a registry that it reaches
without credentials, or with those of the kubelet's credential provider,
which `substrate.atelet.imageCredentialProvider` names.

`make build` builds the images of this repository: the Supervisor as
`aviary-supervisor:dev`, the runner as `aviary-runner:dev`, each harness as
`aviary-harness-<name>:dev`, and the image of the key Job of Substrate as
`aviary-substrate-keys:dev`. Push them to a registry that your cluster pulls
from, and name them in your values, each harness and the runner by its
digest. In a network that does not reach the public registries, see
[Mirrors](./mirrors.md).

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

Substrate reads its own in `ate-system`: the connection strings of its
database, in `substrate.database.secret`, and the access key of a bucket of
`substrate.objectStorage.location`, if it uses one, in
`substrate.objectStorage.secret`, which the Supervisors read in the release
namespace. Without these names the chart makes the Secrets of the strings
and the key in its values.

## Installing

```sh
make chart
helm upgrade --install aviary .out/aviary-0.1.0.tgz \
  --namespace aviary-system --values my-values.yaml --wait --timeout 10m
```

`make chart` packages the chart with Substrate's, which it makes of the
source of the release. Before every install and upgrade, the chart migrates
the database: see [Upgrades and rollbacks](./upgrades.md). The workers of
the sandboxes run in the namespace `aviary-runtime`, which the chart
creates, as pods of Substrate.

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
