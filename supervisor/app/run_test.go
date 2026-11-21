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
	"aviary/supervisor/identity"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// build is a fake build whose plugins record in asked the calls made to them.
// Its database and event log plugins serve the schemes records and log from
// memory, its secret store kind is "recorded" and its identity provider kind
// is "callers".
type build struct {
	asked []string
}

func (b *build) plugins() Plugins {
	return Plugins{
		Databases: []store.Plugin{database{b}}, EventLogs: []events.Plugin{eventLog{b}}, SecretStores: []secret.Plugin{secretStores{b}},
		IdentityProviders: []identity.Plugin{identityProviders{b}},
	}
}

type identityProviders struct{ *build }

func (identityProviders) Kind() string { return "callers" }

func (p identityProviders) Open(_ context.Context, settings json.RawMessage) (identity.Provider, error) {
	p.asked = append(p.asked, "open the identity provider "+string(settings))
	return tokens{}, nil
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

// opened is a store that records when it is closed.
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

// openedLog is an event log that records when it is closed.
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

func (secretStores) Read(context.Context, *http.Client, json.RawMessage, any, string, string) (string, error) {
	return "", errors.New("there is no such store")
}

// "migrate" and "migrate <version>" call the database plugin selected by the
// URL scheme.
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
			t.Errorf("Run(%q): plugin calls %q, error %v", test.args, build.asked, err)
		}
	}
	for _, wrong := range [][]string{{"migrate", "latest"}, {"migrate", "7", "8"}, {"serve"}, {"state-metrics", "7"}} {
		build := &build{}
		if err := Run(ctx, wrong, build.plugins()); err == nil || len(build.asked) != 0 {
			t.Errorf("Run(%q): plugin calls %q, error %v; want an error and no calls", wrong, build.asked, err)
		}
	}
	// A database scheme without a plugin.
	t.Setenv(databaseURL, "postgres://user:secret@db.example/aviary")
	if err := Run(ctx, []string{"migrate"}, (&build{}).plugins()); err == nil || !strings.Contains(err.Error(), "records") {
		t.Errorf("migrate with an unknown database scheme: %v, want an error listing the schemes", err)
	}
	t.Setenv(databaseURL, "")
	if err := Run(ctx, []string{"migrate"}, (&build{}).plugins()); err == nil || !strings.Contains(err.Error(), databaseURL) {
		t.Errorf("migrate without a database URL: %v, want an error naming the setting", err)
	}
}

// "state-metrics" needs the harnesses, and reads the database through the
// plugin selected by the URL scheme.
func TestRunServesStateMetricsFromTheDatabase(t *testing.T) {
	t.Setenv(databaseURL, "postgres://user:secret@db.example/aviary")
	t.Setenv(harnesses, "")
	err := Run(context.Background(), []string{"state-metrics"}, (&build{}).plugins())
	if err == nil || !strings.Contains(err.Error(), harnesses) {
		t.Errorf("state-metrics without harnesses: %v, want an error naming the setting", err)
	}
	t.Setenv(harnesses, `{"opencode": {"image": "registry.example/opencode:1"}}`)
	err = Run(context.Background(), []string{"state-metrics"}, (&build{}).plugins())
	if err == nil || !strings.Contains(err.Error(), "records") || strings.Contains(err.Error(), "secret") {
		t.Errorf("state-metrics with an unknown database scheme: %v, want an error listing the schemes without the password", err)
	}
}

// Serving opens the database and the event log with the plugins selected by
// their URL schemes, uses the secret store and identity provider plugins of
// the build, and closes the database and the event log when it returns.
func TestRunConnectsWithThePluginsOfTheBuild(t *testing.T) {
	t.Setenv(databaseURL, "records://db.example/aviary")
	t.Setenv(eventsURL, "log://events.example:6379")
	// Without encryption keys, startup fails after the database and the
	// event log are opened.
	t.Setenv(encryptionKeys, "")
	build := &build{}
	err := Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), encryptionKeys) {
		t.Errorf("Run without encryption keys: %v, want an error naming the setting", err)
	}
	expected := []string{"open records://db.example/aviary", "open log://events.example:6379", "close the event log", "close the database"}
	if !slices.Equal(build.asked, expected) {
		t.Errorf("plugin calls = %q, want %q", build.asked, expected)
	}

	// With the keys, startup fails at the harness settings.
	key := make([]byte, 32)
	rand.Read(key)
	keys := t.TempDir()
	if err := os.WriteFile(filepath.Join(keys, "k1"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(encryptionKeys, keys)
	t.Setenv(encryptionKey, "k1")
	t.Setenv(secretStoreHosts, `["*"]`)
	t.Setenv(secretCacheSeconds, "60")
	t.Setenv(identityProvider, `{"kind": "callers", "settings": {"of": "the test"}}`)
	t.Setenv(adminGroups, `[]`)
	t.Setenv(userGroups, `[]`)
	t.Setenv(harnesses, "")
	build.asked = nil
	err = Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), harnesses) {
		t.Errorf("Run without harnesses: %v, want an error naming the setting", err)
	}
	expected = []string{
		"open records://db.example/aviary", "open log://events.example:6379", "the kind of the secret stores",
		`open the identity provider {"of": "the test"}`, "close the event log", "close the database",
	}
	if !slices.Equal(build.asked, expected) {
		t.Errorf("plugin calls = %q, want %q", build.asked, expected)
	}

	// An event log scheme without a plugin.
	t.Setenv(eventsURL, "amqp://user:secret@events.example")
	build.asked = nil
	err = Run(context.Background(), nil, build.plugins())
	if err == nil || !strings.Contains(err.Error(), "log") || strings.Contains(err.Error(), "secret") {
		t.Errorf("Run with an unknown event log scheme: %v, want an error listing the schemes without the password", err)
	}
	if expected := []string{"open records://db.example/aviary", "close the database"}; !slices.Equal(build.asked, expected) {
		t.Errorf("plugin calls = %q, want %q", build.asked, expected)
	}
}
