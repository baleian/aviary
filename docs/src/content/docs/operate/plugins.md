---
title: Plugins
description: The products that Aviary connects to through plugins, and how to connect another.
---

Aviary's core does not depend on a particular database, event log, secret
store or identity provider. The Supervisor uses each of them through an
interface, and a plugin implements the interface for one kind of product.
An installation chooses among the plugins that its Supervisor was built
with.

| Component | Interface | Selected by | Plugins in this repository |
| --- | --- | --- | --- |
| Database for the Supervisor's records | `store.Plugin` in `supervisor/store` | The scheme of the URL in the secret that `supervisor.database` names | `store/postgres`: PostgreSQL, at `postgres://` or `postgresql://`. `store/mysql`: MySQL, at `mysql://` |
| Event log for the events of turns | `events.Plugin` in `supervisor/events` | The scheme of the URL in the secret that `supervisor.events` names | `events/resp`: a RESP server with stream and publish commands, at `valkey://`, `valkeys://`, `redis://` or `rediss://` |
| Secret stores of projects | `secret.Plugin` in `supervisor/secret` | The `kind` that a project gives when it registers a store | `secret/kv2`: KV version 2 with AppRole login, as the kind `kv-v2` |
| Identity provider that authenticates API requests | `identity.Plugin` in `supervisor/identity` | The `kind` of `supervisor.auth.provider` | `identity/oidc`: an OpenID Connect issuer, as the kind `oidc` |

A Supervisor that is given a URL whose scheme no plugin supports does not
start, and lists the schemes that it supports.

## Tested products

Aviary supports these products in these versions. The tests of a plugin run
against the product in a container, and the e2e runs Aviary on it in a
cluster.

| Product | Version | Plugin | Tested by |
| --- | --- | --- | --- |
| PostgreSQL | 18 | `store/postgres` | Plugin tests and the e2e |
| MySQL | 8.4 | `store/mysql` | Plugin tests, and the e2e in a cluster with MySQL, `make cluster-up mysql valkey e2e` |
| Valkey | 9.1 | `events/resp` | Plugin tests and the e2e |
| Redis | 7.2 | `events/resp` | Plugin tests, and the e2e in a cluster with Redis, `make cluster-up postgres redis e2e` |
| OpenBao | 2.7 | `secret/kv2` | Plugin tests and the e2e |
| Keycloak | 26.8 | `identity/oidc` | The e2e in a cluster with Keycloak, `make cluster-up postgres valkey keycloak e2e`. The plugin tests use an issuer of their own. |

Other versions, and other products that speak the same protocol, may work
but are not tested. Redis 7.2 is the last Redis release under the BSD
license, and later releases are not tested. MariaDB, a separate product
from MySQL, is not tested either. For an OpenID Connect issuer other than
Keycloak, the `claims` in the plugin's settings say where its tokens name
the caller and the groups.

## Writing a plugin

A plugin is a Go package that implements one of the interfaces. To connect
Aviary to a product that it has no plugin for, write one.

1. **Implement the interface.** Its doc comments state what each operation
   guarantees, including when several Supervisors call it at once. The
   interface works at the level of operations, such as "dispatch the oldest
   queued turns within the limit", not of SQL statements or commands, so a
   plugin implements each one in the way that suits its product. A database
   plugin also manages its schema, with `Migrate` and `MigrateTo`. The
   in-memory implementations, `store/memory` and `events/memory`, are the
   shortest to read.
2. **Pass the test suite.** `store/storetest` and `events/eventstest` test
   the behavior that every store and every event log must have. A plugin's
   own tests run the suite against the product:

   ```go
   func TestStore(t *testing.T) {
       storetest.Run(t, func(t *testing.T) storetest.Database { return newDatabase(t) })
   }
   ```

   `storetest.Database` is what the suite needs from a test database: a way
   to open one more store on it, and a way to make turns older. The tests of
   `store/postgres`, `store/mysql` and `events/resp` show both suites in
   use. The two databases implement the same operations differently: in
   places, PostgreSQL does in one statement what MySQL does by locking a row
   and then deciding. There is no suite for secret stores or identity
   providers: the tests of `secret/kv2` and `identity/oidc` show what to
   test.
3. **Keep it where you like.** A plugin can be a package in this repository
   or in a module of your own that depends on `aviary/supervisor`. That
   module path cannot be downloaded, so your module points to a checkout
   with a `replace` directive in its `go.mod`, as `test/plugin/go.mod` does.

## Installing a plugin

Go links plugins into the binary, so a plugin is installed by building a
Supervisor that includes it. There are two ways:

- **In a checkout of this repository.** Add the plugin to
  `supervisor/plugins.go`, the one place that lists the plugins of the
  build, and build the image with `make build`.
- **In a module of your own.** Write a main package that calls `app.Main`
  with the plugins that you want, including those of this repository, and
  build an image of it as `images/supervisor/Dockerfile` does. `test/plugin`
  is such a build, and `make test-plugin` keeps it working.

## Configuring a plugin

- **Database or event log.** Put its URL, with the scheme of the plugin,
  into the secret that `supervisor.database` or `supervisor.events` names.
  Nothing else needs to be set.
- **Secret store.** A project registers a store with the kind of the plugin
  and the settings that the plugin expects. The installation allows the
  store's host in `supervisor.secretStores.allowedHosts`.
- **Identity provider.** Set the kind of the plugin and its settings in
  `supervisor.auth.provider`.

## Testing a plugin in a cluster

The e2e runs against whatever the Supervisor in its cluster is connected to,
so it tests a build with your plugin as it tests any other.

1. Create a cluster with the images of your checkout, your plugin included.
   Aviary runs in it on the PostgreSQL and the Valkey of the local stack.

   ```bash
   make e2e-up
   ```

2. Install your product in the cluster, put its URL into a secret in the
   namespace `aviary-system` under the key `uri`, and point the Supervisor
   at the secret, keeping the values that `make e2e-up` gave the chart. The
   kubeconfig of the cluster is `.out/e2e/aviary-e2e/host.kubeconfig`.

   ```bash
   KUBECONFIG=.out/e2e/aviary-e2e/host.kubeconfig helm upgrade aviary deploy/charts/aviary --namespace aviary-system --reuse-values --set supervisor.database.secretName=<your secret> --wait
   ```

   The upgrade migrates the database with your plugin and starts the
   Supervisors on it. For an event log, set `supervisor.events.secretName`
   instead.

3. Run the tests:

   ```bash
   make e2e-run
   ```

`make e2e-down` deletes the cluster.
