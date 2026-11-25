---
title: Database migrations
description: The rules for a change of the schema of the Supervisor's database.
---

The schema of the Supervisor's database changes in migrations, one step
each, in the order of their numbers. Each database plugin has its own, in
`supervisor/store/postgres/migrations` and
`supervisor/store/mysql/migrations`, so a change of the schema is a
migration in both. They are written for
[goose](https://github.com/pressly/goose) and embedded in the Supervisor.

[Upgrades and rollbacks](../operate/upgrades.md) describes how a release
applies them, and how Supervisors of two releases run on one database.

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
   table makes every query on that table wait behind it. In PostgreSQL, a
   migration that changes a table in use begins with
   `set local lock_timeout = '5s';`, so that it fails and can be run again,
   and an index on a table in use is built with `create index concurrently`,
   in a migration marked `-- +goose NO TRANSACTION`.

After adding a migration, run `make sqlc`: the Go code for the queries is
generated from the schema that the migrations define.
