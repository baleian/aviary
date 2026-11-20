package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// scripted is a secret store plugin backed by shelves instead of a server.
// The settings of a store give its address and the credential that it takes.
type scripted struct{ holding *shelves }

// shelves holds the values of scripted stores by address, path and key.
type shelves struct {
	mu     sync.Mutex
	values map[string]string
}

// put sets the value at address, path and key, or deletes it if value is
// empty.
func (s *shelves) put(address, path, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]string{}
	}
	if value == "" {
		delete(s.values, address+" "+path+" "+key)
		return
	}
	s.values[address+" "+path+" "+key] = value
}

// storeHost is the host at which test installations allow secret stores.
const storeHost = "store.test:8200"

func (scripted) Kind() string { return "scripted" }

func (scripted) settings(raw json.RawMessage) (address, takes string, err error) {
	var store struct{ Address, Takes string }
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil || store.Address == "" {
		return "", "", errors.New("address is required")
	}
	return store.Address, store.Takes, nil
}

func (s scripted) Address(settings json.RawMessage) (string, error) {
	address, _, err := s.settings(settings)
	return address, err
}

func (s scripted) Login(_ context.Context, _ *http.Client, settings json.RawMessage, credential string) (any, time.Duration, error) {
	_, takes, err := s.settings(settings)
	if err != nil {
		return nil, 0, err
	}
	if credential != takes {
		return nil, 0, errors.New("the store takes another credential")
	}
	return credential, time.Hour, nil
}

func (s scripted) Read(_ context.Context, _ *http.Client, settings json.RawMessage, _ any, path, key string) (string, error) {
	address, _, err := s.settings(settings)
	if err != nil {
		return "", err
	}
	if s.holding == nil {
		return "", errors.New("the store holds nothing")
	}
	s.holding.mu.Lock()
	defer s.holding.mu.Unlock()
	value, found := s.holding.values[address+" "+path+" "+key]
	if !found {
		return "", errors.New("the store has no value at the path and under the key")
	}
	return value, nil
}

