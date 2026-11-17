// Package testenv gives the tests of the Supervisor what the Supervisor runs
// on. The tests of the plugins connect to a database, an event log and a
// secret store: `make test-supervisor` starts those and tells the tests
// where they are.
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

// secretStoreURL names the variable with the address of a secret store.
const secretStoreURL = "AVIARY_TEST_SECRET_STORE_URL"

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

// DatabaseURL returns the address of the database server of the tests whose
// addresses begin with the scheme, which the plugin for databases of that
// kind is tested with. The tests share it: each creates a database of its
// own in it.
func DatabaseURL(t testing.TB, scheme string) string {
	t.Helper()
	return service(t, "AVIARY_TEST_"+strings.ToUpper(scheme)+"_URL")
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
// which the plugins that ask a store are tested with. Its root token is
// "test". The tests share it: what a test keeps in it is apart by a name of
// its own.
func SecretStoreURL(t testing.TB) string {
	t.Helper()
	return service(t, secretStoreURL)
}

// EventsURL returns the address of the event log of the tests whose
// addresses begin with the scheme, which the plugin for logs of that kind is
// tested with. The tests share it: what they keep in it is apart by the IDs
// of their turns.
func EventsURL(t testing.TB, scheme string) string {
	t.Helper()
	return service(t, "AVIARY_TEST_"+strings.ToUpper(scheme)+"_URL")
}
