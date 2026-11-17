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

// in returns the api_key of a provider whose key is in a secret store.
func in(store, path, key string) string {
	return fmt.Sprintf(`{"store": %q, "path": %q, "key": %q}`, store, path, key)
}

// keyed returns the body of a request that gives a provider with the
// api_key.
func keyed(apiKey string) string {
	return `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": ` + apiKey + `}`
}

// A provider keeps its key in a secret store of its project. Aviary stores
// where the key is and not the key, and takes a place only if the store
// gives a value for it.
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
	// record returns the provider as the database holds it.
	record := func(id string) store.Provider {
		t.Helper()
		record, err := aviary.records.Provider(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	// answer sends a request about a provider and returns the provider that
	// the API answers with. No answer holds the key.
	answer := func(method, path, body string) (provider, int, string) {
		t.Helper()
		var raw json.RawMessage
		status, _ := s.request(method, path, body, &raw)
		if strings.Contains(string(raw), value) {
			t.Errorf("%s %s was answered with the key: %s", method, path, raw)
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
		t.Fatalf("the provider whose key is in a secret store was created with %d, as %+v with the key %s", status, created, created.APIKey)
	}
	path := "/v1/providers/" + created.ID
	if read, status, _ := answer("GET", path, ""); status != http.StatusOK || string(read.APIKey) != place {
		t.Errorf("the provider is read with %d, with the key %s", status, read.APIKey)
	}
	if held := record(created.ID); held.APIKey != nil || held.APIKeyAt == nil ||
		*held.APIKeyAt != (store.SecretReference{Store: uuid.MustParse(kept), Path: "llm/model", Key: "api_key"}) {
		t.Errorf("the database holds the provider with the key %q, at %+v", held.APIKey, held.APIKeyAt)
	}

	// What is no place of a key in a secret store of the project. A refusal
	// tells why, and leaves the provider as it is.
	for name, refused := range map[string]struct{ apiKey, why string }{
		"a store of another project": {in(theirs, "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"a store that is not there":  {in(uuid.NewString(), "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"a store that is no ID":      {in("a store", "llm/model", "api_key"), "api_key.store must be the ID of a secret store of the project"},
		"a path without a value":     {in(kept, "llm/nothing", "api_key"), "api_key cannot be read from the secret store: the store has no value at the path and under the key"},
		"a key without a value":      {in(kept, "llm/model", "token"), "api_key cannot be read from the secret store: the store has no value at the path and under the key"},
		"no path":                    {`{"store": "` + kept + `", "key": "api_key"}`, `{"store": "…", "path": "…", "key": "…"}`},
		"no key":                     {`{"store": "` + kept + `", "path": "llm/model"}`, `{"store": "…", "path": "…", "key": "…"}`},
		"a value and a place":        {`{"value": "sk-a-key", "store": "` + kept + `", "path": "llm/model", "key": "api_key"}`, `{"value": "…"}`},
		"neither":                    {`{}`, `{"value": "…"}`},
	} {
		for _, request := range []struct{ method, path string }{{"POST", providers}, {"PUT", path}} {
			if _, status, why := answer(request.method, request.path, keyed(refused.apiKey)); status != http.StatusBadRequest || !strings.Contains(why, refused.why) {
				t.Errorf("%s of a provider with %s as its key was answered with %d %q", request.method, name, status, why)
			}
		}
	}
	if read, _, _ := answer("GET", path, ""); string(read.APIKey) != place || !read.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("after the requests that were refused, the provider is %+v with the key %s", read, read.APIKey)
	}

	// A provider is replaced whole: a key that is stored takes the place of
	// the one in the store, and the other way around.
	if replaced, status, _ := answer("PUT", path, keyed(`{"value": "sk-a-key-to-store"}`)); status != http.StatusOK || string(replaced.APIKey) != `{"stored":true}` {
		t.Errorf("the provider that was given a key to store was answered with %d, with the key %s", status, replaced.APIKey)
	}
	if held := record(created.ID); held.APIKey == nil || held.APIKeyAt != nil {
		t.Errorf("the database holds the provider with the key %q, at %+v", held.APIKey, held.APIKeyAt)
	}
	aviary.secrets.put(address, "llm/other", "key", "sk-another")
	if replaced, status, _ := answer("PUT", path, keyed(in(kept, "llm/other", "key"))); status != http.StatusOK ||
		string(replaced.APIKey) != `{"store":"`+kept+`","path":"llm/other","key":"key"}` {
		t.Errorf("the provider that was given a key in a secret store was answered with %d, with the key %s", status, replaced.APIKey)
	}
	if held := record(created.ID); held.APIKey != nil || held.APIKeyAt == nil || held.APIKeyAt.Path != "llm/other" {
		t.Errorf("the database holds the provider with the key %q, at %+v", held.APIKey, held.APIKeyAt)
	}
}

// The key of a provider that is in a secret store is read from the store
// when a model request comes. Each Supervisor remembers it for a while, and
// a request whose key cannot be read is refused with the reason.
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
	job := aviary.job(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
	s.take(job)
	// told returns the key that a Supervisor tells the proxy to send a model
	// request of the turn with.
	told := func(s *supervisor) string {
		t.Helper()
		got := named(t, s.check(token(job), false, "/v1/chat/completions"))
		if got == nil || got.host != "api.example.com" || got.secure != "true" || got.keyless {
			t.Fatalf("the proxy is told to pass a model request on to %+v", got)
		}
		return got.authorization
	}
	const reads = `aviary_secret_read_seconds_count{kind="scripted",outcome="read"}`

	if key := told(s); key != "Bearer sk-the-first" {
		t.Errorf("the proxy is told to send the model request with %q", key)
	}
	// The key of a provider is for its endpoint alone.
	if got := named(t, s.check(token(job), true, "/")); got != nil {
		t.Errorf("about a request through the forward proxy, the proxy is told %+v", got)
	}
	// The Supervisor read the key once, when the provider was made, and
	// remembers it: also when the store has another by now.
	aviary.secrets.put(address, "llm/model", "api_key", "sk-the-second")
	if key := told(s); key != "Bearer sk-the-first" || s.metric(reads) != 1 {
		t.Errorf("the proxy is told to send the model request with %q, after %v reads of the store", key, s.metric(reads))
	}
	// A Supervisor that does not remember it reads what the store has now.
	if another := aviary.supervisor(); told(another) != "Bearer sk-the-second" || another.metric(reads) != 1 {
		t.Errorf("another Supervisor read the store %v times", another.metric(reads))
	}

	// A key that the store does not give: the request is refused with the
	// reason, and the other requests of the turn go on.
	aviary.secrets.put(address, "llm/model", "api_key", "")
	third := aviary.supervisor()
	status, why := refusal(third.check(token(job), false, "/v1/chat/completions"))
	if status != http.StatusServiceUnavailable ||
		why != "the API key of the model provider cannot be read from its secret store: the store has no value at the path and under the key\n" {
		t.Errorf("a model request whose key the store does not give is refused with %d %q", status, why)
	}
	if failed := third.metric(`aviary_secret_read_seconds_count{kind="scripted",outcome="failed"}`); failed != 1 {
		t.Errorf("the Supervisor counted %v reads that failed", failed)
	}
	if got := named(t, third.check(token(job), true, "/")); got != nil {
		t.Errorf("about a request through the forward proxy, the proxy is told %+v", got)
	}

	// A store that is registered anew is read anew: what a Supervisor
	// remembers is of the store as it was.
	aviary.secrets.put(address, "llm/model", "api_key", "sk-the-third")
	if status, _ := s.request("PUT", "/v1/secret-stores/"+kept, scriptedStore("a store", address, "another-credential", "another-credential"), nil); status != http.StatusOK {
		t.Fatalf("the secret store was replaced with %d", status)
	}
	if key := told(s); key != "Bearer sk-the-third" {
		t.Errorf("after the secret store was replaced, the proxy is told to send the model request with %q", key)
	}

	// An installation that allows no store at that host any more reads none
	// there, and one with other keys cannot log in to it.
	closed := *aviary
	closed.storeHosts = nil
	if status, why := refusal(closed.supervisor().check(token(job), false, "/v1/chat/completions")); status != http.StatusServiceUnavailable ||
		!strings.Contains(why, "supervisor.secretStores.allowedHosts") {
		t.Errorf("a model request whose key is in a store that is not allowed is refused with %d %q", status, why)
	}
	stranger := *aviary
	stranger.keys = testenv.Keys(t)
	if status, why := refusal(stranger.supervisor().check(token(job), false, "/v1/chat/completions")); status != http.StatusServiceUnavailable ||
		why != "the credential of the secret store of the model provider cannot be read\n" {
		t.Errorf("a model request whose store cannot be logged in to is refused with %d %q", status, why)
	}
}
