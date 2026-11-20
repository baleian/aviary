# Database migrations

The files in this directory change the schema of the Supervisor's database,
one step each, in the order of their numbers. They are written for
[goose](https://github.com/pressly/goose) and embedded in the Supervisor.

## How they run

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

## Rules for a migration

1. **Do not change the Up part of a released migration.** Write a new
   migration.
2. **Only add.** The Supervisor of the previous release must work on the new
   schema, because it runs on it during the rollout and after a rollback. New
   tables, new indexes, and new columns that are nullable or have a default
   are fine.
3. **Remove things in a later release, and raise the floor.** Dropping or
   renaming a column or a table, or tightening a constraint, breaks the
   Supervisors that still use the old form. Do it in two releases:
   - First release: add the new form, and stop using the old one.
   - A later release: a migration removes the old form and, in the same
     migration, sets `schema_floor` to the schema version of the first
     release: `update schema_floor set version = <that version>;`

   Supervisors from before the first release then refuse to start on this
   database, which is what makes the removal safe.
4. **Write the Down part.** It undoes the Up part. The Down part of a migration
   that raised the floor restores the previous value.
5. **Keep it quick.** A release waits for its migrations. Move the data of a
   large table in a separate job, in batches, not in a migration.
6. **Do not queue behind locks.** A schema change that waits for a lock on a
   table makes every query on that table wait behind it. A migration that
   changes a table in use begins with `set local lock_timeout = '5s';`, so that
   it fails and can be run again. Build an index on a table in use with
   `create index concurrently`, in a migration marked
   `-- +goose NO TRANSACTION`.

After adding a migration, run `make sqlc`: the Go code for the queries is
generated from the schema that the migrations define.

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
