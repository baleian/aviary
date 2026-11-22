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

// provider is a model provider as the API returns it.
type provider struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	Headers   []store.Header  `json:"headers"`
	APIKey    json.RawMessage `json:"api_key"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// The API key of a provider is write-only: no response contains it, and the
// database holds it sealed.
func TestProviders(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	records := aviary.records
	const first, second = "sk-the-first-key", "sk-the-second-key"
	// answer sends a request and returns the provider in the response. It
	// fails the test if the response reveals a key.
	answer := func(method, path, body string, status int) provider {
		t.Helper()
		var raw json.RawMessage
		if got, _ := s.request(method, path, body, &raw); got != status {
			t.Fatalf("%s %s: unexpected status %d %s", method, path, got, raw)
		}
		if strings.Contains(string(raw), first) || strings.Contains(string(raw), second) {
			t.Errorf("%s %s response contains the key: %s", method, path, raw)
		}
		// Nor does it hold anything else of the key, such as the sealed key.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for name, value := range fields {
			known := slices.Contains([]string{"id", "project_id", "name", "url", "api_key", "created_at", "updated_at", "code", "message"}, name)
			if !known || (name == "api_key" && string(value) != `{"stored":true}`) {
				t.Errorf("%s %s response has field %s: %s", method, path, name, value)
			}
		}
		var decoded provider
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	// kept returns the provider's key as stored and as opened.
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
			t.Fatalf("open provider key: %v", err)
		}
		return record.APIKey, opened
	}

	project := s.project()
	providers := "/v1/projects/" + project + "/providers"
	created := answer("POST", providers,
		`{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "`+first+`"}}`, http.StatusCreated)
	if created.ProjectID != project || created.Name != "a provider" || created.URL != "https://api.example.com/v1" ||
		string(created.APIKey) != `{"stored":true}` || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("created provider = %+v", created)
	}
	path := "/v1/providers/" + created.ID
	if read := answer("GET", path, "", http.StatusOK); read.ID != created.ID || read.URL != created.URL || string(read.APIKey) != `{"stored":true}` {
		t.Errorf("provider = %+v", read)
	}
	sealed, opened := kept(created.ID)
	if opened != first || bytes.Contains(sealed, []byte(first)) {
		t.Errorf("stored key %q opens to %q", sealed, opened)
	}

	// A key is sealed for its provider: the stored key of another provider
	// does not open for this one, even if the value is the same.
	other := answer("POST", providers,
		`{"name": "another", "url": "http://model.example.com:8000", "api_key": {"value": "`+first+`"}}`, http.StatusCreated)
	moved, _ := kept(other.ID)
	if opened, err := aviary.keys.Open(moved, store.ProviderKeyPlace(uuid.MustParse(created.ID))); !errors.Is(err, secret.ErrSealed) {
		t.Errorf("key of another provider opened as %q, %v; want ErrSealed", opened, err)
	}

	// PUT replaces the name, the URL and the key.
	replaced := answer("PUT", path,
		`{"name": "renamed", "url": "https://other.example.com", "api_key": {"value": "`+second+`"}}`, http.StatusOK)
	if replaced.ID != created.ID || replaced.ProjectID != project || replaced.Name != "renamed" || replaced.URL != "https://other.example.com" ||
		string(replaced.APIKey) != `{"stored":true}` || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("replaced provider = %+v, was %+v", replaced, created)
	}
	if read := answer("GET", path, "", http.StatusOK); read.Name != "renamed" || read.URL != "https://other.example.com" {
		t.Errorf("replaced provider = %+v", read)
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("replaced key opens to %q", opened)
	}
	// A refused PUT leaves the provider unchanged.
	answer("PUT", path, `{"name": "renamed", "url": "other.example.com", "api_key": {"value": "`+first+`"}}`, http.StatusBadRequest)
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("key after a refused request opens to %q", opened)
	}

	// A PUT without a key removes the stored key.
	if keyless := answer("PUT", path, `{"name": "renamed", "url": "https://other.example.com"}`, http.StatusOK); keyless.APIKey != nil {
		t.Errorf("provider without a key = %+v", keyless)
	}
	if sealed, _ := kept(created.ID); sealed != nil {
		t.Errorf("stored key not removed: %q", sealed)
	}
	local := answer("POST", providers, `{"name": "local", "url": "http://10.0.0.5:8000/v1", "api_key": null}`, http.StatusCreated)
	if read := answer("GET", "/v1/providers/"+local.ID, "", http.StatusOK); read.APIKey != nil || read.URL != "http://10.0.0.5:8000/v1" {
		t.Errorf("provider without a key = %+v", read)
	}
}

// The headers of a provider are not secrets: a provider is returned with
// them, as they are kept. PUT replaces them with the rest of the provider.
func TestProviderHeaders(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	providers := "/v1/projects/" + project + "/providers"
	// answer sends a request, and returns the status of the response and the
	// provider or the message in it.
	answer := func(method, path, body string) (int, provider, string) {
		t.Helper()
		var got struct {
			provider
			Message string
		}
		status, _ := s.request(method, path, body, &got)
		return status, got.provider, got.Message
	}

	// Names are kept in lowercase, and values without spaces at their ends,
	// in the order in which they are given.
	given := []store.Header{{Name: "x-gateway-route", Value: "team-a"}, {Name: "accept-language", Value: "en"}}
	status, created, _ := answer("POST", providers, `{"name": "a gateway", "url": "https://gateway.example.com/v1",
		"headers": [{"name": "X-Gateway-Route", "value": " team-a "}, {"name": "accept-language", "value": "en"}]}`)
	if status != http.StatusCreated || !slices.Equal(created.Headers, given) {
		t.Fatalf("created provider: status %d, headers %v, want %v", status, created.Headers, given)
	}
	path := "/v1/providers/" + created.ID
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || !slices.Equal(read.Headers, given) {
		t.Errorf("provider: status %d, headers %v, want %v", status, read.Headers, given)
	}
	var list struct{ Providers []provider }
	if status, _ := s.request("GET", providers, "", &list); status != http.StatusOK || len(list.Providers) != 1 || !slices.Equal(list.Providers[0].Headers, given) {
		t.Errorf("providers: status %d, %+v", status, list.Providers)
	}

	// A refused PUT says which header it refuses, and leaves the provider
	// unchanged. A header is a name and a value, and nothing else.
	const gateway = `{"name": "a gateway", "url": "https://gateway.example.com/v1"`
	status, _, why := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}, {"name": "x-aviary-upstream", "value": "other.example.com"}]}`)
	if status != http.StatusBadRequest || why != "headers[1].name must not be x-aviary-upstream, which is reserved" {
		t.Errorf("PUT with a reserved header: status %d, %q", status, why)
	}
	if status, _, _ := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b", "secret": true}]}`); status != http.StatusBadRequest {
		t.Errorf("PUT with a header that has another field: status %d, want 400", status)
	}
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || !slices.Equal(read.Headers, given) {
		t.Errorf("provider after refused requests: status %d, headers %v, want %v", status, read.Headers, given)
	}

	// PUT replaces the headers, and a PUT without headers removes them.
	if status, replaced, _ := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}]}`); status != http.StatusOK ||
		!slices.Equal(replaced.Headers, []store.Header{{Name: "x-gateway-route", Value: "team-b"}}) {
		t.Errorf("replaced provider: status %d, headers %v", status, replaced.Headers)
	}
	for _, body := range []string{gateway + `}`, gateway + `, "headers": []}`, gateway + `, "headers": null}`} {
		var fields map[string]json.RawMessage
		if status, _ := s.request("PUT", path, body, &fields); status != http.StatusOK || fields["headers"] != nil {
			t.Errorf("PUT %s: status %d, headers %s, want a provider without headers", body, status, fields["headers"])
		}
	}
	if record, err := aviary.records.Provider(context.Background(), uuid.MustParse(created.ID)); err != nil || record.Headers != nil {
		t.Errorf("stored headers = %v, %v, want none", record.Headers, err)
	}
}
