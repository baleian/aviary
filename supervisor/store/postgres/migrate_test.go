package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aviary/supervisor/store/postgres"
)

// rows returns the rows of the query, each formatted as text.
func rows(t *testing.T, database, query string) []string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	found, err := conn.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for found.Next() {
		values, err := found.Values()
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, fmt.Sprint(values...))
	}
	if err := found.Err(); err != nil {
		t.Fatal(err)
	}
	return texts
}

// schema describes the columns, constraints and indexes of the database.
func schema(t *testing.T, database string) []string {
	t.Helper()
	return slices.Concat(
		rows(t, database, `select table_name, column_name, data_type, is_nullable, coalesce(column_default, '-')
			from information_schema.columns
			where table_schema = 'public' and table_name not like 'goose%' order by 1, 2`),
		rows(t, database, `select conrelid::regclass::text, conname, pg_get_constraintdef(oid) from pg_constraint
			where connamespace = 'public'::regnamespace and conrelid::regclass::text not like 'goose%' order by 1, 2`),
		rows(t, database, `select indexdef from pg_indexes
			where schemaname = 'public' and tablename not like 'goose%' order by 1`),
	)
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := emptyDatabase(t)
	if st, err := (postgres.Plugin{}).Open(ctx, database); err == nil {
		st.Close()
		t.Fatal("Open succeeded on a database without the schema")
	} else if !strings.Contains(err.Error(), "aviary-supervisor migrate") {
		t.Errorf("Open error = %v, want a hint to run aviary-supervisor migrate", err)
	}

	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	newest := schema(t, database)
	if len(newest) == 0 {
		t.Fatal("schema is empty after Migrate")
	}
	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatalf("Migrate on an up-to-date database: %v", err)
	}

	// Each migration can be undone on its own.
	versions := rows(t, database, `select version_id from goose_db_version where version_id > 0 order by 1 desc`)
	for i := range versions {
		if err := (postgres.Plugin{}).MigrateTo(ctx, database, int64(len(versions)-i-1)); err != nil {
			t.Fatal(err)
		}
	}
	if left := schema(t, database); len(left) != 0 {
		t.Errorf("schema left after undoing all migrations:\n%s", strings.Join(left, "\n"))
	}
	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if again := schema(t, database); !slices.Equal(again, newest) {
		t.Errorf("schema after migrating down and up again:\n%s\nwant:\n%s",
			strings.Join(again, "\n"), strings.Join(newest, "\n"))
	}

	for _, version := range []int64{-1, int64(len(versions)) + 1} {
		if err := (postgres.Plugin{}).MigrateTo(ctx, database, version); err == nil {
			t.Errorf("MigrateTo(%d) succeeded, want an error", version)
		}
	}
}

func TestOpenChecksTheSchema(t *testing.T) {
	t.Parallel()
	database := newDatabase(t)

	// A schema that a newer release added to is accepted.
	exec(t, database, `insert into goose_db_version (version_id, is_applied)
		select max(version_id) + 1, true from goose_db_version`)
	open(t, database)

	// One that dropped what this build uses is not.
	exec(t, database, `update schema_floor set version = (select max(version_id) from goose_db_version)`)
	if st, err := (postgres.Plugin{}).Open(context.Background(), database); err == nil {
		st.Close()
		t.Error("Open succeeded on a schema whose floor excludes this build")
	}
}
