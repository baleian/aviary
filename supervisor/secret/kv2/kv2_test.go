package kv2

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// of returns the settings of a store.
func of(address, mount, roleID string) json.RawMessage {
	settings, _ := json.Marshal(map[string]string{"address": address, "mount": mount, "role_id": roleID})
	return settings
}

func TestSettings(t *testing.T) {
	t.Parallel()
	const address = "https://store.example.com:8200"
	for _, good := range []json.RawMessage{
		of(address, "secret", "a-role"),
		of(address+"/", "team/secrets", "a-role"),
		of("http://10.0.0.5:8200", "secret", "a-role"),
	} {
		if got, err := (Plugin{}).Address(good); err != nil || got != strings.TrimSuffix(field(good, "address"), "/") {
			t.Errorf("the store with the settings %s is at %q: %v", good, got, err)
		}
	}
	for _, bad := range []json.RawMessage{
		nil,
		json.RawMessage(`"https://store.example.com:8200"`),
		json.RawMessage(`{"address": "https://store.example.com:8200", "mount": "secret", "role_id": "a-role", "token": "a-token"}`),
		of("store.example.com:8200", "secret", "a-role"),
		of("ftp://store.example.com", "secret", "a-role"),
		of("https://", "secret", "a-role"),
		of("https://user:password@store.example.com:8200", "secret", "a-role"),
		of(address+"/v1", "secret", "a-role"),
		of(address+"?namespace=a", "secret", "a-role"),
		of(address+"?", "secret", "a-role"),
		of(address+"#part", "secret", "a-role"),
		of(address, "", "a-role"),
		of(address, "/secret", "a-role"),
		of(address, "secret/", "a-role"),
		of(address, "secret", ""),
	} {
		if got, err := (Plugin{}).Address(bad); err == nil {
			t.Errorf("the store with the settings %s is at %q", bad, got)
		}
	}
}

// field returns a string of a JSON object.
func field(object json.RawMessage, name string) string {
	var fields map[string]string
	json.Unmarshal(object, &fields)
	return fields[name]
}

// What the plugin asks a store to log in, and what it makes of the answers.
func TestLoginIsAsked(t *testing.T) {
	t.Parallel()
	const roleID, secretID = "the-role-id", "the-secret-id"
	// answering returns the settings of a store that answers a login with
	// the status and the body.
	answering := func(status int, body string) json.RawMessage {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var sent map[string]string
			if raw, _ := io.ReadAll(r.Body); json.Unmarshal(raw, &sent) != nil ||
				r.Method != http.MethodPost || r.URL.Path != "/v1/auth/approle/login" || sent["role_id"] != roleID || sent["secret_id"] != secretID {
				t.Errorf("the store was asked %s %s with %v", r.Method, r.URL.Path, sent)
			}
			w.WriteHeader(status)
			io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		return of(server.URL+"/", "secret", roleID)
	}
	ctx := context.Background()

	session, good, err := (Plugin{}).Login(ctx, http.DefaultClient, answering(http.StatusOK, `{"auth": {"client_token": "the-token", "lease_duration": 3600}}`), secretID)
	if err != nil || session != "the-token" || good != time.Hour {
		t.Errorf("the login gave %v, good for %s: %v", session, good, err)
	}
	// What a store says beside its status is not passed on.
	for _, refusing := range []struct {
		store json.RawMessage
		why   string
	}{
		{answering(http.StatusBadRequest, `{"errors": ["invalid role or secret ID: the-detail"]}`), "the status 400"},
		{answering(http.StatusForbidden, "the-detail"), "the status 403"},
		{answering(http.StatusOK, `{"auth": {"client_token": "", "the-detail": true}}`), "no token"},
		{answering(http.StatusOK, "the-detail"), "no token"},
		{of("http://127.0.0.1:1", "secret", roleID), "was not reached"},
		{of("http://127.0.0.1:1", "", roleID), "mount is required"},
	} {
		session, _, err := (Plugin{}).Login(ctx, http.DefaultClient, refusing.store, secretID)
		if err == nil || session != nil || !strings.Contains(err.Error(), refusing.why) || strings.Contains(err.Error(), "the-detail") || strings.Contains(err.Error(), secretID) {
			t.Errorf("a store that answers the login with %s gave %v: %v", refusing.why, session, err)
		}
	}
}

