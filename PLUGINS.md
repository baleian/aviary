# Plugins

The core of Aviary is the Supervisor, the runner and the Egress Proxy. What
an installation keeps its data in is not part of the core: the Supervisor
knows a database, an event log and a secret store only by an interface, and
a plugin connects it to one kind of each. An installation picks among the
plugins that its Supervisor was built with.

| What | Interface | How an installation picks | Plugins in this repository |
| --- | --- | --- | --- |
| The database that holds the records | `store.Plugin` in `supervisor/store` | The scheme of the URL in the secret that `supervisor.database` names | `store/postgres`: PostgreSQL, at `postgres://` or `postgresql://`. `store/mysql`: MySQL, at `mysql://` |
| The log of the events of the turns | `events.Plugin` in `supervisor/events` | The scheme of the URL in the secret that `supervisor.events` names | `events/resp`: a server with the stream and publish commands of RESP, at `valkey://`, `valkeys://`, `redis://` or `rediss://` |
| The secret stores of the projects | `secret.Plugin` in `supervisor/secret` | The `kind` that a project gives when it registers a store | `secret/kv2`: KV version 2 with the AppRole login, as the kind `kv-v2` |

A Supervisor that is given a URL with a scheme that it has no plugin for
does not start, and says which schemes it has.

## What the plugins are tested with

Aviary is supported on these products, in these versions. Each is what the
tests of its plugin run against, and the e2e runs Aviary on those that the
table says.

| Product | Version | Plugin | Tested by |
| --- | --- | --- | --- |
| PostgreSQL | 18 | `store/postgres` | The tests of the plugin, and the e2e |
| MySQL | 8.4 | `store/mysql` | The tests of the plugin, and the e2e in a cluster made with `DATABASE=mysql` |
| Valkey | 9.1 | `events/resp` | The tests of the plugin, and the e2e |
| Redis | 7.2 | `events/resp` | The tests of the plugin |
| OpenBao | 2.7 | `secret/kv2` | The tests of the plugin, and the e2e |

Another version, or another product that speaks the same protocol, may work
and is not tested. Redis 7.2 is the last Redis under the BSD licence, and
the later ones are not tested. Nor is MariaDB, which is another product
than MySQL.

## Writing a plugin

A plugin is a Go package that implements one of the interfaces. To connect
Aviary to a product that it has no plugin for, write one.

1. **Implement the interface.** Its comments say what every operation
   promises, also when several Supervisors ask at once. The interface is at
   the level of operations, such as "dispatch the oldest queued turns within
   the limit", and not of SQL or commands, so a plugin does each in the way
   that suits its product. A plugin for a database brings its schema too:
   `Migrate` and `MigrateTo`. The implementations in memory, `store/memory`
   and `events/memory`, are the shortest ones to read.
2. **Pass the suite.** `store/storetest` and `events/eventstest` test what
   holds of every store and every log. A plugin's own tests run the suite
   against the product:

   ```go
   func TestStore(t *testing.T) {
       storetest.Run(t, func(t *testing.T) storetest.Database { return newDatabase(t) })
   }
   ```

   `storetest.Database` is what the suite needs of a test's database: a way
   to open one more store on it, and a way to make turns older. The tests of
   `store/postgres`, `store/mysql` and `events/resp` show both suites in
   use. The two databases do the same in different ways: in places
   PostgreSQL does in one statement what MySQL does by locking a row and
   then deciding. There is no
   suite for secret stores yet: the tests of `secret/kv2` show what to test.
3. **Keep it where you like.** A plugin may be a package of this repository
   or of a module of your own that depends on `aviary/supervisor`. That
   module path cannot be downloaded, so the other module names a checkout
   with `replace` in its `go.mod`, as `test/plugin/go.mod` does.

## Installing a plugin

Go links a plugin into the program, so a plugin is installed by building a
Supervisor that lists it. There are two ways.

- **In a checkout of this repository.** Add the plugin to
  `supervisor/plugins.go`, which is the one place that lists the plugins of
  the build, and build the image with `make build`.
- **In a module of your own.** Write a main package that calls `app.Main`
  with the plugins that you want, those of this repository among them, and
  build an image of it as `images/supervisor/Dockerfile` does. `test/plugin`
  is such a build, and `make test-plugin` keeps it working.

## Using a plugin

- **A database or an event log.** Put its URL, with the scheme of the
  plugin, into the secret that the chart value `supervisor.database` or
  `supervisor.events` names. Nothing else is set.
- **A secret store.** A project registers a store with the kind of the
  plugin and the settings that the plugin asks for. The installation allows
  the host of the store in `supervisor.secretStores.allowedHosts`.

## Testing a plugin in a cluster

The e2e runs against whatever the Supervisor of its cluster is connected to,
so it tests a build with your plugin as it tests any other.

```bash
make e2e-up
```

makes a cluster with the images of your checkout, your plugin among them,
and installs Aviary in it on the PostgreSQL and the Valkey of the local
stack. Install your product in that cluster, put its URL into a secret in
the namespace `aviary-system` under the key `uri`, and point the Supervisor
at the secret:

```bash
helm --kube-context kind-aviary-e2e upgrade aviary deploy/charts/aviary --namespace aviary-system --values test/e2e/values.yaml --set supervisor.database.secretName=<your secret> --wait
```

That migrates the database with your plugin and starts the Supervisors on
it. For an event log the value is `supervisor.events.secretName`. Then

```bash
make e2e-run
```

runs the tests. `make e2e-down` removes the cluster.
