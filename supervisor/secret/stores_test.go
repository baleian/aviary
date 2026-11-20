package secret

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// asking is a plugin whose Login posts the credential to the store's address
// and succeeds on 200 OK.
type asking struct{}

func (asking) Kind() string { return "asking" }

func (asking) Address(settings json.RawMessage) (string, error) {
	var store struct{ Address string }
	if err := json.Unmarshal(settings, &store); err != nil || store.Address == "" {
		return "", errors.New("address is required")
	}
	return store.Address, nil
}

func (a asking) Login(ctx context.Context, client *http.Client, settings json.RawMessage, credential string) (any, time.Duration, error) {
	address, err := a.Address(settings)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(credential))
	if err != nil {
		return nil, 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, errors.New("the store answered with " + response.Status)
	}
	return credential, time.Hour, nil
}

func (asking) Read(context.Context, *http.Client, json.RawMessage, any, string, string) (string, error) {
	return "", errors.New("a store of this kind is not read")
}

func stores(t *testing.T, hosts ...string) *Stores {
	t.Helper()
	stores, err := NewStores(hosts, time.Minute, asking{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stores.Close)
	return stores
}

// at returns the settings of a store at the address.
func at(address string) json.RawMessage {
	settings, _ := json.Marshal(map[string]string{"address": address})
	return settings
}

func TestHostsOfStores(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		hosts   []string
		address string
		allowed bool
	}{
		{nil, "https://secrets.example.com:8200", false},
		{[]string{"*"}, "https://secrets.example.com:8200", true},
		{[]string{"secrets.example.com:8200"}, "https://secrets.example.com:8200", true},
		{[]string{"SECRETS.example.com:8200"}, "https://secrets.EXAMPLE.com:8200", true},
		{[]string{"secrets.example.com:8200"}, "https://secrets.example.com:8201", false},
		{[]string{"secrets.example.com:8200"}, "https://secrets.example.com", false},
		{[]string{"secrets.example.com:8200"}, "https://other.example.com:8200", false},
		{[]string{"secrets.example.com:8200"}, "https://sub.secrets.example.com:8200", false},
		// A host without a port allows the default HTTPS and HTTP ports.
		{[]string{"secrets.example.com"}, "https://secrets.example.com", true},
		{[]string{"secrets.example.com"}, "https://secrets.example.com:443", true},
		{[]string{"secrets.example.com"}, "http://secrets.example.com:80", true},
		{[]string{"secrets.example.com"}, "https://secrets.example.com:8200", false},
		{[]string{"other.example.com", "secrets.example.com:8200"}, "https://secrets.example.com:8200", true},
		{[]string{"*"}, "http://[::1", false},
	} {
		if allowed := stores(t, test.hosts...).allows(test.address); allowed != test.allowed {
			t.Errorf("hosts %v: allows(%s) = %v", test.hosts, test.address, allowed)
		}
	}
	for _, host := range []string{"https://secrets.example.com", "secrets.example.com/v1", "*.example.com", "a host", "user@secrets.example.com", ""} {
		if _, err := NewStores([]string{"secrets.example.com", host}, time.Minute); err == nil || !strings.Contains(err.Error(), host) {
			t.Errorf("NewStores with host %q: %v, want an error naming it", host, err)
		}
	}
}

// Check validates the kind, the settings and the host, and logs in.
func TestStoreIsChecked(t *testing.T) {
	t.Parallel()
	var asked atomic.Int32
	answering := func(status int) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			w.WriteHeader(status)
		}))
		t.Cleanup(server.Close)
		return server.URL
	}
	taking, refusing := answering(http.StatusOK), answering(http.StatusForbidden)
	host := strings.TrimPrefix(taking, "http://")
	ctx := context.Background()

	if err := stores(t, host).Check(ctx, "asking", at(taking), "a credential"); err != nil {
		t.Errorf("Check: %v", err)
	}
	for _, test := range []struct {
		hosts    []string
		kind     string
		settings json.RawMessage
		why      string
	}{
		{[]string{"*"}, "another", at(taking), "asking"},
		{[]string{"*"}, "", at(taking), "asking"},
		{[]string{"*"}, "asking", json.RawMessage(`{}`), "settings: address is required"},
		{[]string{"*"}, "asking", nil, "settings: address is required"},
		{nil, "asking", at(taking), "supervisor.secretStores.allowedHosts"},
		{[]string{"other.example.com"}, "asking", at(taking), "supervisor.secretStores.allowedHosts"},
		{[]string{"*"}, "asking", at(refusing), "login failed: the store answered with 403"},
	} {
		before := asked.Load()
		err := stores(t, test.hosts...).Check(ctx, test.kind, test.settings, "a credential")
		if err == nil || !strings.Contains(err.Error(), test.why) {
			t.Errorf("Check(%q, %s) with hosts %v = %v", test.kind, test.settings, test.hosts, err)
		}
		// A store at a host that is not allowed is never contacted.
		if strings.Contains(test.why, "allowedHosts") && asked.Load() != before {
			t.Errorf("store at a disallowed host was contacted")
		}
	}
}

// The client for the stores does not follow redirects, times out, and
// ignores the proxy settings of the environment.
func TestStoreIsAskedWithCare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var followed atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusTemporaryRedirect))
	t.Cleanup(redirecting.Close)
	if err := stores(t, "*").Check(ctx, "asking", at(redirecting.URL), "a credential"); err == nil || !strings.Contains(err.Error(), "307") || followed.Load() {
		t.Errorf("Check with a redirect = %v, followed %v; want a 307 error, not followed", err, followed.Load())
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	hurried := stores(t, "*")
	hurried.client.Timeout = 100 * time.Millisecond
	began := time.Now()
	if err := hurried.Check(ctx, "asking", at(slow.URL), "a credential"); err == nil || time.Since(began) > 2*time.Second {
		t.Errorf("Check of a slow store took %s: %v", time.Since(began), err)
	}
	// The default client times out before the Egress Proxy's authz check
	// does, and uses no proxy.
	client := stores(t, "*").client
	if transport, ok := client.Transport.(*http.Transport); client.Timeout != timeout || timeout > 4*time.Second || !ok || transport.Proxy != nil {
		t.Errorf("client transport = %+v, timeout = %s", client.Transport, client.Timeout)
	}
}
