---
title: Development environment
description: How to run Aviary from a checkout, in a local cluster, and see a change in it.
---

## What you need

You need Docker, [kind](https://kind.sigs.k8s.io), kubectl, Helm, jq, make,
curl, openssl and bash. Tested with kind 0.33, Helm 4.3 and Docker 29 on
Linux.

Go runs in a container, so the host needs no Go toolchain. `make lint` and
`make test` also need Rust, which rustup installs in the version that
`rust-toolchain.toml` names, and Node.js for the code in `web/` and `docs/`.
They are tested with Node.js 26.

In a network that does not reach the public registries, see
[Mirrors](../operate/mirrors.md).

## The local cluster

```sh
make cluster-up        # create a kind cluster
make postgres valkey   # install a database and an event log in it
make images            # build the images and load them into the cluster
make deploy            # install Aviary with its Helm chart
```

`make deploy` serves the API at http://localhost:8080. `make mysql` installs
MySQL instead of PostgreSQL, and `make redis` Redis instead of Valkey.
`make deploy` fails unless the cluster has one database and one event log.
`make cluster-down` deletes the cluster, including the session volumes,
which are directories on its nodes. The cluster runs Kubernetes 1.37.

After a change, `make images deploy` builds the images again and installs
them. The pods of the Supervisor's image start again if the image changed,
and a session runs a changed harness image the next time it wakes.
`make deploy VALUES=<file>` installs the chart with a values file of your
own.

The cluster can have more:

| Target | Installs |
| --- | --- |
| `make web` | The [web UI](../use/web-ui.md), built from `web/`, at http://localhost:8082. |
| `make keycloak` | Keycloak as an identity provider: see [Authentication](../operate/authentication.md#try-it-locally). |
| `make openbao`, `make vault` | A secret store: see [Secret stores](../use/secret-stores.md#try-it-locally). |
| `make grafana` | [Prometheus](https://prometheus.io), which scrapes the metrics of Aviary, and [Grafana](https://grafana.com) with the Aviary dashboard, at http://localhost:3000. |
| `make headlamp` | [Headlamp](https://headlamp.dev), a web UI for the resources, logs and events of the cluster, at http://localhost:4466. |

## The web UI

`make web-dev` serves the web UI from its source in `web/` at
http://localhost:5173, and shows a change when its file is saved. It is
answered from example data in the browser, or by an API:
`make web-dev API=http://localhost:8080` uses the API of the local cluster.

If the API authenticates its callers, the environment names the OpenID
Connect issuer and the client that the web UI signs them in with, for
example those of `make keycloak`:

```sh
AVIARY_OIDC_ISSUER=http://keycloak.localhost:8081/realms/aviary \
AVIARY_OIDC_CLIENT_ID=aviary make web-dev API=http://localhost:8080
```

## Generated code

`make sqlc` generates the Go code of the SQL queries. Run it after changing
a query or a migration. `make lint` fails if the generated code is not up to
date.

## Before you send a change

```sh
make lint          # formatters, linters and generated code
make test          # each component on its own, without a cluster
```

[Testing](./testing.md) describes these and the tests that
take longer.