// rootToken is what the secret store of the tests is set up with, as its
// owner sets it up.
const rootToken = "test"

// owner sends the secret store of the tests a request as its owner does and
// returns the data of the answer.
func owner(t *testing.T, method, path string, body any, statuses ...int) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, testenv.SecretStoreURL(t)+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Vault-Token", rootToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("the secret store of the tests: %v", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	allowed := append(statuses, http.StatusOK, http.StatusNoContent)
	for _, status := range allowed {
		if response.StatusCode == status {
			var answer struct{ Data map[string]any }
			json.Unmarshal(raw, &answer)
			return answer.Data
		}
	}
	t.Fatalf("the secret store of the tests answered %s %s with %d %s", method, path, response.StatusCode, raw)
	return nil
}

// role gives the secret store of the tests a role of the test's own, as the
// owner of a store does for Aviary: one that may read the secrets below a
// path of its own. It returns the IDs that the role logs in with and that
// path.
func role(t *testing.T) (roleID, secretID, path string) {
	t.Helper()
	// Several tests ask for the login, and one of them is the first.
	owner(t, "POST", "/v1/sys/auth/approle", map[string]string{"type": "approle"}, http.StatusBadRequest)
	name := "test-" + strings.ToLower(rand.Text())
	owner(t, "PUT", "/v1/sys/policies/acl/"+name, map[string]string{
		"policy": fmt.Sprintf(`path "secret/data/%s/*" { capabilities = ["read"] }`, name),
	})
	owner(t, "POST", "/v1/auth/approle/role/"+name, map[string]string{"token_policies": name, "token_ttl": "1h"})
	roleID, _ = owner(t, "GET", "/v1/auth/approle/role/"+name+"/role-id", nil)["role_id"].(string)
	secretID, _ = owner(t, "POST", "/v1/auth/approle/role/"+name+"/secret-id", nil)["secret_id"].(string)
	if roleID == "" || secretID == "" {
		t.Fatal("the secret store of the tests gave the role no IDs")
	}
	return roleID, secretID, name
}

