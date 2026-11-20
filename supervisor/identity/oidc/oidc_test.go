package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"aviary/supervisor/identity"
)

// issuer is a fake OpenID Connect issuer that serves its discovery document
// and keys, and signs tokens.
type issuer struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	keys   []jose.JSONWebKey
	// fetched counts the requests for the keys.
	fetched int
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	i := &issuer{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                i.server.URL,
			"jwks_uri":                              i.server.URL + "/keys",
			"authorization_endpoint":                i.server.URL + "/authorize",
			"token_endpoint":                        i.server.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		i.mu.Lock()
		defer i.mu.Unlock()
		i.fetched++
		public := jose.JSONWebKeySet{}
		for _, key := range i.keys {
			public.Keys = append(public.Keys, key.Public())
		}
		_ = json.NewEncoder(w).Encode(public)
	})
	i.server = httptest.NewServer(mux)
	t.Cleanup(i.server.Close)
	i.rotate()
	return i
}

// rotate replaces the issuer's key with a new one.
func (i *issuer) rotate() {
	i.t.Helper()
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys = []jose.JSONWebKey{newKey(i.t, len(i.keys))}
}

func newKey(t *testing.T, number int) jose.JSONWebKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return jose.JSONWebKey{Key: key, KeyID: "key-" + string(rune('a'+number)), Algorithm: "RS256", Use: "sig"}
}

// token returns a token for the audience "aviary" that is valid for a
// minute, with claims added or overridden.
func (i *issuer) token(claims map[string]any) string {
	i.t.Helper()
	i.mu.Lock()
	key := i.keys[0]
	i.mu.Unlock()
	return sign(i.t, key, i.claims(claims))
}

func (i *issuer) claims(more map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{"iss": i.server.URL, "aud": "aviary", "sub": "subject-1", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()}
	for name, value := range more {
		claims[name] = value
	}
	return claims
}

