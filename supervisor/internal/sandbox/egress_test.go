package sandbox

import (
	"strings"
	"testing"

	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	"google.golang.org/protobuf/proto"
)

func TestHostOf(t *testing.T) {
	for address, want := range map[string]Host{
		"https://api.example.com/v1":         {TLS: true, Name: "api.example.com", Port: 443},
		"http://Model.Example.com:8000/v1":   {Name: "model.example.com", Port: 8000},
		"http://keycloak.localhost:8081/x":   {Name: "keycloak.localhost", Port: 8081},
		"https://*":                          {TLS: true, Name: "*", Port: 443},
		"https://*.example.com:8443/ignored": {TLS: true, Name: "*.example.com", Port: 8443},
	} {
		if got, err := HostOf(address); err != nil || got != want {
			t.Errorf("HostOf(%q) = %+v, %v; want %+v", address, got, err, want)
		}
	}
	for _, wrong := range []string{
		"ftp://example.com",
		"example.com",
		"https://",
		"https://10.0.0.1/v1",
		"http://[::1]:8000",
		"http://localhost:11434/v1",
		"https://a_b.example.com",
		"https://example.com:0",
		"https://example.com:70000",
		"https://*a.example.com",
		"https://a.*.example.com",
	} {
		if got, err := HostOf(wrong); err == nil {
			t.Errorf("HostOf(%q) = %+v, want an error", wrong, got)
		}
	}
}

func TestEgressRules(t *testing.T) {
	api := Host{TLS: true, Name: "api.example.com", Port: 443}
	model := Host{Name: "model.example.com", Port: 8000}
	const key, team, token = "ate-secret://aviary.io/default/providers/1/key", "ate-secret://aviary.io/default/providers/1/headers/x-team", "ate-secret://aviary.io/default/mcp-servers/2/headers/x-token"
	egress := Egress{
		Hosts: []Host{{Name: "pypi.example.com", Port: 80}, {TLS: true, Name: "*", Port: 443}, api, model},
		Secrets: []Secret{
			{Host: api, Header: "X-Team", URI: team},
			{Host: api, Header: "authorization", Prefix: "Bearer ", URI: key},
			{Host: model, Header: "x-token", URI: token},
		},
	}
	rules, err := egress.rules()
	if err != nil {
		t.Fatal(err)
	}
	// Each name and port has a rule, in the order of the names. TLS passes
	// through unless secrets go into the requests, which the gateway then
	// reads.
	on := func(port int32) *ateapi.Ports { return &ateapi.Ports{Numbers: []int32{port}} }
	want := []*ateapi.EgressRule{
		{TlsPassthrough: &ateapi.TlsPassthroughRule{Hostnames: []string{"*"}, Ports: on(443)}},
		{Https: &ateapi.HttpsRule{Hostnames: []string{"api.example.com"}, Ports: on(443), Effects: &ateapi.HttpRuleEffects{ReplaceHeaders: []*ateapi.CredentialHeader{
			{Header: "authorization", Prefix: "Bearer ", CredentialUri: key},
			{Header: "x-team", CredentialUri: team},
		}}}},
		{Http: &ateapi.HttpRule{Hostnames: []string{"model.example.com"}, Ports: on(8000), Effects: &ateapi.HttpRuleEffects{ReplaceHeaders: []*ateapi.CredentialHeader{
			{Header: "x-token", CredentialUri: token},
		}}}},
		{Http: &ateapi.HttpRule{Hostnames: []string{"pypi.example.com"}, Ports: on(80)}},
	}
	if len(rules) != len(want) {
		t.Fatalf("rules = %v, want %v", rules, want)
	}
	for i := range want {
		if !proto.Equal(rules[i], want[i]) {
			t.Errorf("rule %d = %v, want %v", i, rules[i], want[i])
		}
	}

	// A header of a host takes one secret.
	egress.Secrets = append(egress.Secrets, Secret{Host: api, Header: "Authorization", URI: team})
	if _, err := egress.rules(); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Errorf("two secrets for a header: %v, want an error", err)
	}
}
