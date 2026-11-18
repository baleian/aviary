// Package oidc tells who the tokens of an OpenID Connect issuer belong to.
// More than one product is such an issuer.
//
// A provider of this kind has these settings:
//
//	{"issuer": "https://login.example.com/realms/main", "audience": "aviary",
//	 "claims": {"subject": "sub", "username": "email", "displayName": "name", "groups": "groups"}}
//
// The issuer is the URL below which the provider publishes its configuration
// and the keys that it signs with. The audience is whom a token must be meant
// for. A token is a JWT that the issuer signed for that audience and that has
// not expired, be it an ID token or an access token.
//
// The claims say where a token tells of its caller, and all of them may be
// left out. The subject is "sub" unless the settings say otherwise, the
// display name is "name" and the groups are "groups". A caller has a username
// only if the settings name its claim. A claim is named as it is in the
// token, or by its path with dots if it is inside another, such as
// "realm_access.roles". The groups are a list of names, or one name.
//
// An email address is a username only if the issuer says that it has seen
// its owner use it, with the claim "email_verified".
package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"aviary/supervisor/identity"
)

// Kind is the name of the kind of provider that this package asks.
const Kind = "oidc"

// Plugin connects to the providers of the kind.
type Plugin struct{}

func (Plugin) Kind() string { return Kind }

// settings are the settings of a provider.
type settings struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Claims   claims `json:"claims"`
}

// claims are the names of the claims that tell of a caller.
type claims struct {
	Subject     string `json:"subject"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Groups      string `json:"groups"`
}

// Open reads the configuration that the issuer publishes. The keys of the
// issuer are fetched when a token needs them, and again when a token is
// signed with one that is not among them.
func (Plugin) Open(ctx context.Context, written json.RawMessage) (identity.Provider, error) {
	set := settings{Claims: claims{Subject: "sub", DisplayName: "name", Groups: "groups"}}
	decoder := json.NewDecoder(bytes.NewReader(written))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return nil, fmt.Errorf("the settings of the identity provider: %w", err)
	}
	if set.Issuer == "" || set.Audience == "" || set.Claims.Subject == "" {
		return nil, errors.New("the settings of the identity provider need an issuer, an audience and a claim for the subject")
	}
	issuer, err := gooidc.NewProvider(ctx, set.Issuer)
	if err != nil {
		return nil, fmt.Errorf("the identity provider at %s: %w", set.Issuer, err)
	}
	return &provider{verifier: issuer.Verifier(&gooidc.Config{ClientID: set.Audience}), claims: set.Claims}, nil
}

type provider struct {
	verifier *gooidc.IDTokenVerifier
	claims   claims
}

func (p *provider) Caller(ctx context.Context, token string) (identity.Caller, error) {
	verified, err := p.verifier.Verify(ctx, token)
	if err != nil {
		return identity.Caller{}, err
	}
	var told map[string]any
	if err := verified.Claims(&told); err != nil {
		return identity.Caller{}, err
	}
	caller := identity.Caller{DisplayName: text(told, p.claims.DisplayName), Groups: texts(told, p.claims.Groups)}
	if caller.Subject = text(told, p.claims.Subject); caller.Subject == "" {
		return identity.Caller{}, fmt.Errorf("the token names no caller in the claim %q", p.claims.Subject)
	}
	// An address that the issuer has not seen its owner use could be
	// anybody's.
	if seen, _ := find(told, "email_verified").(bool); seen || p.claims.Username != "email" {
		caller.Username = text(told, p.claims.Username)
	}
	return caller, nil
}

// find returns the claim with the name: the claim that is called so, or
// else the one at the end of the name as a path with dots.
func find(told map[string]any, name string) any {
	if name == "" {
		return nil
	}
	if value, ok := told[name]; ok {
		return value
	}
	var value any = told
	for _, step := range strings.Split(name, ".") {
		inside, _ := value.(map[string]any)
		value = inside[step]
	}
	return value
}

// text returns the claim with the name if it is a text.
func text(told map[string]any, name string) string {
	value, _ := find(told, name).(string)
	return value
}

// texts returns the texts of the claim with the name, which is a list of
// them or one.
func texts(told map[string]any, name string) []string {
	var all []string
	switch value := find(told, name).(type) {
	case string:
		all = append(all, value)
	case []any:
		for _, one := range value {
			if one, ok := one.(string); ok {
				all = append(all, one)
			}
		}
	}
	return all
}
