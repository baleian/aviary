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

// An unsupported scheme or kind is refused with an error that lists the
// supported ones and does not reveal credentials.
func TestPluginsOfThisBuild(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AVIARY_DATABASE_URL", "another://user:secret@host/records")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err == nil ||
		!strings.Contains(err.Error(), "postgres, postgresql, mysql") || strings.Contains(err.Error(), "secret") {
		t.Errorf("migrate with an unknown database scheme: %v", err)
	}
	if _, err := events.Open("amqp://user:secret@host", plugins.EventLogs...); err == nil ||
		!strings.Contains(err.Error(), "valkey, valkeys, redis, rediss") || strings.Contains(err.Error(), "secret") {
		t.Errorf("Open with an unknown event log scheme: %v", err)
	}
	// A supported scheme with a URL that the plugin cannot parse.
	if _, err := events.Open("valkey://user:secret@host:port", plugins.EventLogs...); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("Open with an invalid port: %v", err)
	}
	stores, err := secret.NewStores([]string{"*"}, time.Minute, plugins.SecretStores...)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	if err := stores.Check(ctx, "another", json.RawMessage(`{}`), "a-credential"); err == nil || !strings.Contains(err.Error(), "kv-v2") {
		t.Errorf("Check of an unknown secret store kind: %v", err)
	}
	if _, err := identity.Open(ctx, "another", json.RawMessage(`{}`), plugins.IdentityProviders...); err == nil || !strings.Contains(err.Error(), "oidc") {
		t.Errorf("Open of an unknown identity provider kind: %v", err)
	}
}
