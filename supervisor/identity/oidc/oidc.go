// Package oidc authenticates the tokens of an OpenID Connect issuer. More
// than one product can be such an issuer.
//
// A provider of this kind has these settings:
//
//	{"issuer": "https://login.example.com/realms/main", "audience": "aviary",
//	 "claims": {"subject": "sub", "username": "email", "displayName": "name", "groups": "groups"}}
//
// The issuer is the URL below which the provider publishes its discovery
// document and signing keys. A token must be a JWT, either an ID token or an
// access token, that the issuer signed for the audience and that has not
// expired.
//
// The claims name the token claims that hold the caller's fields, and are all
// optional. The subject defaults to "sub", the display name to "name" and the
// groups to "groups"; a caller has a username only if its claim is set. A
// claim is named as in the token, or by its dotted path if it is nested, such
// as "realm_access.roles". The groups claim is a list of names or one name.
//
// If the username claim is "email", the address is used only if the
// email_verified claim is true.
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

// Kind is the identity provider kind that this package serves.
const Kind = "oidc"

// Plugin is the identity.Plugin for providers of this kind.
type Plugin struct{}

func (Plugin) Kind() string { return Kind }

type settings struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Claims   claims `json:"claims"`
}

// claims names the token claims that hold the caller's fields.
type claims struct {
	Subject     string `json:"subject"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Groups      string `json:"groups"`
}

// Open fetches the discovery document of the issuer. The signing keys are
// fetched when a token first needs them, and again when a token is signed
// with an unknown key.
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
	// An unverified email address could belong to anyone.
	if seen, _ := find(told, "email_verified").(bool); seen || p.claims.Username != "email" {
		caller.Username = text(told, p.claims.Username)
	}
	return caller, nil
}

// find returns the claim called name or, if there is none, the nested claim
// at the dotted path name.
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

// text returns the named claim if it is a string.
func text(told map[string]any, name string) string {
	value, _ := find(told, name).(string)
	return value
}

// texts returns the strings of the named claim, which is a string or a list.
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
