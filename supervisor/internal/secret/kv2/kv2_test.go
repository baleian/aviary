package kv2

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/secret"
	"aviary/supervisor/internal/testenv"
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
		if got, err := (Driver{}).Address(good); err != nil || got != strings.TrimSuffix(field(good, "address"), "/") {
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
		if got, err := (Driver{}).Address(bad); err == nil {
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

// What the driver asks a store to log in, and what it makes of the answers.
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

	session, good, err := (Driver{}).Login(ctx, http.DefaultClient, answering(http.StatusOK, `{"auth": {"client_token": "the-token", "lease_duration": 3600}}`), secretID)
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
		session, _, err := (Driver{}).Login(ctx, http.DefaultClient, refusing.store, secretID)
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

// A store that serves the API takes the login of the driver, as the
// Supervisor asks it: with the stores of an installation.
func TestLoginToAStore(t *testing.T) {
	t.Parallel()
	address := testenv.SecretStoreURL(t)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := secret.NewStores([]string{parsed.Host}, Driver{})
	if err != nil {
		t.Fatal(err)
	}
	roleID, secretID, _ := role(t)
	other, another, _ := role(t)
	ctx := context.Background()

	if err := stores.Check(ctx, Kind, of(address, "secret", roleID), secretID); err != nil {
		t.Errorf("the store does not take the login of its role: %v", err)
	}
	session, good, err := (Driver{}).Login(ctx, http.DefaultClient, of(address, "secret", other), another)
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
