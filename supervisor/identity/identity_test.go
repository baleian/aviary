package identity_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aviary/supervisor/identity"
)

// plugin records the settings that it was opened with.
type plugin struct {
	kind   string
	opened *string
}

func (p plugin) Kind() string { return p.kind }

func (p plugin) Open(_ context.Context, settings json.RawMessage) (identity.Provider, error) {
	*p.opened = p.kind + " " + string(settings)
	return nil, nil
}

// Open uses the plugin for the kind, and refuses an unknown kind with an
// error that lists the known kinds.
func TestOpen(t *testing.T) {
	var opened string
	plugins := []identity.Plugin{plugin{"one", &opened}, plugin{"other", &opened}}
	if _, err := identity.Open(context.Background(), "other", json.RawMessage(`{"a": 1}`), plugins...); err != nil || opened != `other {"a": 1}` {
		t.Errorf("the provider was opened as %q, with the error %v", opened, err)
	}
	_, err := identity.Open(context.Background(), "third", nil, plugins...)
	if err == nil || !strings.Contains(err.Error(), `"third"`) || !strings.Contains(err.Error(), "one, other") {
		t.Errorf("a provider of a kind without a plugin is refused with: %v", err)
	}
}
