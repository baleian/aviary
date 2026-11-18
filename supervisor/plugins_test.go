package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/app"
	"aviary/supervisor/events"
	"aviary/supervisor/identity"
	"aviary/supervisor/secret"
)

// This build connects to the kinds of database, of event log, of secret
// store and of identity provider that its plugins are for, and tells of them
// when it is given another.
func TestPluginsOfThisBuild(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AVIARY_DATABASE_URL", "another://user:secret@host/records")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err == nil ||
		!strings.Contains(err.Error(), "postgres, postgresql, mysql") || strings.Contains(err.Error(), "secret") {
		t.Errorf("a database that the build has no plugin for was migrated with: %v", err)
	}
	if _, err := events.Open("amqp://user:secret@host", plugins.EventLogs...); err == nil ||
		!strings.Contains(err.Error(), "valkey, valkeys, redis, rediss") || strings.Contains(err.Error(), "secret") {
		t.Errorf("an event log that the build has no plugin for was opened with: %v", err)
	}
	// An address that the plugin of its scheme cannot use.
	if _, err := events.Open("valkey://user:secret@host:port", plugins.EventLogs...); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("an event log with a port that is none was opened with: %v", err)
	}
	stores, err := secret.NewStores([]string{"*"}, time.Minute, plugins.SecretStores...)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	if err := stores.Check(ctx, "another", json.RawMessage(`{}`), "a-credential"); err == nil || !strings.Contains(err.Error(), "kv-v2") {
		t.Errorf("a secret store of a kind that the build has no plugin for is checked as: %v", err)
	}
	if _, err := identity.Open(ctx, "another", json.RawMessage(`{}`), plugins.IdentityProviders...); err == nil || !strings.Contains(err.Error(), "oidc") {
		t.Errorf("an identity provider of a kind that the build has no plugin for is opened with: %v", err)
	}
}
