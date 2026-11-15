# Database migrations

The files in this directory change the schema of the Supervisor's database,
one step each, in the order of their numbers. They are written for
[goose](https://github.com/pressly/goose) and are built into the Supervisor.

## How they run

`aviary-supervisor migrate` applies the migrations that the database does not
have yet. The Helm chart runs it as a Job before every install and upgrade.
If it fails, the release stops there and the Supervisors of the release
before keep running.

A Supervisor does not change the schema. When it starts, it checks two
things and does not start if one of them fails:

- The schema is at least as new as the Supervisor needs.
- The Supervisor is at least as new as the schema allows. The table
  `schema_floor` holds the oldest schema version that a Supervisor may have
  been built for.

So Supervisors of two releases can run on one database. That is what happens
while a release is rolled out, after it is rolled back, and in a blue-green
deployment.

## Rules for a migration

1. **Do not change the Up part of a migration that has been released.** Write
   a new migration.
2. **Only add.** The Supervisor of the release before must work on the new
   schema, because it runs on it during the rollout and after a rollback.
   New tables, new indexes and new columns that are nullable or have a
   default are fine.
3. **Take things away in a later release, and raise the floor.** Dropping or
   renaming a column or a table, or tightening a constraint, breaks the
   Supervisors that still use the old form. Do it in two releases:
   - First release: add the new form. The Supervisor stops using the old one.
   - A later release: a migration removes the old form and, in the same
     migration, sets `schema_floor` to the schema version of the first
     release: `update schema_floor set version = <that version>;`

   From then on, a Supervisor from before the first release does not start
   on this database, which is what makes the removal safe.
4. **Write the Down part.** It undoes the Up part. The Down part of a
   migration that raised the floor puts the old value back.
5. **Keep it quick.** A release waits for its migrations. Moving the data of
   a large table belongs in a job of its own, in batches, not in a migration.
6. **Do not queue behind locks.** A schema change that waits for a lock on a
   table makes every query on that table wait behind it. A migration that
   changes a table in use begins with `set local lock_timeout = '5s';`, so
   that it fails and can be run again. An index on a table in use is built
   with `create index concurrently`, in a migration marked
   `-- +goose NO TRANSACTION`.

After adding a migration, run `make sqlc`. The Go code for the Supervisor's
queries is written from the schema that the migrations make.

## Going back

**To the release before:** roll back the release, for example with
`helm rollback`. The schema stays as it is, and the older Supervisor runs on
it. Nothing is lost.

**Further back:** the same, as long as the floor allows it. If it does not,
the older Supervisor reports that at startup and does not start, and the
Supervisors that are running stay.

**Undoing a schema change itself:** `aviary-supervisor migrate <version>`
moves the schema to that version, forward or back. Going back runs the Down
parts, which drop what the migrations added together with the data in it. It
needs the image of the newer release, because that is where the Down parts
are. For example, to go back to version 7:

```sh
url=$(kubectl --namespace aviary-system get secret aviary-database \
  --output jsonpath='{.data.uri}' | base64 --decode)
kubectl --namespace aviary-system run aviary-migrate --rm --attach --restart=Never \
  --image=<image of the newer release> --env=AVIARY_DATABASE_URL="$url" -- migrate 7
```

Supervisors that need a newer version than that must be gone first: they
keep running on the older schema until a query fails.
