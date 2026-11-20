package mysql

import (
	"context"
	"crypto/rand"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/testenv"
)

// exec runs a statement on the database, to arrange what a Store cannot, such
// as the passing of time or the schema of another release.
func exec(t *testing.T, database, statement string, arguments ...any) {
	t.Helper()
	db, err := connect(database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// emptyDatabase creates a database for the test and returns its URL. The
// database is dropped when the test ends.
func emptyDatabase(t *testing.T) string {
	t.Helper()
	server := testenv.DatabaseURL(t, "mysql")
	address, err := url.Parse(server)
	if err != nil {
		t.Fatalf("the URL of the database server of the tests: %v", err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	exec(t, server, "create database "+name)
	t.Cleanup(func() { exec(t, server, "drop database "+name) })
	address.Path = "/" + name
	return address.String()
}

// newDatabase is like emptyDatabase, with the schema of this build.
func newDatabase(t *testing.T) string {
	t.Helper()
	database := emptyDatabase(t)
	if err := (Plugin{}).Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// rows returns the rows of the query, each formatted as text.
func rows(t *testing.T, database, query string) []string {
	t.Helper()
	db, err := connect(database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	found, err := db.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer found.Close()
	columns, err := found.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for found.Next() {
		values := make([]any, len(columns))
		for i := range values {
			values[i] = new(*string)
		}
		if err := found.Scan(values...); err != nil {
			t.Fatal(err)
		}
		var text []string
		for _, value := range values {
			if written := *value.(**string); written != nil {
				text = append(text, *written)
			} else {
				text = append(text, "-")
			}
		}
		texts = append(texts, strings.Join(text, " "))
	}
	if err := found.Err(); err != nil {
		t.Fatal(err)
	}
	return texts
}

// schema describes the columns, indexes and constraints of the database.
func schema(t *testing.T, database string) []string {
	t.Helper()
	return slices.Concat(
		rows(t, database, `select table_name, column_name, column_type, is_nullable, column_default, extra
			from information_schema.columns
			where table_schema = database() and table_name not like 'goose%' order by 1, 2`),
		rows(t, database, `select table_name, index_name, non_unique, seq_in_index, coalesce(column_name, expression)
			from information_schema.statistics
			where table_schema = database() and table_name not like 'goose%' order by 1, 2, 4`),
		rows(t, database, `select table_name, constraint_name, constraint_type from information_schema.table_constraints
			where table_schema = database() and table_name not like 'goose%' order by 1, 2`),
		rows(t, database, `select table_name, constraint_name, referenced_table_name from information_schema.referential_constraints
			where constraint_schema = database() order by 1, 2`),
	)
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := emptyDatabase(t)
	db, err := connect(database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := checkSchema(ctx, db); err == nil || !strings.Contains(err.Error(), "aviary-supervisor migrate") {
		t.Errorf("of a database without the schema, a Supervisor is told: %v", err)
	}

	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	newest := schema(t, database)
	if len(newest) == 0 {
		t.Fatal("the migrations made nothing")
	}
	if err := checkSchema(ctx, db); err != nil {
		t.Errorf("of a database with the schema, a Supervisor is told: %v", err)
	}
	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatalf("migrate a database that is up to date: %v", err)
	}

	// Each migration can be undone on its own.
	versions := rows(t, database, `select version_id from goose_db_version where version_id > 0 order by 1 desc`)
	for i := range versions {
		if err := (Plugin{}).MigrateTo(ctx, database, int64(len(versions)-i-1)); err != nil {
			t.Fatal(err)
		}
	}
	if left := schema(t, database); len(left) != 0 {
		t.Errorf("the migrations, undone, leave behind:\n%s", strings.Join(left, "\n"))
	}
	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if again := schema(t, database); !slices.Equal(again, newest) {
		t.Errorf("the schema is not the same after the migrations were undone and done again:\n%s\nwas:\n%s",
			strings.Join(again, "\n"), strings.Join(newest, "\n"))
	}

	for _, version := range []int64{-1, int64(len(versions)) + 1} {
		if err := (Plugin{}).MigrateTo(ctx, database, version); err == nil {
			t.Errorf("the schema was moved to version %d, which there is not", version)
		}
	}
}

func TestCheckSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newDatabase(t)
	db, err := connect(database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A schema that a newer release added to is accepted.
	exec(t, database, `insert into goose_db_version (version_id, is_applied)
		select max(version_id) + 1, true from goose_db_version`)
	if err := checkSchema(ctx, db); err != nil {
		t.Errorf("of a schema that is newer than it needs, a Supervisor is told: %v", err)
	}

	// One that dropped what this build uses is not.
	exec(t, database, `update schema_floor set version = (select max(version_id) from goose_db_version)`)
	if err := checkSchema(ctx, db); err == nil {
		t.Error("a database that no longer supports this build is taken")
	}
}

func TestConnect(t *testing.T) {
	t.Parallel()
	// An unusable URL fails with an error that does not reveal the password.
	for address, why := range map[string]string{
		"mysql://user:secret@host:port/name":             "parse the URL",
		"mysql://user:secret@host:3306/name?timeout=5s":  `the parameter "timeout"`,
		"mysql://user:secret@host:3306/name?tls=perhaps": "tls",
	} {
		db, err := connect(address)
		if err == nil {
			db.Close()
		}
		if err == nil || !strings.Contains(err.Error(), why) || strings.Contains(err.Error(), "secret") {
			t.Errorf("the database at %s was connected to with: %v", address, err)
		}
	}
	// A valid URL is accepted without connecting.
	for _, address := range []string{"mysql://user:secret@host/name", "mysql://user@host:3307/name?tls=true", "mysql://host/name"} {
		db, err := connect(address)
		if err != nil {
			t.Errorf("the database at %s: %v", address, err)
			continue
		}
		db.Close()
	}
	// Connections to the test server have the session settings of connect.
	db, err := connect(emptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var zone, isolation string
	if err := db.QueryRow(`select @@session.time_zone, @@session.transaction_isolation`).Scan(&zone, &isolation); err != nil ||
		zone != "+00:00" || isolation != "READ-COMMITTED" {
		t.Errorf("a connection has the time zone %s and reads as %s: %v", zone, isolation, err)
	}
	// Times are read as UTC, whatever the local time zone.
	var at time.Time
	if err := db.QueryRow(`select cast('2026-01-02 03:04:05.678901' as datetime(6))`).Scan(&at); err != nil ||
		at.Location() != time.UTC || !at.Equal(time.Date(2026, 1, 2, 3, 4, 5, 678901000, time.UTC)) {
		t.Errorf("a time of the database is read as %v: %v", at, err)
	}
	// A value too long for its column is rejected, although the test server
	// is configured to truncate it.
	if _, err := db.Exec(`create table short (name varchar(4))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into short (name) values ('longer')`); !failed(err, 1406) {
		t.Errorf("a value that is too long for its column was taken: %v", err)
	}
	// An update counts the rows that it matched, unchanged ones included.
	if _, err := db.Exec(`insert into short (name) values ('same')`); err != nil {
		t.Fatal(err)
	}
	left, err := db.Exec(`update short set name = 'same'`)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := left.RowsAffected(); err != nil || count != 1 {
		t.Errorf("an update that left its row as it was counts %d rows: %v", count, err)
	}
}
