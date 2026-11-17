package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// provider is a model provider as the API answers it.
type provider struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	APIKey    json.RawMessage `json:"api_key"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// A project has model providers: model endpoints and the keys to them. A key
// goes in and does not come out: the API never answers with it, and the
// database holds it sealed.
func TestProviders(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	records := aviary.records
	const first, second = "sk-the-first-key", "sk-the-second-key"
	// answer sends a request about a provider and returns the provider that
	// the API answers with. No answer holds a key.
	answer := func(method, path, body string, status int) provider {
		t.Helper()
		var raw json.RawMessage
		if got, _ := s.request(method, path, body, &raw); got != status {
			t.Fatalf("%s %s was answered with %d %s", method, path, got, raw)
		}
		if strings.Contains(string(raw), first) || strings.Contains(string(raw), second) {
			t.Errorf("%s %s was answered with a key: %s", method, path, raw)
		}
		// Nor does it hold anything else of the key, such as what is stored.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for name, value := range fields {
			known := slices.Contains([]string{"id", "project_id", "name", "url", "api_key", "created_at", "updated_at", "code", "message"}, name)
			if !known || (name == "api_key" && string(value) != `{"stored":true}`) {
				t.Errorf("%s %s was answered with %s: %s", method, path, name, value)
			}
		}
		var decoded provider
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	// kept returns the key of a provider as the database holds it, and what
	// it opens to for that provider.
	kept := func(id string) ([]byte, string) {
		t.Helper()
		record, err := records.Provider(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		if record.APIKey == nil {
			return nil, ""
		}
		opened, err := aviary.keys.Open(record.APIKey, store.ProviderKeyPlace(record.ID))
		if err != nil {
			t.Fatalf("the key of the provider does not open: %v", err)
		}
		return record.APIKey, opened
	}

	project := s.create("/v1/projects", `{"name": "test"}`)
	providers := "/v1/projects/" + project + "/providers"
	created := answer("POST", providers,
		`{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "`+first+`"}}`, http.StatusCreated)
	if created.ProjectID != project || created.Name != "a provider" || created.URL != "https://api.example.com/v1" ||
		string(created.APIKey) != `{"stored":true}` || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("the provider was created as %+v", created)
	}
	path := "/v1/providers/" + created.ID
	if read := answer("GET", path, "", http.StatusOK); read.ID != created.ID || read.URL != created.URL || string(read.APIKey) != `{"stored":true}` {
		t.Errorf("the provider is read as %+v", read)
	}
	sealed, opened := kept(created.ID)
	if opened != first || bytes.Contains(sealed, []byte(first)) {
		t.Errorf("the database holds the key as %q, which opens to %q", sealed, opened)
	}

	// The key is sealed for its provider: that of another provider is not
	// read in its place, even if it is the same key.
	other := answer("POST", providers,
		`{"name": "another", "url": "http://model.example.com:8000", "api_key": {"value": "`+first+`"}}`, http.StatusCreated)
	moved, _ := kept(other.ID)
	if opened, err := aviary.keys.Open(moved, store.ProviderKeyPlace(uuid.MustParse(created.ID))); !errors.Is(err, secret.ErrSealed) {
		t.Errorf("the key of another provider opened for this one as %q, %v", opened, err)
	}

	// A provider is replaced whole: its name, its URL and its key.
	replaced := answer("PUT", path,
		`{"name": "renamed", "url": "https://other.example.com", "api_key": {"value": "`+second+`"}}`, http.StatusOK)
	if replaced.ID != created.ID || replaced.ProjectID != project || replaced.Name != "renamed" || replaced.URL != "https://other.example.com" ||
		string(replaced.APIKey) != `{"stored":true}` || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("the provider was replaced with %+v, and was %+v", replaced, created)
	}
	if read := answer("GET", path, "", http.StatusOK); read.Name != "renamed" || read.URL != "https://other.example.com" {
		t.Errorf("the replaced provider is read as %+v", read)
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("the key of the replaced provider opens to %q", opened)
	}
	// What cannot replace it leaves it as it is.
	answer("PUT", path, `{"name": "renamed", "url": "other.example.com", "api_key": {"value": "`+first+`"}}`, http.StatusBadRequest)
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("after a request that was refused, the key of the provider opens to %q", opened)
	}

	// A provider that is given no key has none, also if it had one.
	if keyless := answer("PUT", path, `{"name": "renamed", "url": "https://other.example.com"}`, http.StatusOK); keyless.APIKey != nil {
		t.Errorf("the provider that was given no key is %+v", keyless)
	}
	if sealed, _ := kept(created.ID); sealed != nil {
		t.Errorf("the database still holds a key of the provider: %q", sealed)
	}
	local := answer("POST", providers, `{"name": "local", "url": "http://10.0.0.5:8000/v1", "api_key": null}`, http.StatusCreated)
	if read := answer("GET", "/v1/providers/"+local.ID, "", http.StatusOK); read.APIKey != nil || read.URL != "http://10.0.0.5:8000/v1" {
		t.Errorf("the provider without a key is read as %+v", read)
	}
}
