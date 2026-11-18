package identity_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aviary/supervisor/identity"
)

// plugin is a plugin for the providers of a kind, which says what it was
// opened with.
type plugin struct {
	kind   string
	opened *string
}

func (p plugin) Kind() string { return p.kind }

func (p plugin) Open(_ context.Context, settings json.RawMessage) (identity.Provider, error) {
	*p.opened = p.kind + " " + string(settings)
	return nil, nil
}

// A provider is opened by the plugin for its kind, and a kind that the
// build has no plugin for is refused with the kinds that it has.
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
