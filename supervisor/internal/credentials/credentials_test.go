package credentials

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestURIs(t *testing.T) {
	id := uuid.New()
	for uri, want := range map[string]asked{
		ProviderKey(id):                      {kind: providers, id: id},
		ProviderHeader(id, "X-Team"):         {kind: providers, id: id, header: "x-team"},
		ProviderHeader(id, "x-a#b%c"):        {kind: providers, id: id, header: "x-a#b%c"},
		McpServerHeader(id, "authorization"): {kind: mcpServers, id: id, header: "authorization"},
	} {
		if got, err := parse(uri); err != nil || got != want {
			t.Errorf("parse(%q) = %+v, %v; want %+v", uri, got, err, want)
		}
	}
	for _, wrong := range []string{
		"https://aviary.io/default/providers/{id}/key",
		"ate-secret://other.example.com/default/providers/{id}/key",
		"ate-secret://aviary.io/tenant/providers/{id}/key",
		"ate-secret://aviary.io/default/providers/not-an-id/key",
		"ate-secret://aviary.io/default/providers/{id}",
		"ate-secret://aviary.io/default/providers/{id}/headers/",
		"ate-secret://aviary.io/default/providers/{id}/key/more",
		"ate-secret://aviary.io/default/providers/{id}/headers/x-a/b",
		"ate-secret://aviary.io/default/mcp-servers/{id}/key",
		"ate-secret://aviary.io/default/agents/{id}/headers/x-team",
	} {
		uri := strings.ReplaceAll(wrong, "{id}", id.String())
		if got, err := parse(uri); err == nil {
			t.Errorf("parse(%q) = %+v, want an error", uri, got)
		}
	}
}

func TestSessionOfAnActor(t *testing.T) {
	s := New(nil, "aviary")
	id := uuid.New()
	if got, known := s.session("spiffe://substrate-actor.local/actor/aviary/session-" + id.String()); !known || got != id {
		t.Errorf("session of the sandbox = %s, %t; want %s", got, known, id)
	}
	for _, wrong := range []string{
		"spiffe://substrate-actor.local/actor/other/session-" + id.String(),
		"spiffe://cluster.local/actor/aviary/session-" + id.String(),
		"spiffe://substrate-actor.local/actor/aviary/" + id.String(),
		"spiffe://substrate-actor.local/actor/aviary/session-x",
		"https://substrate-actor.local/actor/aviary/session-" + id.String(),
		"",
	} {
		if got, known := s.session(wrong); known {
			t.Errorf("session of %q = %s, want none", wrong, got)
		}
	}
}
