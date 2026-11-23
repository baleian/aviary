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
	Headers   []header        `json:"headers"`
	APIKey    json.RawMessage `json:"api_key"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// header is a header of an endpoint as the API returns it.
type header struct {
	Name   string          `json:"name"`
	Value  string          `json:"value"`
	Secret json.RawMessage `json:"secret"`
}

// lines returns the headers each as a line, "name: value", or "name: " and
// the secret as the API shows it.
func lines(headers []header) string {
	var shown strings.Builder
	for _, header := range headers {
		if header.Secret != nil {
			shown.WriteString(header.Name + ": " + string(header.Secret) + "\n")
			continue
		}
		shown.WriteString(header.Name + ": " + header.Value + "\n")
	}
	return shown.String()
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

	// {"stored": true} keeps the stored key while the URL stays the same. For
	// another URL the key is given again, so that it goes to no other
	// endpoint.
	if same := answer("PUT", path, `{"name": "kept", "url": "https://other.example.com", "api_key": {"stored": true}}`, http.StatusOK); same.Name != "kept" ||
		string(same.APIKey) != `{"stored":true}` {
		t.Errorf("provider that keeps its key = %+v", same)
	}
	if _, opened := kept(created.ID); opened != second {
		t.Errorf("kept key opens to %q", opened)
	}
	var refused struct{ Message string }
	if status, _ := s.request("PUT", path, `{"name": "kept", "url": "https://third.example.com", "api_key": {"stored": true}}`, &refused); status != http.StatusBadRequest ||
		refused.Message != "api_key must be given again: the URL changed" {
		t.Errorf("PUT that keeps the key for another URL: %d %q", status, refused.Message)
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
	// A provider without a stored key has none to keep.
	if status, _ := s.request("PUT", "/v1/providers/"+local.ID, `{"name": "local", "url": "http://10.0.0.5:8000/v1", "api_key": {"stored": true}}`, &refused); status != http.StatusBadRequest ||
		refused.Message != "api_key has no stored secret to keep" {
		t.Errorf("PUT that keeps a key that is not stored: %d %q", status, refused.Message)
	}
}

// The values of the headers of a provider are not secrets: a provider is
// returned with them, as they are kept. PUT replaces them with the rest of
// the provider.
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
	const given = "x-gateway-route: team-a\naccept-language: en\n"
	status, created, _ := answer("POST", providers, `{"name": "a gateway", "url": "https://gateway.example.com/v1",
		"headers": [{"name": "X-Gateway-Route", "value": " team-a "}, {"name": "accept-language", "value": "en"}]}`)
	if status != http.StatusCreated || lines(created.Headers) != given {
		t.Fatalf("created provider: status %d, headers %q, want %q", status, lines(created.Headers), given)
	}
	path := "/v1/providers/" + created.ID
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || lines(read.Headers) != given {
		t.Errorf("provider: status %d, headers %q, want %q", status, lines(read.Headers), given)
	}
	var list struct{ Providers []provider }
	if status, _ := s.request("GET", providers, "", &list); status != http.StatusOK || len(list.Providers) != 1 || lines(list.Providers[0].Headers) != given {
		t.Errorf("providers: status %d, %+v", status, list.Providers)
	}

	// A refused PUT says which header it refuses, and leaves the provider
	// unchanged. A header is a name and a value or a secret, and nothing
	// else.
	const gateway = `{"name": "a gateway", "url": "https://gateway.example.com/v1"`
	status, _, why := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}, {"name": "x-aviary-upstream", "value": "other.example.com"}]}`)
	if status != http.StatusBadRequest || why != "headers[1].name must not be x-aviary-upstream, which is reserved" {
		t.Errorf("PUT with a reserved header: status %d, %q", status, why)
	}
	if status, _, _ := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b", "secret": true}]}`); status != http.StatusBadRequest {
		t.Errorf("PUT with a header that has another field: status %d, want 400", status)
	}
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || lines(read.Headers) != given {
		t.Errorf("provider after refused requests: status %d, headers %q, want %q", status, lines(read.Headers), given)
	}

	// PUT replaces the headers, and a PUT without headers removes them.
	if status, replaced, _ := answer("PUT", path, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}]}`); status != http.StatusOK ||
		lines(replaced.Headers) != "x-gateway-route: team-b\n" {
		t.Errorf("replaced provider: status %d, headers %q", status, lines(replaced.Headers))
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

// The value of a header can be a secret: one that Aviary stores, sealed for
// its provider and its name, or one in a secret store of the project. A
// provider is returned with the secret as stored or by its location. A stored
// secret stays while the URL does, so that it goes to no other endpoint.
func TestProviderHeaderSecrets(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
	providers := "/v1/projects/" + project + "/providers"
	const address, secret, inStore = "https://" + storeHost, "s3cret-of-the-header", "t0ken-in-the-store"
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	theirs := s.create("/v1/projects/"+other+"/secret-stores", scriptedStore("theirs", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "team/gateway", "token", inStore)
	// answer sends a request, and returns the status of the response and the
	// headers of the provider in it, or its message. It fails the test if the
	// response reveals a secret.
	answer := func(method, path, body string) (int, string, string) {
		t.Helper()
		var raw json.RawMessage
		status, _ := s.request(method, path, body, &raw)
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), inStore) || strings.Contains(string(raw), "sealed") {
			t.Errorf("%s %s response reveals a secret: %s", method, path, raw)
		}
		var got struct {
			provider
			Message string
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return status, lines(got.Headers), got.Message
	}
	// stored returns the header of the provider with the name, as stored.
	stored := func(id, name string) store.Header {
		t.Helper()
		record, err := aviary.records.Provider(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		for _, header := range record.Headers {
			if header.Name == name {
				return header
			}
		}
		t.Fatalf("provider %s has no header %s", id, name)
		return store.Header{}
	}
	// opened returns the stored secret of the header, opened.
	opened := func(id, name string) string {
		t.Helper()
		header := stored(id, name)
		value, err := aviary.keys.Open(header.Sealed, store.ProviderHeaderPlace(uuid.MustParse(id), name))
		if err != nil || header.Value != "" || header.At != nil {
			t.Fatalf("header %s of provider %s = %+v, opens: %v", name, id, header, err)
		}
		return value
	}

	const gateway = `{"name": "a gateway", "url": "https://gateway.example.com/v1", "headers": `
	created := s.create(providers, gateway+`[{"name": "X-Token", "secret": {"value": " `+secret+` "}},
		{"name": "x-team-token", "secret": `+in(kept, "team/gateway", "token")+`}, {"name": "x-team", "value": "a"}]}`)
	path := "/v1/providers/" + created
	want := `x-token: {"stored":true}` + "\n" + `x-team-token: {"store":"` + kept + `","path":"team/gateway","key":"token"}` + "\nx-team: a\n"
	if status, headers, _ := answer("GET", path, ""); status != http.StatusOK || headers != want {
		t.Fatalf("provider: %d, headers %q, want %q", status, headers, want)
	}
	if value := opened(created, "x-token"); value != secret {
		t.Errorf("stored secret opens to %q", value)
	}
	if at := stored(created, "x-team-token").At; at == nil || *at != (store.SecretReference{Store: uuid.MustParse(kept), Path: "team/gateway", Key: "token"}) {
		t.Errorf("header in a store: %+v", at)
	}
	// A secret is sealed for its header: it does not open for another name.
	if value, err := aviary.keys.Open(stored(created, "x-token").Sealed, store.ProviderHeaderPlace(uuid.MustParse(created), "x-other")); err == nil {
		t.Errorf("secret opened for another header as %q", value)
	}

	// {"stored": true} keeps the stored secret of the header with the same
	// name while the URL stays the same.
	sealed := stored(created, "x-token").Sealed
	if status, headers, why := answer("PUT", path, gateway+`[{"name": "x-token", "secret": {"stored": true}}]}`); status != http.StatusOK ||
		headers != `x-token: {"stored":true}`+"\n" {
		t.Errorf("PUT that keeps the secret: %d %q, headers %q", status, why, headers)
	}
	if !bytes.Equal(stored(created, "x-token").Sealed, sealed) {
		t.Error("the kept secret was sealed again")
	}
	for name, refused := range map[string]struct{ body, why string }{
		"another URL": {`{"name": "a gateway", "url": "https://other.example.com/v1", "headers": [{"name": "x-token", "secret": {"stored": true}}]}`,
			"headers[0].secret must be given again: the URL changed"},
		"another name":                 {gateway + `[{"name": "x-other", "secret": {"stored": true}}]}`, "headers[0].secret has no stored secret to keep"},
		"a value and a secret":         {gateway + `[{"name": "x-token", "value": "a", "secret": {"stored": true}}]}`, "headers[0] must have a value or a secret"},
		"a secret of two kinds":        {gateway + `[{"name": "x-token", "secret": {"value": "a", "stored": true}}]}`, `headers[0].secret must be {"value": "…"}`},
		"an empty secret":              {gateway + `[{"name": "x-token", "secret": {}}]}`, `headers[0].secret must be {"value": "…"}`},
		"a store of another project":   {gateway + `[{"name": "x-token", "secret": ` + in(theirs, "team/gateway", "token") + `}]}`, "headers[0].secret.store must be the ID of a secret store of the project"},
		"a path that the store lacks":  {gateway + `[{"name": "x-token", "secret": ` + in(kept, "team/nothing", "token") + `}]}`, "cannot read headers[0].secret from the secret store: the store has no value at the path and under the key"},
		"a secret that breaks a line":  {gateway + `[{"name": "x-token", "secret": {"value": "a\r\nx-other: b"}}]}`, "headers[0].secret.value must be the value of an HTTP header"},
		"a secret for a reserved name": {gateway + `[{"name": "host", "secret": {"value": "a"}}]}`, "headers[0].name must not be host, which is reserved"},
	} {
		if status, _, why := answer("PUT", path, refused.body); status != http.StatusBadRequest || !strings.HasPrefix(why, refused.why) {
			t.Errorf("PUT with %s: %d %q, want 400 %q", name, status, why, refused.why)
		}
	}
	if status, headers, _ := answer("GET", path, ""); status != http.StatusOK || headers != `x-token: {"stored":true}`+"\n" {
		t.Errorf("provider after refused requests: %d, headers %q", status, headers)
	}

	// A secret is replaced by another, and by a value that is not one.
	answer("PUT", path, gateway+`[{"name": "x-token", "secret": {"value": "another"}}]}`)
	if value := opened(created, "x-token"); value != "another" {
		t.Errorf("replaced secret opens to %q", value)
	}
	if status, headers, _ := answer("PUT", path, gateway+`[{"name": "x-token", "value": "plain"}]}`); status != http.StatusOK || headers != "x-token: plain\n" {
		t.Errorf("PUT with a value in place of the secret: %d, headers %q", status, headers)
	}
	if header := stored(created, "x-token"); header.Sealed != nil || header.Value != "plain" {
		t.Errorf("header after its secret was replaced by a value = %+v", header)
	}
}
