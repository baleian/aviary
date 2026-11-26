package outbound

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The client does not connect to the Supervisor's own host, nor to addresses
// of its link.
func TestRefusedAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the client reached a loopback address")
	}))
	defer server.Close()
	client := Client("")
	for _, address := range []string{server.URL, "http://169.254.169.254/latest/meta-data", "http://[::1]:1/", "http://0.0.0.0:1/", "http://[fe80::1]:1/"} {
		if answer, err := client.Get(address); !Refused(err) {
			if err == nil {
				answer.Body.Close()
			}
			t.Errorf("GET %s: %v, want it refused", address, err)
		}
	}
}

// relayed answers every request with a redirect, and records the host of
// each.
type relayed struct{ hosts []string }

func (r *relayed) RoundTrip(request *http.Request) (*http.Response, error) {
	r.hosts = append(r.hosts, request.URL.Host)
	return &http.Response{
		StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://elsewhere.example.com/"}},
		Body: http.NoBody, Request: request,
	}, nil
}

// A request for the local host goes to the host that stands in for it, and a
// redirect is not followed.
func TestTheLocalHostIsWhereTheInstallationSays(t *testing.T) {
	for localhost, reached := range map[string]map[string]string{
		"host.example.com": {
			"http://localhost:11434/v1/models": "host.example.com:11434",
			"http://127.0.0.1/v1/models":       "host.example.com",
			"https://[::1]:8443/v1/models":     "host.example.com:8443",
			"https://api.example.com/v1":       "api.example.com",
		},
		"": {"http://localhost:11434/v1/models": "localhost:11434"},
	} {
		for address, host := range reached {
			through := &relayed{}
			answer, err := Through(localhost, through).Get(address)
			if err != nil {
				t.Fatal(err)
			}
			answer.Body.Close()
			if answer.StatusCode != http.StatusFound || len(through.hosts) != 1 || through.hosts[0] != host {
				t.Errorf("with %q for the local host, GET %s: %d at %v, want 302 at %s", localhost, address, answer.StatusCode, through.hosts, host)
			}
		}
	}
}
