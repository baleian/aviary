package postgres_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
	"aviary/supervisor/store/postgres"
	"aviary/supervisor/store/storetest"
)

// emptyDatabase creates a database for the test and returns its URL. The
// database is dropped when the test ends.
func emptyDatabase(t *testing.T) string {
	t.Helper()
	server := testenv.DatabaseURL(t, "postgres")
	address, err := url.Parse(server)
	if err != nil {
		t.Fatalf("the URL of the database server of the tests: %v", err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	exec(t, server, "create database "+name)
	t.Cleanup(func() { exec(t, server, "drop database "+name+" with (force)") })
	address.Path = "/" + name
	return address.String()
}

// newDatabase is like emptyDatabase, with the schema of this build.
func newDatabase(t *testing.T) string {
	t.Helper()
	database := emptyDatabase(t)
	if err := (postgres.Plugin{}).Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// open opens a Store on the database. Each call stands for one more
// Supervisor.
func open(t *testing.T, database string) store.Store {
	t.Helper()
	st, err := postgres.Plugin{}.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

// exec runs a statement on the database, to arrange what a Store cannot, such
// as the passing of time or the schema of another release.
func exec(t *testing.T, database, statement string, arguments ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, database)
	if err != nil {
		t.Fatalf("connect to the PostgreSQL of the tests: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, statement, arguments...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// fixture is a storetest.Database, given by its URL.
type fixture string

func (f fixture) Open(t *testing.T) store.Store {
	return open(t, string(f))
}

func (f fixture) Age(t *testing.T, by time.Duration, turns ...uuid.UUID) {
	exec(t, string(f), `update turns set created_at = created_at - $2::interval, dispatched_at = dispatched_at - $2::interval,
		started_at = started_at - $2::interval, cancel_requested_at = cancel_requested_at - $2::interval,
		ended_at = ended_at - $2::interval where id = any($1)`, turns, by)
	exec(t, string(f), `update approvals set requested_at = requested_at - $2::interval, answered_at = answered_at - $2::interval,
		resolved_at = resolved_at - $2::interval where turn_id = any($1)`, turns, by)
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return fixture(newDatabase(t)) })
}