// A store that serves the API takes the login of the plugin, as the
// Supervisor asks it: with the stores of an installation.
func TestLoginToAStore(t *testing.T) {
	t.Parallel()
	address := testenv.SecretStoreURL(t)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := secret.NewStores([]string{parsed.Host}, Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	roleID, secretID, _ := role(t)
	other, another, _ := role(t)
	ctx := context.Background()

	if err := stores.Check(ctx, Kind, of(address, "secret", roleID), secretID); err != nil {
		t.Errorf("the store does not take the login of its role: %v", err)
	}
	session, good, err := (Plugin{}).Login(ctx, http.DefaultClient, of(address, "secret", other), another)
	if token, _ := session.(string); err != nil || token == "" || good != time.Hour {
		t.Errorf("the login gave %v, good for %s: %v", session, good, err)
	}
	for name, refused := range map[string]struct{ roleID, secretID string }{
		"the secret ID of another role": {roleID, another},
		"a secret ID of no role":        {roleID, "not-a-secret-id"},
		"a role that is not there":      {"not-a-role", secretID},
	} {
		err := stores.Check(ctx, Kind, of(address, "secret", refused.roleID), refused.secretID)
		if err == nil || !strings.Contains(err.Error(), "does not take the login: the store answered the login with the status 400") {
			t.Errorf("the store is checked with %s as: %v", name, err)
		}
	}
}

// A store that serves the API is read as the Supervisor reads it, with the
// stores of an installation: the value under a key of a secret that the
// role of the store may read.
func TestReadFromAStore(t *testing.T) {
	t.Parallel()
	address := testenv.SecretStoreURL(t)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := secret.NewStores([]string{parsed.Host}, Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	roleID, secretID, own := role(t)
	_, _, theirs := role(t)
	ctx := context.Background()
	// put keeps values at a path of the store, as its owner does.
	put := func(path string, values map[string]any) {
		t.Helper()
		owner(t, "POST", "/v1/secret/data/"+path, map[string]any{"data": values})
	}
	put(own+"/model", map[string]any{"api_key": "sk-the-first", "retries": 3})
	put(own+"/a team/model one", map[string]any{"api_key": "sk-of-a-path-with-spaces"})
	put(theirs+"/model", map[string]any{"api_key": "sk-of-another-role"})
	registered := store.SecretStore{ID: uuid.New(), Kind: Kind, Settings: of(address, "secret", roleID), UpdatedAt: time.Now()}

	for path, value := range map[string]string{own + "/model": "sk-the-first", own + "/a team/model one": "sk-of-a-path-with-spaces"} {
		if read, err := stores.Read(ctx, registered, secretID, path, "api_key"); err != nil || read != value {
			t.Errorf("the value at %s was read as %q: %v", path, read, err)
		}
	}
	// What the store does not give, and why. No reason holds what the store
	// said beside its status.
	for name, refused := range map[string]struct{ path, key, why string }{
		"a path without a secret":           {own + "/nothing", "api_key", "the store has no secret at the path"},
		"a path that the role may not read": {theirs + "/model", "api_key", "the store does not let the role read the secret at the path"},
		"a key that the secret has not":     {own + "/model", "token", "the secret at the path holds no value under the key"},
		"a value that is no text":           {own + "/model", "retries", "the value under the key is not a text"},
		"no key":                            {own + "/model", "", "key is required"},
		"no path":                           {"", "api_key", "path must be the path of a secret"},
		"a path that begins with a slash":   {"/" + own + "/model", "api_key", "path must be the path of a secret"},
		"a path with a step back":           {own + "/../" + theirs + "/model", "api_key", "path must be the path of a secret"},
	} {
		read, err := stores.Read(ctx, registered, secretID, refused.path, refused.key)
		if err == nil || read != "" || !strings.Contains(err.Error(), refused.why) || strings.Contains(err.Error(), "sk-") {
			t.Errorf("%s was read as %q: %v", name, read, err)
		}
	}
	// A store that does not take the login gives nothing.
	wrong := registered
	wrong.ID = uuid.New()
	if read, err := stores.Read(ctx, wrong, "not-the-secret-id", own+"/model", "api_key"); err == nil || read != "" || !strings.Contains(err.Error(), "does not take the login") {
		t.Errorf("a store that was logged in to with another secret ID was read as %q: %v", read, err)
	}

	// The plugin reads what the store has now: the Supervisor asks it again
	// when it no longer remembers.
	session, _, err := (Plugin{}).Login(ctx, http.DefaultClient, registered.Settings, secretID)
	if err != nil {
		t.Fatal(err)
	}
	put(own+"/model", map[string]any{"api_key": "sk-the-second"})
	if read, err := (Plugin{}).Read(ctx, http.DefaultClient, registered.Settings, session, own+"/model", "api_key"); err != nil || read != "sk-the-second" {
		t.Errorf("after the value changed, it was read as %q: %v", read, err)
	}
	// A session that the store has ended is told apart from a path that the
	// role may not read.
	owner(t, "POST", "/v1/auth/token/revoke", map[string]any{"token": session})
	if read, err := (Plugin{}).Read(ctx, http.DefaultClient, registered.Settings, session, own+"/model", "api_key"); !errors.Is(err, secret.ErrSessionEnded) || read != "" {
		t.Errorf("with a session that has ended, the value was read as %q: %v", read, err)
	}
	// The Supervisor, which remembers its own session, logs in again.
	if read, err := stores.Read(ctx, registered, secretID, own+"/model", "api_key"); err != nil || read != "sk-the-first" {
		t.Errorf("the value that the Supervisor remembers was read as %q: %v", read, err)
	}
}
