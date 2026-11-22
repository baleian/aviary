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

// client sends the requests of the tests. It has a transport of its own:
// closing an httptest.Server closes the idle connections of the default
// transport, which can break a request on it whose response has no body.
var client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}

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
			t.Errorf("Address(%s) = %q, %v", good, got, err)
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
		of(address, "team//secrets", "a-role"),
		of(address, "secret/./kv", "a-role"),
		of(address, "a/../../sys", "a-role"),
		of(address, "secret", ""),
	} {
		if got, err := (Plugin{}).Address(bad); err == nil {
			t.Errorf("Address(%s) = %q, want an error", bad, got)
		}
	}
}

// The mount, like the path, is escaped into the URL of a read.
func TestReadEscapesMount(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/kv%3Fx/data/team/model" || r.URL.RawQuery != "" {
			t.Errorf("read request for %s?%s, want /v1/kv%%3Fx/data/team/model", r.URL.EscapedPath(), r.URL.RawQuery)
		}
		io.WriteString(w, `{"data": {"data": {"api_key": "the-value"}}}`)
	}))
	t.Cleanup(server.Close)
	value, err := (Plugin{}).Read(context.Background(), client, of(server.URL, "kv?x", "a-role"), "a-token", "team/model", "api_key")
	if err != nil || value != "the-value" {
		t.Errorf("Read = %q, %v; want the-value", value, err)
	}
}

// field returns a string field of a JSON object.
func field(object json.RawMessage, name string) string {
	var fields map[string]string
	json.Unmarshal(object, &fields)
	return fields[name]
}

// Login sends the role ID and the secret ID, and reports a refusal by its
// status only.
func TestLoginIsAsked(t *testing.T) {
	t.Parallel()
	const roleID, secretID = "the-role-id", "the-secret-id"
	// answering returns the settings of a store that answers a login with
	// status and body.
	answering := func(status int, body string) json.RawMessage {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var sent map[string]string
			if raw, _ := io.ReadAll(r.Body); json.Unmarshal(raw, &sent) != nil ||
				r.Method != http.MethodPost || r.URL.Path != "/v1/auth/approle/login" || sent["role_id"] != roleID || sent["secret_id"] != secretID {
				t.Errorf("unexpected login request %s %s %v", r.Method, r.URL.Path, sent)
			}
			w.WriteHeader(status)
			io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		return of(server.URL+"/", "secret", roleID)
	}
	ctx := context.Background()

	session, good, err := (Plugin{}).Login(ctx, client, answering(http.StatusOK, `{"auth": {"client_token": "the-token", "lease_duration": 3600}}`), secretID)
	if err != nil || session != "the-token" || good != time.Hour {
		t.Errorf("Login = %v, %s, %v", session, good, err)
	}
	// Errors do not include the response body.
	for _, refusing := range []struct {
		store json.RawMessage
		why   string
	}{
		{answering(http.StatusBadRequest, `{"errors": ["invalid role or secret ID: the-detail"]}`), "status 400"},
		{answering(http.StatusForbidden, "the-detail"), "status 403"},
		{answering(http.StatusOK, `{"auth": {"client_token": "", "the-detail": true}}`), "no client token"},
		{answering(http.StatusOK, "the-detail"), "no client token"},
		{of("http://127.0.0.1:1", "secret", roleID), "connect to the store"},
		{of("http://127.0.0.1:1", "", roleID), "mount is required"},
	} {
		session, _, err := (Plugin{}).Login(ctx, client, refusing.store, secretID)
		if err == nil || session != nil || !strings.Contains(err.Error(), refusing.why) || strings.Contains(err.Error(), "the-detail") || strings.Contains(err.Error(), secretID) {
			t.Errorf("%s: Login = %v, %v; want an error without the response body or the secret ID", refusing.why, session, err)
		}
	}
}

// rootToken is the root token of the test secret store.
const rootToken = "test"

// owner sends a request to the test secret store with the root token and
// returns the data of the response. statuses are accepted besides 200 and
// 204.
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
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("test secret store: %v", err)
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
	t.Fatalf("test secret store: %s %s = %d %s", method, path, response.StatusCode, raw)
	return nil
}

// role creates an AppRole in the test secret store that may read the secrets
// below a path of its own. It returns the role ID, a secret ID and the path.
func role(t *testing.T) (roleID, secretID, path string) {
	t.Helper()
	// Enable AppRole. Another test may have done so already, hence the 400.
	owner(t, "POST", "/v1/sys/auth/approle", map[string]string{"type": "approle"}, http.StatusBadRequest)
	name := "test-" + strings.ToLower(rand.Text())
	owner(t, "PUT", "/v1/sys/policies/acl/"+name, map[string]string{
		"policy": fmt.Sprintf(`path "secret/data/%s/*" { capabilities = ["read"] }`, name),
	})
	owner(t, "POST", "/v1/auth/approle/role/"+name, map[string]string{"token_policies": name, "token_ttl": "1h"})
	roleID, _ = owner(t, "GET", "/v1/auth/approle/role/"+name+"/role-id", nil)["role_id"].(string)
	secretID, _ = owner(t, "POST", "/v1/auth/approle/role/"+name+"/secret-id", nil)["secret_id"].(string)
	if roleID == "" || secretID == "" {
		t.Fatal("test secret store returned no role ID or secret ID")
	}
	return roleID, secretID, name
}

