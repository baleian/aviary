package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// scripted is a driver of the tests, for secret stores of a kind that needs
// no store to be there. The settings of such a store say where it is and
// which credential it takes.
type scripted struct{}

// storeHost is where the installations of the tests allow secret stores.
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

// secretStore is a secret store as the API answers it.
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

// scriptedStore returns the body of a request that gives a secret store of
// the tests' kind: one at the address that takes a credential, and the
// credential that the Supervisor is given for it.
func scriptedStore(name, address, takes, credential string) string {
	return fmt.Sprintf(`{"name": %q, "kind": "scripted", "settings": {"address": %q, "takes": %q}, "credential": {"value": %q}}`,
		name, address, takes, credential)
}

// A project registers the secret stores that it keeps secrets in. A store
// has a kind, which says how it is asked, and the settings of that kind.
// The Supervisor logs in to a store before it takes it, and keeps what it
// logs in with sealed.
func TestSecretStores(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	records := aviary.records
	const address, first, second = "https://" + storeHost, "the-first-credential", "the-second-credential"
	// answer sends a request about a store and returns the store that the
	// API answers with. No answer holds what a store is logged in to with.
	answer := func(method, path, body string, status int) secretStore {
		t.Helper()
		var raw json.RawMessage
		if got, _ := s.request(method, path, body, &raw); got != status {
			t.Fatalf("%s %s was answered with %d %s", method, path, got, raw)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["credential"]) != `{"stored":true}` || len(fields) != 8 {
			t.Errorf("%s %s was answered with %s", method, path, raw)
		}
		var decoded secretStore
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	// kept returns what the database holds of the store to log in with, and
	// what it opens to for that store.
	kept := func(id string) ([]byte, string) {
		t.Helper()
		record, err := records.SecretStore(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		opened, err := aviary.keys.Open(record.Credential, store.SecretStoreCredentialPlace(record.ID))
		if err != nil {
			t.Fatalf("the credential of the store does not open: %v", err)
		}
		return record.Credential, opened
	}

	project := s.create("/v1/projects", `{"name": "test"}`)
	stores := "/v1/projects/" + project + "/secret-stores"
	// The settings name the credential that the store takes, which is for
	// the tests and no secret of the store.
	created := answer("POST", stores, scriptedStore("a store", address, first, first), http.StatusCreated)
	if created.ProjectID != project || created.Name != "a store" || created.Kind != "scripted" ||
		created.Settings["address"] != address || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("the store was created as %+v", created)
	}
	path := "/v1/secret-stores/" + created.ID
	if read := answer("GET", path, "", http.StatusOK); read.ID != created.ID || read.Name != "a store" || read.Kind != "scripted" || read.Settings["address"] != address {
		t.Errorf("the store is read as %+v", read)
	}
	sealed, opened := kept(created.ID)
	if opened != first || bytes.Contains(sealed, []byte(first)) {
		t.Errorf("the database holds the credential as %q, which opens to %q", sealed, opened)
	}

	// A store is replaced whole, and tried again.
	replaced := answer("PUT", path, scriptedStore("renamed", address+"/moved", second, second), http.StatusOK)
	if replaced.ID != created.ID || replaced.ProjectID != project || replaced.Name != "renamed" || replaced.Settings["address"] != address+"/moved" ||
		!replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("the store was replaced with %+v, and was %+v", replaced, created)
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("the credential of the replaced store opens to %q", opened)
	}

	// A store that does not take the login is not taken, and what it was to
	// replace stays. The reason holds no credential.
	for method, target := range map[string]string{"POST": stores, "PUT": path} {
		var refused struct{ Code, Message string }
		status, _ := s.request(method, target, scriptedStore("a store", address, first, "another-credential"), &refused)
		if status != http.StatusBadRequest || refused.Code != "invalid_request" ||
			!strings.Contains(refused.Message, "the store does not take the login: the store takes another credential") ||
			strings.Contains(refused.Message, "another-credential") {
			t.Errorf("%s of a store that does not take the login was answered with %d %s %q", method, status, refused.Code, refused.Message)
		}
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("after requests that were refused, the credential of the store opens to %q", opened)
	}
}

// What is no secret store, the API does not take. The Supervisor itself
// connects to a store, so a store is to be at a host that the installation
// allows: without one, nobody registers a store.
func TestSecretStoresThatAreRefused(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	closed := install(t)
	closed.stores = allowing(t)
	unlisted := closed.supervisor()
	const address, credential = "https://" + storeHost, "a-credential"
	project := s.create("/v1/projects", `{"name": "test"}`)
	stores := "/v1/projects/" + project + "/secret-stores"
	existing := "/v1/secret-stores/" + s.create(stores, scriptedStore("a store", address, credential, credential))
	elsewhere := "/v1/projects/" + unlisted.create("/v1/projects", `{"name": "test"}`) + "/secret-stores"
	none := uuid.NewString()
	// with returns the body of a store that is as it should be but for its
	// kind, its settings or its credential.
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
		// The host of a store.
		{s, "POST", stores, scriptedStore("a store", "https://other.test:8200", credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		{s, "POST", stores, scriptedStore("a store", "https://store.test", credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		{unlisted, "POST", elsewhere, scriptedStore("a store", address, credential, credential), http.StatusBadRequest, invalid, "supervisor.secretStores.allowedHosts"},
		// What is not there.
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
			t.Errorf("%s %s %.100s was answered with %d %s %q, not with %d %s that tells of %q",
				test.method, test.path, test.body, status, got.Code, got.Message, test.status, test.code, test.why)
		}
	}
}
