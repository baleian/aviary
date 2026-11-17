package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// build is a build of the tests. Its plugins connect to databases at
// records:// and to event logs at log://, which are in memory, and ask
// secret stores of the kind "recorded". It tells what was asked of them.
type build struct {
	asked []string
}

func (b *build) plugins() Plugins {
	return Plugins{
		Databases: []store.Plugin{database{b}}, EventLogs: []events.Plugin{eventLog{b}}, SecretStores: []secret.Plugin{secretStores{b}},
	}
}

type database struct{ *build }

func (database) Schemes() []string { return []string{"records"} }

func (d database) Open(_ context.Context, address string) (store.Store, error) {
	d.asked = append(d.asked, "open "+address)
	return opened{Store: memorystore.New(), build: d.build}, nil
}

func (d database) Migrate(_ context.Context, address string) error {
	d.asked = append(d.asked, "migrate "+address)
	return nil
}

func (d database) MigrateTo(_ context.Context, address string, version int64) error {
	d.asked = append(d.asked, fmt.Sprintf("migrate %s to %d", address, version))
	return nil
}

// opened is a store that tells when it is closed.
type opened struct {
	store.Store
	*build
}

func (o opened) Close() { o.asked = append(o.asked, "close the database") }

type eventLog struct{ *build }

func (eventLog) Schemes() []string { return []string{"log"} }

func (l eventLog) Open(address string) (events.Log, error) {
	l.asked = append(l.asked, "open "+address)
	return openedLog{Log: memorylog.New(), build: l.build}, nil
}

// openedLog is an event log that tells when it is closed.
type openedLog struct {
	events.Log
	*build
}

func (o openedLog) Close() { o.asked = append(o.asked, "close the event log") }

type secretStores struct{ *build }

func (s secretStores) Kind() string {
	s.asked = append(s.asked, "the kind of the secret stores")
	return "recorded"
}

func (secretStores) Address(json.RawMessage) (string, error) {
	return "", errors.New("there is no such store")
}

func (secretStores) Login(context.Context, *http.Client, json.RawMessage, string) (any, time.Duration, error) {
	return nil, 0, errors.New("there is no such store")
}

// With the argument "migrate" a Supervisor brings the schema of its database
// up to date, or moves it to a version, with the plugin that the address of
// the database names.
func TestRunMigrates(t *testing.T) {
	ctx := context.Background()
	t.Setenv(databaseURL, "records://db.example/aviary")
	for _, test := range []struct {
		args  []string
		asked string
	}{
		{[]string{"migrate"}, "migrate records://db.example/aviary"},
		{[]string{"migrate", "7"}, "migrate records://db.example/aviary to 7"},
	} {
		build := &build{}
		if err := Run(ctx, test.args, build.plugins()); err != nil || !slices.Equal(build.asked, []string{test.asked}) {
			t.Errorf("with the arguments %q, the plugin was asked %q: %v", test.args, build.asked, err)
		}
	}
	for _, wrong := range [][]string{{"migrate", "latest"}, {"migrate", "7", "8"}, {"serve"}} {
		build := &build{}
		if err := Run(ctx, wrong, build.plugins()); err == nil || len(build.asked) != 0 {
			t.Errorf("with the arguments %q, the plugin was asked %q: %v", wrong, build.asked, err)
		}
	}
	// A database of a kind that the build has no plugin for.
	t.Setenv(databaseURL, "postgres://user:secret@db.example/aviary")
	if err := Run(ctx, []string{"migrate"}, (&build{}).plugins()); err == nil || !strings.Contains(err.Error(), "records") {
		t.Errorf("a database that the build has no plugin for was migrated with: %v", err)
	}
	t.Setenv(databaseURL, "")
	if err := Run(ctx, []string{"migrate"}, (&build{}).plugins()); err == nil || !strings.Contains(err.Error(), databaseURL) {
		t.Errorf("a Supervisor without the address of its database migrated with: %v", err)
	}
}

// A Supervisor that serves connects to its database and to its event log
// with the plugins that their addresses name, and lets go of both when it
// stops. The secret stores of its projects are those of the kinds that its
// build has plugins for.
func TestRunConnectsWithThePluginsOfTheBuild(t *testing.T) {
	t.Setenv(databaseURL, "records://db.example/aviary")
	t.Setenv(eventsURL, "log://events.example:6379")
	// The Supervisor of this test gets no further than the keys that it
	// seals secrets with, which it is not given.
	t.Setenv(encryptionKeys, "")
	build := &build{}
	err := Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), encryptionKeys) {
		t.Errorf("a Supervisor without its keys stopped with: %v", err)
	}
	expected := []string{"open records://db.example/aviary", "open log://events.example:6379", "close the event log", "close the database"}
	if !slices.Equal(build.asked, expected) {
		t.Errorf("the plugins were asked %q, not %q", build.asked, expected)
	}

	// With its keys, the Supervisor gets as far as how it is to run turns,
	// which it is not told.
	key := make([]byte, 32)
	rand.Read(key)
	keys := t.TempDir()
	if err := os.WriteFile(filepath.Join(keys, "k1"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(encryptionKeys, keys)
	t.Setenv(encryptionKey, "k1")
	t.Setenv(secretStoreHosts, `["*"]`)
	t.Setenv(gatewayURL, "")
	build.asked = nil
	err = Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), gatewayURL) {
		t.Errorf("a Supervisor that is not told how to run turns stopped with: %v", err)
	}
	expected = []string{
		"open records://db.example/aviary", "open log://events.example:6379", "the kind of the secret stores",
		"close the event log", "close the database",
	}
	if !slices.Equal(build.asked, expected) {
		t.Errorf("the plugins were asked %q, not %q", build.asked, expected)
	}

	// An event log of a kind that the build has no plugin for.
	t.Setenv(eventsURL, "amqp://user:secret@events.example")
	build.asked = nil
	err = Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), "log") || strings.Contains(err.Error(), "secret") {
		t.Errorf("a Supervisor with an event log that its build has no plugin for stopped with: %v", err)
	}
	if expected := []string{"open records://db.example/aviary", "close the database"}; !slices.Equal(build.asked, expected) {
		t.Errorf("the plugins were asked %q, not %q", build.asked, expected)
	}
}