func sign(t *testing.T, key jose.JSONWebKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// open opens a provider for the issuer with claims, in JSON, as its claims
// setting, or without one if claims is empty.
func open(t *testing.T, i *issuer, claims string) identity.Provider {
	t.Helper()
	settings := map[string]any{"issuer": i.server.URL, "audience": "aviary"}
	if claims != "" {
		settings["claims"] = json.RawMessage(claims)
	}
	written, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := identity.Open(context.Background(), Kind, written, Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// The fields of the caller are read from the claims that the settings name.
func TestCaller(t *testing.T) {
	i := newIssuer(t)
	told := map[string]any{
		"name": "Alice Kim", "preferred_username": "alice", "email": "alice@example.com", "email_verified": true,
		"groups": []string{"team-a", "admins"}, "realm_access": map[string]any{"roles": []string{"builder"}},
		"https://example.com/teams": "one-team", "oid": "object-1", "department": 7,
	}
	for name, test := range map[string]struct {
		claims string
		told   map[string]any
		caller identity.Caller
	}{
		"the claims that a provider has unless its settings say otherwise": {
			caller: identity.Caller{Subject: "subject-1", DisplayName: "Alice Kim", Groups: []string{"team-a", "admins"}},
		},
		"the claims that the settings name": {
			claims: `{"subject": "oid", "username": "preferred_username", "displayName": "email", "groups": "realm_access.roles"}`,
			caller: identity.Caller{Subject: "object-1", Username: "alice", DisplayName: "alice@example.com", Groups: []string{"builder"}},
		},
		"a claim whose name has dots, and groups that are one name": {
			claims: `{"groups": "https://example.com/teams"}`,
			caller: identity.Caller{Subject: "subject-1", DisplayName: "Alice Kim", Groups: []string{"one-team"}},
		},
		"claims that the token does not have, or that are no texts": {
			claims: `{"username": "nickname", "displayName": "department", "groups": "realm_access.groups.names"}`,
			caller: identity.Caller{Subject: "subject-1"},
		},
		"claims that the settings empty": {
			claims: `{"displayName": "", "groups": ""}`,
			caller: identity.Caller{Subject: "subject-1"},
		},
		"an email address that its owner was seen to use": {
			claims: `{"username": "email"}`,
			caller: identity.Caller{Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a", "admins"}},
		},
		"an email address that its owner was not seen to use": {
			claims: `{"username": "email"}`,
			told:   map[string]any{"email_verified": false},
			caller: identity.Caller{Subject: "subject-1", DisplayName: "Alice Kim", Groups: []string{"team-a", "admins"}},
		},
		"an email address of which the issuer says nothing": {
			claims: `{"username": "email"}`,
			told:   map[string]any{"email_verified": nil},
			caller: identity.Caller{Subject: "subject-1", DisplayName: "Alice Kim", Groups: []string{"team-a", "admins"}},
		},
		"a token for more than one audience": {
			told:   map[string]any{"aud": []string{"account", "aviary"}},
			caller: identity.Caller{Subject: "subject-1", DisplayName: "Alice Kim", Groups: []string{"team-a", "admins"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			claims := map[string]any{}
			for name, value := range told {
				claims[name] = value
			}
			for name, value := range test.told {
				claims[name] = value
			}
			caller, err := open(t, i, test.claims).Caller(context.Background(), i.token(claims))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(caller, test.caller) {
				t.Errorf("the caller is %+v, not %+v", caller, test.caller)
			}
		})
	}
}

func TestTokensThatAreRefused(t *testing.T) {
	i := newIssuer(t)
	provider := open(t, i, "")
	now := time.Now()
	changed := i.token(nil)
	middle := len(changed) - 40
	changed = changed[:middle] + map[bool]string{true: "B", false: "A"}[changed[middle] == 'A'] + changed[middle+1:]
	for name, token := range map[string]string{
		"a token that has expired":                     i.token(map[string]any{"iat": now.Add(-time.Hour).Unix(), "exp": now.Add(-time.Minute).Unix()}),
		"a token that is not good yet":                 i.token(map[string]any{"nbf": now.Add(time.Hour).Unix()}),
		"a token for another audience":                 i.token(map[string]any{"aud": "another"}),
		"a token that names another issuer":            i.token(map[string]any{"iss": "https://another.example.com"}),
		"a token that was signed with another key":     sign(t, newKey(t, 0), i.claims(nil)),
		"a token that was changed after it was signed": changed,
		"a token without a subject":                    i.token(map[string]any{"sub": ""}),
		"a text that is no token":                      "not-a-token",
		"no token":                                     "",
	} {
		if caller, err := provider.Caller(context.Background(), token); err == nil {
			t.Errorf("%s tells of the caller %+v", name, caller)
		}
	}
	if _, err := provider.Caller(context.Background(), i.token(nil)); err != nil {
		t.Errorf("a token of the issuer is refused after the others: %v", err)
	}
}

// Tokens signed with a rotated key are accepted, and the keys are not
// fetched for every token.
func TestIssuerChangesItsKey(t *testing.T) {
	i := newIssuer(t)
	provider := open(t, i, "")
	for range 3 {
		if _, err := provider.Caller(context.Background(), i.token(nil)); err != nil {
			t.Fatal(err)
		}
	}
	i.mu.Lock()
	fetched := i.fetched
	i.mu.Unlock()
	if fetched != 1 {
		t.Errorf("the keys were fetched %d times for three tokens, not once", fetched)
	}
	before := i.token(nil)
	i.rotate()
	if _, err := provider.Caller(context.Background(), i.token(nil)); err != nil {
		t.Errorf("a token that was signed with the new key is refused: %v", err)
	}
	// The old key is dropped some time after the new keys are fetched, not
	// at once.
	for forgotten := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := provider.Caller(context.Background(), before); err != nil {
			break
		}
		if time.Now().After(forgotten) {
			t.Fatal("a token that was signed with a key that the issuer has no more is still taken")
		}
	}
}

// Invalid settings are refused with an error that says what is wrong.
func TestSettings(t *testing.T) {
	i := newIssuer(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	for settings, wrong := range map[string]string{
		`{"audience": "aviary"}`:             "need an issuer",
		`{"issuer": "` + i.server.URL + `"}`: "need an issuer, an audience",
		`{"issuer": "` + i.server.URL + `", "audience": "a", "claims": {"subject": ""}}`:          "a claim for the subject",
		`{"issuer": "` + i.server.URL + `", "audience": "a", "claims": {"display_name": "name"}}`: "display_name",
		`{"issuer": "` + i.server.URL + `", "audience": "a", "client_secret": "s"}`:               "client_secret",
		`{"issuer": "` + other.URL + `", "audience": "a"}`:                                        other.URL,
		// The discovery document names a different issuer.
		`{"issuer": "` + i.server.URL + `/", "audience": "a"}`: "issuer",
		`not JSON`: "settings",
	} {
		_, err := Plugin{}.Open(context.Background(), json.RawMessage(settings))
		if err == nil || !strings.Contains(err.Error(), wrong) {
			t.Errorf("the settings %s are refused with %v, which does not say %q", settings, err, wrong)
		}
	}
}
