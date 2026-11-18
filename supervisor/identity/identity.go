// Package identity tells who sends a request to the API. The Supervisor does
// not keep the users of an installation and logs nobody in: an identity
// provider does, and a caller brings a token of the provider with every
// request. A plugin connects the Supervisor to the providers of one kind. A
// kind is a way in which a token is checked, whatever product the provider
// is.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Caller is who sent a request, as the identity provider tells it.
type Caller struct {
	// Subject is what the provider knows the caller by. It is no other
	// caller's and it never changes, and it is not made for a person to
	// read.
	Subject string
	// Username is a name of the caller that a person can read and that no
	// other caller has at the provider, such as an email address. It is
	// empty if the provider gave none that can be relied on.
	Username string
	// DisplayName is what the caller is shown as. It tells nothing of who
	// the caller is.
	DisplayName string
	// Groups are the names that the provider gave the caller, whether the
	// provider calls them groups or roles.
	Groups []string
}

// Provider tells who the tokens of an identity provider belong to.
type Provider interface {
	// Caller returns who the token was issued to. It returns an error for a
	// token that is not the provider's, is not meant for this installation
	// or has expired, and for one that it cannot check.
	Caller(ctx context.Context, token string) (Caller, error)
}

// Plugin connects to the identity providers of one kind. The Supervisor
// knows the kinds that it was built with: a provider of another kind is
// served by writing a plugin for it and adding the plugin to the build.
type Plugin interface {
	// Kind is the name of the kind, which an installation gives with its
	// provider.
	Kind() string
	// Open reads the settings of a provider, as the installation gave them,
	// and connects to the provider. It tells what is wrong with the
	// settings, if anything is. Nothing in the settings is secret.
	Open(ctx context.Context, settings json.RawMessage) (Provider, error)
}

// Open connects to an identity provider of the kind, with the plugin for
// that kind.
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
