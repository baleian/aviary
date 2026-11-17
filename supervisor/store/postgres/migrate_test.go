package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store/postgres"
)

// rows returns the rows that the query finds in the database, each as text.
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

// schema describes what the migrations have made of the database: the
// columns, the constraints and the indexes.
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
	database := testenv.EmptyDatabase(t)
	if st, err := (postgres.Plugin{}).Open(ctx, database); err == nil {
		st.Close()
		t.Fatal("a database without the schema was opened")
	} else if !strings.Contains(err.Error(), "aviary-supervisor migrate") {
		t.Errorf("the error does not say what to do about a database without the schema: %v", err)
	}

	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	newest := schema(t, database)
	if len(newest) == 0 {
		t.Fatal("the migrations made nothing")
	}
	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatalf("migrate a database that is up to date: %v", err)
	}

	// Every migration can be undone by itself.
	versions := rows(t, database, `select version_id from goose_db_version where version_id > 0 order by 1 desc`)
	for i := range versions {
		if err := (postgres.Plugin{}).MigrateTo(ctx, database, int64(len(versions)-i-1)); err != nil {
			t.Fatal(err)
		}
	}
	if left := schema(t, database); len(left) != 0 {
		t.Errorf("the migrations, undone, leave behind:\n%s", strings.Join(left, "\n"))
	}
	if err := (postgres.Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if again := schema(t, database); !slices.Equal(again, newest) {
		t.Errorf("the schema is not the same after the migrations were undone and done again:\n%s\nwas:\n%s",
			strings.Join(again, "\n"), strings.Join(newest, "\n"))
	}

	for _, version := range []int64{-1, int64(len(versions)) + 1} {
		if err := (postgres.Plugin{}).MigrateTo(ctx, database, version); err == nil {
			t.Errorf("the schema was moved to version %d, which there is not", version)
		}
	}
}

func TestOpenChecksTheSchema(t *testing.T) {
	t.Parallel()
	database := testenv.Database(t)

	// A newer release has added to the schema. This one runs on it.
	testenv.Exec(t, database, `insert into goose_db_version (version_id, is_applied)
		select max(version_id) + 1, true from goose_db_version`)
	testenv.Open(t, database)

	// A newer release has dropped what this one uses.
	testenv.Exec(t, database, `update schema_floor set version = (select max(version_id) from goose_db_version)`)
	if st, err := (postgres.Plugin{}).Open(context.Background(), database); err == nil {
		st.Close()
		t.Error("a database that no longer supports this build was opened")
	}
}
