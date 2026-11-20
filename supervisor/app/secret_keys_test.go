package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// in returns an api_key that refers to a value in a secret store.
func in(store, path, key string) string {
	return fmt.Sprintf(`{"store": %q, "path": %q, "key": %q}`, store, path, key)
}

// keyed returns the body of a provider with apiKey as its api_key.
func keyed(apiKey string) string {
	return `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": ` + apiKey + `}`
}

// A provider's key can be a reference to a secret store of its project.
// Aviary stores the reference, not the key, and accepts it only if the store
// returns a value for it.
func TestProviderWithItsKeyInASecretStore(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	const address, value = "https://" + storeHost, "sk-in-the-store"
	project, other := s.create("/v1/projects", `{"name": "test"}`), s.create("/v1/projects", `{"name": "another"}`)
	providers := "/v1/projects/" + project + "/providers"
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	theirs := s.create("/v1/projects/"+other+"/secret-stores", scriptedStore("theirs", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", value)
	// record returns the provider as stored.
	record := func(id string) store.Provider {
		t.Helper()
		record, err := aviary.records.Provider(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	// answer sends a request and returns the provider, the status and the
	// error message of the response. It fails the test if the response
	// reveals the key.
	answer := func(method, path, body string) (provider, int, string) {
		t.Helper()
		var raw json.RawMessage
		status, _ := s.request(method, path, body, &raw)
		if strings.Contains(string(raw), value) {
			t.Errorf("%s %s response contains the key: %s", method, path, raw)
		}
		var decoded provider
		var refused struct{ Message string }
		json.Unmarshal(raw, &decoded)
		json.Unmarshal(raw, &refused)
		return decoded, status, refused.Message
	}

	place := `{"store":"` + kept + `","path":"llm/model","key":"api_key"}`
	created, status, _ := answer("POST", providers, keyed(in(kept, "llm/model", "api_key")))
	if status != http.StatusCreated || string(created.APIKey) != place {
		t.Fatalf("create provider = %d %+v, api_key %s", status, created, created.APIKey)
	}
	path := "/v1/providers/" + created.ID
	if read, status, _ := answer("GET", path, ""); status != http.StatusOK || string(read.APIKey) != place {
		t.Errorf("GET provider = %d, api_key %s", status, read.APIKey)
	}
	if held := record(created.ID); held.APIKey != nil || held.APIKeyAt == nil ||
		*held.APIKeyAt != (store.SecretReference{Store: uuid.MustParse(kept), Path: "llm/model", Key: "api_key"}) {
		t.Errorf("stored provider: APIKey %q, APIKeyAt %+v", held.APIKey, held.APIKeyAt)
	}

	// Invalid references are refused with a reason, and leave the provider
	// unchanged.
	for name, refused := range map[string]struct{ apiKey, why string }{
		"store of another project": {in(theirs, "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"unknown store":            {in(uuid.NewString(), "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"invalid store ID":         {in("a store", "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"path without a value":     {in(kept, "llm/nothing", "api_key"), "cannot read api_key from the secret store: the store has no value at the path and under the key"},
		"key without a value":      {in(kept, "llm/model", "token"), "cannot read api_key from the secret store: the store has no value at the path and under the key"},
		"no path":                  {`{"store": "` + kept + `", "key": "api_key"}`, `{"store": "…", "path": "…", "key": "…"}`},
		"no key":                   {`{"store": "` + kept + `", "path": "llm/model"}`, `{"store": "…", "path": "…", "key": "…"}`},
		"value and reference":      {`{"value": "sk-a-key", "store": "` + kept + `", "path": "llm/model", "key": "api_key"}`, `{"value": "…"}`},
		"empty object":             {`{}`, `{"value": "…"}`},
	} {
		for _, request := range []struct{ method, path string }{{"POST", providers}, {"PUT", path}} {
			if _, status, why := answer(request.method, request.path, keyed(refused.apiKey)); status != http.StatusBadRequest || !strings.Contains(why, refused.why) {
				t.Errorf("%s provider with %s: %d %q, want 400 with the reason", request.method, name, status, why)
			}
		}
	}
	if read, _, _ := answer("GET", path, ""); string(read.APIKey) != place || !read.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("provider after refused requests = %+v, api_key %s", read, read.APIKey)
	}

	// PUT replaces the key: a stored key replaces a reference, and the other
	// way around.
	if replaced, status, _ := answer("PUT", path, keyed(`{"value": "sk-a-key-to-store"}`)); status != http.StatusOK || string(replaced.APIKey) != `{"stored":true}` {
		t.Errorf("PUT stored key = %d, api_key %s", status, replaced.APIKey)
	}
	if held := record(created.ID); held.APIKey == nil || held.APIKeyAt != nil {
		t.Errorf("stored provider: APIKey %q, APIKeyAt %+v", held.APIKey, held.APIKeyAt)
	}
	aviary.secrets.put(address, "llm/other", "key", "sk-another")
	if replaced, status, _ := answer("PUT", path, keyed(in(kept, "llm/other", "key"))); status != http.StatusOK ||
		string(replaced.APIKey) != `{"store":"`+kept+`","path":"llm/other","key":"key"}` {
		t.Errorf("PUT key reference = %d, api_key %s", status, replaced.APIKey)
	}
	if held := record(created.ID); held.APIKey != nil || held.APIKeyAt == nil || held.APIKeyAt.Path != "llm/other" {
		t.Errorf("stored provider: APIKey %q, APIKeyAt %+v", held.APIKey, held.APIKeyAt)
	}
}

// A key in a secret store is read when a model request needs it, and each
// Supervisor caches it. If the key cannot be read, the request is denied with
// the reason.
func TestModelRequestWithAKeyFromASecretStore(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	const address = "https://" + storeHost
	project := s.create("/v1/projects", `{"name": "test"}`)
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", "sk-the-first")
	provider := s.create("/v1/projects/"+project+"/providers", keyed(in(kept, "llm/model", "api_key")))
	agent := s.create("/v1/projects/"+project+"/agents", `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
	token := aviary.token(aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID).Sandbox)
	// told returns the Authorization header that a Supervisor sets on a
	// model request of the turn.
	told := func(s *supervisor) string {
		t.Helper()
		got := named(t, s.check(token, false, "/v1/chat/completions"))
		if got == nil || got.host != "api.example.com" || got.secure != "true" || got.keyless {
			t.Fatalf("model request endpoint = %+v", got)
		}
		return got.authorization
	}
	// refused returns the status and the reason with which a Supervisor
	// denies a model request of the turn.
	refused := func(s *supervisor) (int, string) {
		t.Helper()
		return refusal(s.check(token, false, "/v1/chat/completions"))
	}
	// elsewhere reports whether a forward proxy request of the turn passes
	// unchanged, without a key.
	elsewhere := func(s *supervisor) bool {
		t.Helper()
		return named(t, s.check(token, true, "/")) == nil
	}
	const reads = `aviary_secret_read_seconds_count{kind="scripted",outcome="read"}`

	if key := told(s); key != "Bearer sk-the-first" {
		t.Errorf("Authorization = %q, want the key from the store", key)
	}
	// The key is added to model requests only.
	if !elsewhere(s) {
		t.Error("forward proxy request was modified")
	}
	// The key was read once, when the provider was created, and stays cached
	// after the store changes.
	aviary.secrets.put(address, "llm/model", "api_key", "sk-the-second")
	if key := told(s); key != "Bearer sk-the-first" || s.metric(reads) != 1 {
		t.Errorf("Authorization = %q after %v store reads, want the cached key after 1", key, s.metric(reads))
	}
	// A Supervisor without the cached key reads the current one.
	if another := aviary.supervisor(); told(another) != "Bearer sk-the-second" || another.metric(reads) != 1 {
		t.Errorf("another Supervisor: %v store reads, want 1 with the current key", another.metric(reads))
	}

	// If the store has no value, model requests are denied with the reason,
	// and the other requests of the turn pass.
	aviary.secrets.put(address, "llm/model", "api_key", "")
	third := aviary.supervisor()
	if status, why := refused(third); status != http.StatusServiceUnavailable ||
		why != "cannot read the model provider's API key from its secret store: the store has no value at the path and under the key\n" {
		t.Errorf("request with a missing key: denied with %d %q", status, why)
	}
	if failed := third.metric(`aviary_secret_read_seconds_count{kind="scripted",outcome="failed"}`); failed != 1 {
		t.Errorf("failed reads = %v, want 1", failed)
	}
	if !elsewhere(third) {
		t.Error("forward proxy request was modified while the key cannot be read")
	}

	// Replacing the store invalidates the cache.
	aviary.secrets.put(address, "llm/model", "api_key", "sk-the-third")
	if status, _ := s.request("PUT", "/v1/secret-stores/"+kept, scriptedStore("a store", address, "another-credential", "another-credential"), nil); status != http.StatusOK {
		t.Fatalf("PUT secret store: status %d, want 200", status)
	}
	if key := told(s); key != "Bearer sk-the-third" {
		t.Errorf("Authorization after replacing the store = %q", key)
	}

	// A store at a host that is not allowed is not read. Nor is a store whose
	// credential was sealed with the keys of another installation, since the
	// credential does not open.
	closed := *aviary
	closed.storeHosts = nil
	if status, why := refused(closed.supervisor()); status != http.StatusServiceUnavailable ||
		!strings.Contains(why, "supervisor.secretStores.allowedHosts") {
		t.Errorf("request with a key in a disallowed store: denied with %d %q", status, why)
	}
	held, err := aviary.records.SecretStore(context.Background(), uuid.MustParse(kept))
	if err != nil {
		t.Fatal(err)
	}
	if held.Credential, err = testenv.Keys(t).Seal("another-credential", store.SecretStoreCredentialPlace(held.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := aviary.records.ReplaceSecretStore(context.Background(), held); err != nil {
		t.Fatal(err)
	}
	if status, why := refused(s); status != http.StatusServiceUnavailable ||
		why != "cannot read the credential of the model provider's secret store\n" {
		t.Errorf("request with a store credential that does not open: denied with %d %q", status, why)
	}
}
