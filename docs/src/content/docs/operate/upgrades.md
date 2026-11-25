---
title: Upgrades and rollbacks
description: How a release migrates the database, and how to go back to an earlier release.
---

A release of Aviary is installed over another with `helm upgrade`. The
schema of the database changes in migrations, which the release applies
before its Supervisors start.

## How the migrations run

`aviary-supervisor migrate` applies the migrations that the database does not
have yet. The Helm chart runs it as a Job before every install and upgrade.
If it fails, the release stops there and the Supervisors of the previous
release keep running.

Supervisors do not change the schema. At startup, a Supervisor checks that:

- the schema is at least as new as the Supervisor needs, and
- the Supervisor is at least as new as the schema allows. The `schema_floor`
  table holds the oldest schema version that a Supervisor may be built for.

If either check fails, the Supervisor does not start. This lets Supervisors of
two releases run on one database: during a rollout, after a rollback, and in a
blue-green deployment.

## Rolling back

**To the previous release:** roll back the release, for example with
`helm rollback`. The schema stays as it is, and the older Supervisor runs on
it. Nothing is lost.

**Further back:** the same, as long as the floor allows it. If it does not,
the older Supervisor reports so at startup and does not start, and the running
Supervisors stay.

**Undoing a schema change:** `aviary-supervisor migrate <version>` migrates
the schema to that version, forward or back. Migrating back runs the Down
parts, which drop what the migrations added, with its data. It needs the
image of the newer release, which contains the Down parts. For example, to go
back to version 7:

```sh
url=$(kubectl --namespace aviary-system get secret aviary-database \
  --output jsonpath='{.data.uri}' | base64 --decode)
kubectl --namespace aviary-system run aviary-migrate --rm --attach --restart=Never \
  --image=<image of the newer release> --env=AVIARY_DATABASE_URL="$url" -- migrate 7
```

First stop the Supervisors that need a newer version than the target.
Otherwise they keep running on the older schema until a query fails.

[Database migrations](../contribute/database-migrations.md) has the rules
that keep a migration safe for the release before it.