// secretStore is a secret store as the API returns it.
type secretStore struct {
	ID         string            `json:"id"`
	ProjectID  string            `json:"project_id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Settings   map[string]string `json:"settings"`
	Credential json.RawMessage   `json:"credential"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// scriptedStore returns the body of a scripted store at address. The store
// takes only the credential takes, and is registered with credential.
func scriptedStore(name, address, takes, credential string) string {
	return fmt.Sprintf(`{"name": %q, "kind": "scripted", "settings": {"address": %q, "takes": %q}, "credential": {"value": %q}}`,
		name, address, takes, credential)
}

// A project registers its secret stores. The Supervisor logs in to a store
// before it accepts it, and keeps the credential sealed.
func TestSecretStores(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	records := aviary.records
	const address, first, second = "https://" + storeHost, "the-first-credential", "the-second-credential"
	// answer sends a request and returns the store in the response. It fails
	// the test if the response reveals the credential.
	answer := func(method, path, body string, status int) secretStore {
		t.Helper()
		var raw json.RawMessage
		if got, _ := s.request(method, path, body, &raw); got != status {
			t.Fatalf("%s %s: unexpected status %d %s", method, path, got, raw)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["credential"]) != `{"stored":true}` || len(fields) != 8 {
			t.Errorf("%s %s response = %s, want 8 fields and a stored credential", method, path, raw)
		}
		var decoded secretStore
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	// kept returns the store's credential as stored and as opened.
	kept := func(id string) ([]byte, string) {
		t.Helper()
		record, err := records.SecretStore(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		opened, err := aviary.keys.Open(record.Credential, store.SecretStoreCredentialPlace(record.ID))
		if err != nil {
			t.Fatalf("open store credential: %v", err)
		}
		return record.Credential, opened
	}

	project := s.create("/v1/projects", `{"name": "test"}`)
	stores := "/v1/projects/" + project + "/secret-stores"
	// The "takes" setting belongs to the fake and is not a secret.
	created := answer("POST", stores, scriptedStore("a store", address, first, first), http.StatusCreated)
	if created.ProjectID != project || created.Name != "a store" || created.Kind != "scripted" ||
		created.Settings["address"] != address || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("created store = %+v", created)
	}
	path := "/v1/secret-stores/" + created.ID
	if read := answer("GET", path, "", http.StatusOK); read.ID != created.ID || read.Name != "a store" || read.Kind != "scripted" || read.Settings["address"] != address {
		t.Errorf("store = %+v", read)
	}
	sealed, opened := kept(created.ID)
	if opened != first || bytes.Contains(sealed, []byte(first)) {
		t.Errorf("stored credential %q opens to %q", sealed, opened)
	}

	// PUT replaces the whole store and logs in again.
	replaced := answer("PUT", path, scriptedStore("renamed", address+"/moved", second, second), http.StatusOK)
	if replaced.ID != created.ID || replaced.ProjectID != project || replaced.Name != "renamed" || replaced.Settings["address"] != address+"/moved" ||
		!replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("replaced store = %+v, was %+v", replaced, created)
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("replaced credential opens to %q", opened)
	}

	// A store that refuses the login is refused and leaves the existing store
	// unchanged. The reason does not reveal the credential.
	for method, target := range map[string]string{"POST": stores, "PUT": path} {
		var refused struct{ Code, Message string }
		status, _ := s.request(method, target, scriptedStore("a store", address, first, "another-credential"), &refused)
		if status != http.StatusBadRequest || refused.Code != "invalid_request" ||
			!strings.Contains(refused.Message, "login failed: the store takes another credential") ||
			strings.Contains(refused.Message, "another-credential") {
			t.Errorf("%s store with a rejected login = %d %s %q", method, status, refused.Code, refused.Message)
		}
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("credential after refused requests opens to %q", opened)
	}
}

// Invalid secret stores are refused. Since the Supervisor connects to a store
// itself, the store must be at an allowed host; if no host is allowed, no
// store can be registered.
func TestSecretStoresThatAreRefused(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	closed := install(t)
	closed.storeHosts = nil
	unlisted := closed.supervisor()
	const address, credential = "https://" + storeHost, "a-credential"
	project := s.create("/v1/projects", `{"name": "test"}`)
	stores := "/v1/projects/" + project + "/secret-stores"
	existing := "/v1/secret-stores/" + s.create(stores, scriptedStore("a store", address, credential, credential))
	elsewhere := "/v1/projects/" + unlisted.create("/v1/projects", `{"name": "test"}`) + "/secret-stores"
	none := uuid.NewString()
	// with returns the body of a store with the kind, settings and
	// credential.
	with := func(kind, settings, credential string) string {
		return fmt.Sprintf(`{"name": "a store", "kind": %q, "settings": %s, "credential": %s}`, kind, settings, credential)
	}
	settings, value := fmt.Sprintf(`{"address": %q, "takes": %q}`, address, credential), fmt.Sprintf(`{"value": %q}`, credential)
	const invalid, notFound = "invalid_request", "not_found"
	for _, test := range []struct {
		supervisor         *supervisor
		method, path, body string
		status             int
		code, why          string
	}{
		{s, "POST", stores, `{}`, http.StatusBadRequest, invalid, "name"},
		{s, "POST", stores, scriptedStore("", address, credential, credential), http.StatusBadRequest, invalid, "name"},
		{s, "POST", stores, with("another", settings, value), http.StatusBadRequest, invalid, "scripted"},
		{s, "POST", stores, with("", settings, value), http.StatusBadRequest, invalid, "scripted"},
		{s, "POST", stores, with("scripted", `{}`, value), http.StatusBadRequest, invalid, "settings: address is required"},
		{s, "POST", stores, with("scripted", `{"address": "`+address+`", "unknown": "to the kind"}`, value), http.StatusBadRequest, invalid, "settings"},
		{s, "POST", stores, with("scripted", `"`+address+`"`, value), http.StatusBadRequest, invalid, "settings"},
		{s, "POST", stores, `{"name": "a store", "kind": "scripted", "credential": ` + value + `}`, http.StatusBadRequest, invalid, "settings"},
		{s, "POST", stores, `{"name": "a store", "kind": "scripted", "settings": ` + settings + `}`, http.StatusBadRequest, invalid, "credential.value"},
		{s, "POST", stores, with("scripted", settings, `{}`), http.StatusBadRequest, invalid, "credential.value"},
		{s, "POST", stores, with("scripted", settings, `"`+credential+`"`), http.StatusBadRequest, invalid, ""},
		{s, "POST", stores, with("scripted", settings, `{"value": "`+credential+`", "stored": true}`), http.StatusBadRequest, invalid, ""},
		// Hosts that are not allowed.
		{s, "POST", stores, scriptedStore("a store", "https://other.test:8200", credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		{s, "POST", stores, scriptedStore("a store", "https://store.test", credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		{unlisted, "POST", elsewhere, scriptedStore("a store", address, credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		// IDs that do not exist, and a PUT without a credential.
		{s, "POST", "/v1/projects/" + none + "/secret-stores", scriptedStore("a store", address, credential, credential), http.StatusNotFound, notFound, ""},
		{s, "GET", "/v1/secret-stores/" + none, ``, http.StatusNotFound, notFound, ""},
		{s, "GET", "/v1/secret-stores/not-an-id", ``, http.StatusNotFound, notFound, ""},
		{s, "PUT", existing, `{"name": "a store"}`, http.StatusBadRequest, invalid, "credential.value"},
		{s, "PUT", "/v1/secret-stores/" + none, scriptedStore("a store", address, credential, credential), http.StatusNotFound, notFound, ""},
		{s, "PUT", "/v1/secret-stores/" + none, `{"name": "a store"}`, http.StatusNotFound, notFound, ""},
	} {
		var got struct{ Code, Message string }
		status, _ := test.supervisor.request(test.method, test.path, test.body, &got)
		if status != test.status || got.Code != test.code || got.Message == "" || !strings.Contains(got.Message, test.why) || strings.Contains(got.Message, "a-credential") {
			t.Errorf("%s %s %.100s = %d %s %q, want %d %s containing %q",
				test.method, test.path, test.body, status, got.Code, got.Message, test.status, test.code, test.why)
		}
	}
}