// A real store takes the login of a role only with the role's own secret ID.
func TestLoginToAStore(t *testing.T) {
	t.Parallel()
	address := testenv.SecretStoreURL(t)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := secret.NewStores([]string{parsed.Host}, time.Minute, Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stores.Close)
	roleID, secretID, _ := role(t)
	other, another, _ := role(t)
	ctx := context.Background()

	if err := stores.Check(ctx, Kind, of(address, "secret", roleID), secretID); err != nil {
		t.Errorf("Check: %v", err)
	}
	session, good, err := (Plugin{}).Login(ctx, client, of(address, "secret", other), another)
	if token, _ := session.(string); err != nil || token == "" || good != time.Hour {
		t.Errorf("Login = %v, %s, %v", session, good, err)
	}
	for name, refused := range map[string]struct{ roleID, secretID string }{
		"secret ID of another role": {roleID, another},
		"unknown secret ID":         {roleID, "not-a-secret-id"},
		"unknown role":              {"not-a-role", secretID},
	} {
		err := stores.Check(ctx, Kind, of(address, "secret", refused.roleID), refused.secretID)
		if err == nil || !strings.Contains(err.Error(), "login failed: store returned status 400") {
			t.Errorf("Check with %s: %v", name, err)
		}
	}
}

// A real store is read through Stores, as the Supervisor reads it.
func TestReadFromAStore(t *testing.T) {
	t.Parallel()
	address := testenv.SecretStoreURL(t)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := secret.NewStores([]string{parsed.Host}, time.Minute, Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stores.Close)
	roleID, secretID, own := role(t)
	_, _, theirs := role(t)
	ctx := context.Background()
	// put writes values at path with the root token.
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
			t.Errorf("Read(%s) = %q, %v", path, read, err)
		}
	}
	// Errors say why, and do not reveal values.
	for name, refused := range map[string]struct{ path, key, why string }{
		"missing secret":             {own + "/nothing", "api_key", "secret not found"},
		"path the role may not read": {theirs + "/model", "api_key", "role is not allowed to read the secret"},
		"missing key":                {own + "/model", "token", "secret has no such key"},
		"non-string value":           {own + "/model", "retries", "value is not a string"},
		"no key":                     {own + "/model", "", "key is required"},
		"no path":                    {"", "api_key", "path must be a secret path"},
		"leading slash":              {"/" + own + "/model", "api_key", "path must be a secret path"},
		"dot-dot segment":            {own + "/../" + theirs + "/model", "api_key", "path must be a secret path"},
	} {
		read, err := stores.Read(ctx, registered, secretID, refused.path, refused.key)
		if err == nil || read != "" || !strings.Contains(err.Error(), refused.why) || strings.Contains(err.Error(), "sk-") {
			t.Errorf("%s: Read = %q, %v", name, read, err)
		}
	}
	// A wrong secret ID fails the login.
	wrong := registered
	wrong.ID = uuid.New()
	if read, err := stores.Read(ctx, wrong, "not-the-secret-id", own+"/model", "api_key"); err == nil || read != "" || !strings.Contains(err.Error(), "login failed") {
		t.Errorf("Read with a wrong secret ID = %q, %v", read, err)
	}

	// The plugin itself does not cache.
	session, _, err := (Plugin{}).Login(ctx, client, registered.Settings, secretID)
	if err != nil {
		t.Fatal(err)
	}
	put(own+"/model", map[string]any{"api_key": "sk-the-second"})
	if read, err := (Plugin{}).Read(ctx, client, registered.Settings, session, own+"/model", "api_key"); err != nil || read != "sk-the-second" {
		t.Errorf("Read after the change = %q, %v", read, err)
	}
	// A revoked token gives ErrSessionEnded, not a permission error.
	owner(t, "POST", "/v1/auth/token/revoke", map[string]any{"token": session})
	if read, err := (Plugin{}).Read(ctx, client, registered.Settings, session, own+"/model", "api_key"); !errors.Is(err, secret.ErrSessionEnded) || read != "" {
		t.Errorf("Read with a revoked token = %q, %v; want ErrSessionEnded", read, err)
	}
	// Stores still serves its cached value: the revoked token was not its
	// own session.
	if read, err := stores.Read(ctx, registered, secretID, own+"/model", "api_key"); err != nil || read != "sk-the-first" {
		t.Errorf("cached Read = %q, %v", read, err)
	}
	// Once its own session is revoked too, Stores logs in again to read a
	// value that it has not cached.
	revokeTokens(t, own)
	put(own+"/model two", map[string]any{"api_key": "sk-after-the-revocation"})
	if read, err := stores.Read(ctx, registered, secretID, own+"/model two", "api_key"); err != nil || read != "sk-after-the-revocation" {
		t.Errorf("Read after the session was revoked = %q, %v; want sk-after-the-revocation", read, err)
	}
}

// revokeTokens revokes every token issued to the AppRole role. A token that
// another test revokes meanwhile is skipped.
func revokeTokens(t *testing.T, role string) {
	t.Helper()
	accessors, _ := owner(t, "LIST", "/v1/auth/token/accessors", nil)["keys"].([]any)
	revoked := 0
	for _, accessor := range accessors {
		token := owner(t, "POST", "/v1/auth/token/lookup-accessor", map[string]any{"accessor": accessor}, http.StatusBadRequest)
		if meta, _ := token["meta"].(map[string]any); meta["role_name"] == role {
			owner(t, "POST", "/v1/auth/token/revoke-accessor", map[string]any{"accessor": accessor}, http.StatusBadRequest)
			revoked++
		}
	}
	if revoked == 0 {
		t.Fatalf("no token of the role %s to revoke", role)
	}
}
