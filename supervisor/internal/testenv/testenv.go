// Package testenv gives the tests of the Supervisor what the Supervisor runs
// on. `make test-supervisor` starts it and tells the tests where it is.
package testenv

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aviary/supervisor/internal/secret"
	"aviary/supervisor/internal/store"
)

// databaseURL names the variable with the URL of a PostgreSQL in which the
// tests may create databases, eventsURL the one with the address of an
// event log, and secretStoreURL the one with the address of a secret store.
const (
	databaseURL    = "AVIARY_TEST_DATABASE_URL"
	eventsURL      = "AVIARY_TEST_EVENTS_URL"
	secretStoreURL = "AVIARY_TEST_SECRET_STORE_URL"
)

func init() {
	// What the Supervisor logs of every migration and every turn would bury
	// the failure of a test.
	slog.SetLogLoggerLevel(slog.LevelWarn)
}

// service returns the URL that the variable holds.
func service(t testing.TB, variable string) string {
	t.Helper()
	address := os.Getenv(variable)
	if address == "" {
		// A test that was skipped here would look like one that passed.
		t.Fatalf("%s is not set: run the tests with `make test-supervisor`", variable)
	}
	return address
}

// EmptyDatabase creates a database for the test and returns its URL. The
// database is dropped when the test ends.
func EmptyDatabase(t testing.TB) string {
	t.Helper()
	server := service(t, databaseURL)
	address, err := url.Parse(server)
	if err != nil {
		t.Fatalf("%s: %v", databaseURL, err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	Exec(t, server, "create database "+name)
	t.Cleanup(func() { Exec(t, server, "drop database "+name+" with (force)") })
	address.Path = "/" + name
	return address.String()
}

// Database creates a database with the schema of this build for the test
// and returns its URL.
func Database(t testing.TB) string {
	t.Helper()
	database := EmptyDatabase(t)
	if err := store.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// Open returns a Store on the database, as one Supervisor has it. Several
// Supervisors are several Stores on one database.
func Open(t testing.TB, database string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

// Exec runs a statement on the database, for what a test has to arrange
// that the Supervisor has no way to: time that has passed, or a schema that
// another release left.
func Exec(t testing.TB, database, statement string, arguments ...any) {
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

// Keys returns the keys of an installation, which its Supervisors seal
// secrets with: one key, as the secret of a new installation holds it.
func Keys(t testing.TB) *secret.Keys {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k1"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := secret.Load(dir, "k1")
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// SecretStoreURL returns the address of the secret store of the tests,
// which the drivers that ask a store are tested with. Its root token is
// "test". The tests share it: what a test keeps in it is apart by a name of
// its own.
func SecretStoreURL(t testing.TB) string {
	t.Helper()
	return service(t, secretStoreURL)
}

// EventsURL returns the address of the event log of the tests, which the
// plugins that connect to a log are tested with. The tests share it: what
// they keep in it is apart by the IDs of their turns.
func EventsURL(t testing.TB) string {
	t.Helper()
	return service(t, eventsURL)
}
