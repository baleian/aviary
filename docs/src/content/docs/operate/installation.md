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
- **A network plugin that enforces network policies.** The policies are what
  isolate the sandboxes.
- **Storage for volumes.** Every session has a volume, and the depot has
  one: see [Sessions and storage](./storage.md).
- **agent-sandbox**, which manages the sandboxes. The chart installs
  agent-sandbox v1.0.11 as well, with the chart of agent-sandbox, in the
  namespace `agent-sandbox-system`.

A cluster runs agent-sandbox once. If yours has it already, in that version,
set `agent-sandbox.enabled` to false. Helm installs the custom resource
definitions of agent-sandbox with the release and leaves them alone
afterwards: it does not upgrade them, and does not delete them with the
release.

## Images

A cluster of your own pulls the images that the values of the chart name:
`supervisor.image`, `egressProxy.image`, `agent-sandbox.image.repository`
and the `image` of each harness in `runtime.harnesses`.

`make build` builds the images of this repository, the Supervisor as
`aviary-supervisor:dev` and each harness as `aviary-harness-<name>:dev`.
Push them to a registry that your cluster pulls from, and name them in your
values. In a network that does not reach the public registries, see
[Mirrors](./mirrors.md).

To run a harness that is not in this repository, see
[Harness images](./harness-images.md). To connect Aviary to a
database, an event log or a secret store that it has no plugin for, see
[Plugins](./plugins.md).

## The chart

The chart of agent-sandbox is not in this repository. `make chart` takes it
from the source of that release and packages the Aviary chart with it, as
`.out/aviary-0.1.0.tgz`. `make deploy` and the tests take it the same way.

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
[Upgrades and rollbacks](./upgrades.md). The sandboxes run in
the namespace `aviary-runtime`, which the chart creates and which enforces
the restricted Pod Security Standard.

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
- [Web UI](./web-ui.md): the chart does not install it.
- [Observability](./observability.md): metrics and logs.
