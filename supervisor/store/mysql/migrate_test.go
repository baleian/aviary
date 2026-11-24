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
		t.Fatalf("parse database server URL: %v", err)
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
		t.Errorf("checkSchema on a database without the schema = %v, want a hint to run aviary-supervisor migrate", err)
	}

	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	newest := schema(t, database)
	if len(newest) == 0 {
		t.Fatal("schema is empty after Migrate")
	}
	if err := checkSchema(ctx, db); err != nil {
		t.Errorf("checkSchema after Migrate: %v", err)
	}
	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatalf("Migrate on an up-to-date database: %v", err)
	}

	// Each migration can be undone on its own.
	versions := rows(t, database, `select version_id from goose_db_version where version_id > 0 order by 1 desc`)
	for i := range versions {
		if err := (Plugin{}).MigrateTo(ctx, database, int64(len(versions)-i-1)); err != nil {
			t.Fatal(err)
		}
	}
	if left := schema(t, database); len(left) != 0 {
		t.Errorf("schema left after undoing all migrations:\n%s", strings.Join(left, "\n"))
	}
	if err := (Plugin{}).Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if again := schema(t, database); !slices.Equal(again, newest) {
		t.Errorf("schema after migrating down and up again:\n%s\nwant:\n%s",
			strings.Join(again, "\n"), strings.Join(newest, "\n"))
	}

	for _, version := range []int64{-1, int64(len(versions)) + 1} {
		if err := (Plugin{}).MigrateTo(ctx, database, version); err == nil {
			t.Errorf("MigrateTo(%d) succeeded, want an error", version)
		}
	}
}

// The sandboxes that slept before the time of it was recorded sleep since the
// migration, so that their homes are archived as well. The others stay as
// they are.
func TestMigrateDatesTheSandboxesThatSleep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := emptyDatabase(t)
	if err := (Plugin{}).MigrateTo(ctx, database, 3); err != nil {
		t.Fatal(err)
	}
	const project, provider, agent = "unhex('00000000000000000000000000000001')", "unhex('00000000000000000000000000000002')",
		"unhex('00000000000000000000000000000003')"
	exec(t, database, `insert into projects (id, slug) values (`+project+`, 'project')`)
	exec(t, database, `insert into providers (id, project_id, name, url) values (`+provider+`, `+project+`, 'provider', 'http://models.example')`)
	exec(t, database, `insert into agents (id, project_id, slug, harness, provider_id, model)
		values (`+agent+`, `+project+`, 'agent', 'opencode', `+provider+`, 'model')`)
	exec(t, database, `insert into sessions (id, agent_id, sandbox, sandbox_state, asleep_since) values
		(unhex(replace(uuid(), '-', '')), `+agent+`, 'asleep', 'asleep', null),
		(unhex(replace(uuid(), '-', '')), `+agent+`, 'dated', 'asleep', now(6) - interval 1 day),
		(unhex(replace(uuid(), '-', '')), `+agent+`, 'awake', 'awake', null),
		(unhex(replace(uuid(), '-', '')), `+agent+`, null, null, null)`)

	if err := (Plugin{}).MigrateTo(ctx, database, 4); err != nil {
		t.Fatal(err)
	}
	dated := rows(t, database, `select concat(coalesce(sandbox, 'none'), ': ', case
			when asleep_since is null then 'no time'
			when asleep_since > now(6) - interval 1 hour then 'the migration'
			else 'before' end) as dated
		from sessions order by 1`)
	if want := []string{"asleep: the migration", "awake: no time", "dated: before", "none: no time"}; !slices.Equal(dated, want) {
		t.Errorf("since when the sandboxes sleep after the migration = %q, want %q", dated, want)
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
		t.Errorf("checkSchema on a newer schema: %v", err)
	}

	// One that dropped what this build uses is not.
	exec(t, database, `update schema_floor set version = (select max(version_id) from goose_db_version)`)
	if err := checkSchema(ctx, db); err == nil {
		t.Error("checkSchema accepted a schema whose floor excludes this build")
	}
}

func TestConnect(t *testing.T) {
	t.Parallel()
	// An unusable URL fails with an error that does not reveal the password.
	for address, why := range map[string]string{
		"mysql://user:secret@host:port/name":             "parse database URL",
		"mysql://user:secret@host:3306/name?timeout=5s":  `parameter "timeout"`,
		"mysql://user:secret@host:3306/name?tls=perhaps": "tls",
	} {
		db, err := connect(address)
		if err == nil {
			db.Close()
		}
		if err == nil || !strings.Contains(err.Error(), why) || strings.Contains(err.Error(), "secret") {
			t.Errorf("connect(%s) error = %v, want the reason without the password", address, err)
		}
	}
	// A valid URL is accepted without connecting.
	for _, address := range []string{"mysql://user:secret@host/name", "mysql://user@host:3307/name?tls=true", "mysql://host/name"} {
		db, err := connect(address)
		if err != nil {
			t.Errorf("connect(%s): %v", address, err)
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
		t.Errorf("session time zone = %s, isolation = %s, want +00:00 and READ-COMMITTED: %v", zone, isolation, err)
	}
	// Times are read as UTC, whatever the local time zone.
	var at time.Time
	if err := db.QueryRow(`select cast('2026-01-02 03:04:05.678901' as datetime(6))`).Scan(&at); err != nil ||
		at.Location() != time.UTC || !at.Equal(time.Date(2026, 1, 2, 3, 4, 5, 678901000, time.UTC)) {
		t.Errorf("datetime read as %v, want 2026-01-02 03:04:05.678901 UTC: %v", at, err)
	}
	// A value too long for its column is rejected, although the test server
	// is configured to truncate it.
	if _, err := db.Exec(`create table short (name varchar(4))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into short (name) values ('longer')`); !failed(err, 1406) {
		t.Errorf("insert of a too-long value = %v, want error 1406", err)
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
		t.Errorf("RowsAffected of an update that changes nothing = %d, want 1: %v", count, err)
	}
}
