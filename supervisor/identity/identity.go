// Package identity authenticates API requests with the tokens of an identity
// provider. The Supervisor does not manage users or log anyone in: a caller
// sends a token of the provider with every request. Plugins implement the
// kinds of providers; a kind is a way of checking tokens, whatever product
// the provider is.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Caller is the sender of a request, as the identity provider reports it.
type Caller struct {
	// Subject identifies the caller at the provider. It is unique and never
	// changes, and it is not meant to be read by people.
	Subject string
	// Username is a human-readable name that is unique at the provider,
	// such as an email address. It is empty if the provider gave none that
	// can be relied on.
	Username string
	// DisplayName is the name to show for the caller. It does not identify
	// the caller.
	DisplayName string
	// Groups are the caller's group or role names at the provider.
	Groups []string
}

// Provider authenticates the tokens of an identity provider.
type Provider interface {
	// Caller returns the caller to whom token was issued. It returns an
	// error if the token was not issued by the provider for this
	// installation, has expired, or cannot be checked.
	Caller(ctx context.Context, token string) (Caller, error)
}

// Plugin connects to the identity providers of one kind. To support another
// kind, add its plugin to the build.
type Plugin interface {
	// Kind returns the name of the kind, which the installation gives with
	// its provider.
	Kind() string
	// Open validates the settings of a provider and connects to it. The
	// error says what is wrong with the settings. Settings are not treated
	// as secret.
	Open(ctx context.Context, settings json.RawMessage) (Provider, error)
}

// Open connects to an identity provider with the plugin for kind.
func Open(ctx context.Context, kind string, settings json.RawMessage, plugins ...Plugin) (Provider, error) {
	kinds := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin.Kind() == kind {
			return plugin.Open(ctx, settings)
		}
		kinds = append(kinds, plugin.Kind())
	}
	return nil, fmt.Errorf("no plugin for an identity provider of the kind %q: this build has %s", kind, strings.Join(kinds, ", "))
}
