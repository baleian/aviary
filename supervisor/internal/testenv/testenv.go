// Package testenv provides the services that the Supervisor's tests need.
// The plugin tests connect to a database, an event log and a secret store,
// which `make test-supervisor` starts and passes in environment variables.
package testenv

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aviary/supervisor/secret"
)

// secretStoreURL is the environment variable with the URL of the secret
// store.
const secretStoreURL = "AVIARY_TEST_SECRET_STORE_URL"

func init() {
	// Info logs of migrations and turns would bury test failures.
	slog.SetLogLoggerLevel(slog.LevelWarn)
}

// service returns the URL in the environment variable, and fails the test if
// it is not set.
func service(t testing.TB, variable string) string {
	t.Helper()
	address := os.Getenv(variable)
	if address == "" {
		// Fail rather than skip, since a skipped test looks like a pass.
		t.Fatalf("%s is not set: run the tests with `make test-supervisor`", variable)
	}
	return address
}

// DatabaseURL returns the URL of the test database server for the scheme.
// Tests share the server, and each creates a database of its own.
func DatabaseURL(t testing.TB, scheme string) string {
	t.Helper()
	return service(t, "AVIARY_TEST_"+strings.ToUpper(scheme)+"_URL")
}

// Keys returns encryption keys with one random key, as a new installation
// has.
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

// SecretStoreURL returns the URL of the test secret store. Its root token is
// "test". Tests share it, and keep their secrets under names of their own.
func SecretStoreURL(t testing.TB) string {
	t.Helper()
	return service(t, secretStoreURL)
}

// EventsURL returns the URL of the test event log for the scheme. Tests
// share it, kept apart by the IDs of their turns.
func EventsURL(t testing.TB, scheme string) string {
	t.Helper()
	return service(t, "AVIARY_TEST_"+strings.ToUpper(scheme)+"_URL")
}
