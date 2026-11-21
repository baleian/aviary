package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/sandbox"
)

// Agents are listed to every member of the project, and sessions to those
// who may read them: their own, or everyone's.
func TestLists(t *testing.T) {
	installation := install(t)
	provider := tokens{"admin": {Subject: "admin", Groups: []string{"platform"}}}
	for _, caller := range []string{"ann", "ben", "viewer", "auditor", "stranger"} {
		provider[caller] = identity.Caller{Subject: caller, Username: caller + "@example.com"}
	}
	installation.callers = api.Callers{Provider: provider, Admins: []string{"platform"}}
	s := installation.supervisor()
	// as sets the caller of the following requests.
	as := func(caller string) *supervisor {
		s.token = caller
		return s
	}

	project, other := as("admin").project(), s.project()
	members := map[string]string{}
	for caller, role := range map[string]string{"ann": "user", "ben": "user", "viewer": "viewer", "auditor": "auditor"} {
		members[caller] = s.create("/v1/projects/"+project+"/members", `{"user": "`+caller+`@example.com", "role": "`+role+`"}`)
	}
	first, second := s.agent(project, `{"harness": "opencode", "instructions": "Be brief."}`), s.agent(project, `{"harness": "codex"}`)
	elsewhere := s.agent(other, `{"harness": "opencode"}`)

	type agent struct {
		ID           string `json:"id"`
		ProjectID    string `json:"project_id"`
		Harness      string `json:"harness"`
		Instructions string `json:"instructions"`
	}
	for _, listed := range []struct {
		caller, project string
		agents          []agent
	}{
		{"viewer", project, []agent{{first, project, "opencode", "Be brief."}, {second, project, "codex", ""}}},
		{"admin", project, []agent{{first, project, "opencode", "Be brief."}, {second, project, "codex", ""}}},
		{"admin", other, []agent{{elsewhere, other, "opencode", ""}}},
	} {
		var answer struct{ Agents []agent }
		if status, _ := as(listed.caller).request("GET", "/v1/projects/"+listed.project+"/agents", "", &answer); status != http.StatusOK ||
			!slices.Equal(answer.Agents, listed.agents) {
			t.Errorf("%s: list agents = %d %+v, want %+v", listed.caller, status, answer.Agents, listed.agents)
		}
	}
	var empty struct{ Agents []agent }
	if status, _ := as("admin").request("GET", "/v1/projects/"+s.project()+"/agents", "", &empty); status != http.StatusOK || empty.Agents == nil {
		t.Errorf("list agents of an empty project = %d %+v, want an empty list", status, empty.Agents)
	}

	// Five sessions of the first agent, alternately created by ann and ben.
	var created []string
	for _, caller := range []string{"ann", "ben", "ann", "ben", "ann"} {
		created = append(created, as(caller).create("/v1/agents/"+first+"/sessions", ""))
	}
	apart := as("ann").create("/v1/agents/"+second+"/sessions", "")
	ann, ben := []string{created[0], created[2], created[4]}, []string{created[1], created[3]}
	type session struct {
		ID        string `json:"id"`
		AgentID   string `json:"agent_id"`
		CreatedBy string `json:"created_by"`
	}
	for _, listed := range []struct {
		caller, agent, query string
		sessions             []string
	}{
		{"ann", first, "", []string{ann[2], ann[1], ann[0]}},
		{"ann", first, "?limit=2", []string{ann[2], ann[1]}},
		{"ann", first, "?limit=2&before=" + ann[1], []string{ann[0]}},
		{"ann", first, "?before=" + ben[1], []string{ann[1], ann[0]}},
		{"ann", second, "", []string{apart}},
		{"ben", first, "", []string{ben[1], ben[0]}},
		{"ben", second, "", []string{}},
		// A caller who may read no session gets an empty list.
		{"viewer", first, "", []string{}},
		{"auditor", first, "", []string{created[4], created[3], created[2], created[1], created[0]}},
		{"auditor", first, "?limit=2", []string{created[4], created[3]}},
		{"auditor", first, "?limit=2&before=" + created[3], []string{created[2], created[1]}},
		{"auditor", first, "?before=" + created[0], []string{}},
		{"admin", first, "?limit=100", []string{created[4], created[3], created[2], created[1], created[0]}},
		{"admin", second, "", []string{apart}},
		{"admin", elsewhere, "", []string{}},
	} {
		var answer struct{ Sessions []session }
		status, _ := as(listed.caller).request("GET", "/v1/agents/"+listed.agent+"/sessions"+listed.query, "", &answer)
		ids := []string{}
		for _, session := range answer.Sessions {
			if session.AgentID != listed.agent || session.CreatedBy == "" {
				t.Errorf("session of agent %s listed as %+v", listed.agent, session)
			}
			ids = append(ids, session.ID)
		}
		if status != http.StatusOK || answer.Sessions == nil || !slices.Equal(ids, listed.sessions) {
			t.Errorf("list sessions%s as %s = %d %v, want %v", listed.query, listed.caller, status, ids, listed.sessions)
		}
	}

	// A member who may no longer run sessions does not see their own either.
	var demoted struct{ Sessions []session }
	if status, _ := as("admin").request("PUT", "/v1/members/"+members["ben"], `{"role": "viewer"}`, nil); status != http.StatusOK {
		t.Fatalf("PUT member: status %d, want 200", status)
	}
	if status, _ := as("ben").request("GET", "/v1/agents/"+first+"/sessions", "", &demoted); status != http.StatusOK || demoted.Sessions == nil || len(demoted.Sessions) != 0 {
		t.Errorf("list sessions after demotion = %d %+v, want an empty list", status, demoted.Sessions)
	}

	none := "00000000-0000-0000-0000-000000000000"
	for _, refused := range []struct {
		caller, path string
		status       int
	}{
		{"stranger", "/v1/projects/" + project + "/agents", http.StatusNotFound},
		{"ann", "/v1/projects/" + other + "/agents", http.StatusNotFound},
		{"admin", "/v1/projects/" + none + "/agents", http.StatusNotFound},
		{"stranger", "/v1/agents/" + first + "/sessions", http.StatusNotFound},
		{"ann", "/v1/agents/" + elsewhere + "/sessions", http.StatusNotFound},
		{"admin", "/v1/agents/" + none + "/sessions", http.StatusNotFound},
		{"ann", "/v1/agents/" + first + "/sessions?before=the-last", http.StatusBadRequest},
		{"ann", "/v1/agents/" + first + "/sessions?limit=0", http.StatusBadRequest},
		{"ann", "/v1/agents/" + first + "/sessions?limit=101", http.StatusBadRequest},
	} {
		if status, _ := as(refused.caller).request("GET", refused.path, "", nil); status != refused.status {
			t.Errorf("GET %s as %s: status %d, want %d", refused.path, refused.caller, status, refused.status)
		}
	}
}

// The model providers and the secret stores of a project are listed to every
// member, in the order in which they were created and as they are read: with
// neither the key nor the credential.
func TestListsOfConnections(t *testing.T) {
	aviary := install(t)
	provider := tokens{"admin": {Subject: "admin", Groups: []string{"platform"}}}
	for _, caller := range []string{"viewer", "stranger"} {
		provider[caller] = identity.Caller{Subject: caller, Username: caller + "@example.com"}
	}
	aviary.callers = api.Callers{Provider: provider, Admins: []string{"platform"}}
	s := aviary.supervisor()
	as := func(caller string) *supervisor {
		s.token = caller
		return s
	}
	const address, key = "https://" + storeHost, "sk-a-key"

	project, other, empty := as("admin").project(), s.project(), s.project()
	s.create("/v1/projects/"+project+"/members", `{"user": "viewer@example.com", "role": "viewer"}`)
	providers := func(project string) string { return "/v1/projects/" + project + "/providers" }
	stores := func(project string) string { return "/v1/projects/" + project + "/secret-stores" }
	first := s.create(stores(project), scriptedStore("first", address, "a-credential", "a-credential"))
	second := s.create(stores(project), scriptedStore("second", address, "another", "another"))
	theirStore := s.create(stores(other), scriptedStore("theirs", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", key)
	stored := s.create(providers(project), keyed(`{"value": "`+key+`"}`))
	located := s.create(providers(project), keyed(in(first, "llm/model", "api_key")))
	keyless := s.create(providers(project), `{"name": "keyless", "url": "http://10.0.0.5:8000/v1"}`)
	theirs := s.create(providers(other), keyed(`{"value": "`+key+`"}`))

	type resources = []map[string]any
	// list returns the resources under name at path, and fails the test if
	// the response holds the key.
	list := func(caller, path, name string) resources {
		t.Helper()
		var raw json.RawMessage
		if status, _ := as(caller).request("GET", path, "", &raw); status != http.StatusOK {
			t.Fatalf("GET %s as %s: status %d %s, want 200", path, caller, status, raw)
		}
		if strings.Contains(string(raw), key) {
			t.Errorf("GET %s as %s contains the key: %s", path, caller, raw)
		}
		var answer map[string]resources
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatal(err)
		}
		if answer[name] == nil {
			t.Errorf("GET %s as %s = %s, want a list under %q", path, caller, raw, name)
		}
		return answer[name]
	}
	for _, listed := range []struct {
		caller, path, name, read string
		ids                      []string
	}{
		{"viewer", providers(project), "providers", "/v1/providers/", []string{stored, located, keyless}},
		{"admin", providers(project), "providers", "/v1/providers/", []string{stored, located, keyless}},
		{"admin", providers(other), "providers", "/v1/providers/", []string{theirs}},
		{"admin", providers(empty), "providers", "/v1/providers/", []string{}},
		{"viewer", stores(project), "secret_stores", "/v1/secret-stores/", []string{first, second}},
		{"admin", stores(project), "secret_stores", "/v1/secret-stores/", []string{first, second}},
		{"admin", stores(other), "secret_stores", "/v1/secret-stores/", []string{theirStore}},
		{"admin", stores(empty), "secret_stores", "/v1/secret-stores/", []string{}},
	} {
		ids := []string{}
		for _, resource := range list(listed.caller, listed.path, listed.name) {
			id, _ := resource["id"].(string)
			ids = append(ids, id)
			var read map[string]any
			if status, _ := s.request("GET", listed.read+id, "", &read); status != http.StatusOK || !reflect.DeepEqual(resource, read) {
				t.Errorf("GET %s as %s lists %v, which is read as %d %v", listed.path, listed.caller, resource, status, read)
			}
		}
		if !slices.Equal(ids, listed.ids) {
			t.Errorf("GET %s as %s = %v, want %v", listed.path, listed.caller, ids, listed.ids)
		}
	}
	// A key is listed as stored, or by where it is.
	keys := map[string]any{}
	for _, provider := range list("viewer", providers(project), "providers") {
		id, _ := provider["id"].(string)
		keys[id] = provider["api_key"]
	}
	if want := map[string]any{
		stored:  map[string]any{"stored": true},
		located: map[string]any{"store": first, "path": "llm/model", "key": "api_key"},
		keyless: nil,
	}; !reflect.DeepEqual(keys, want) {
		t.Errorf("listed keys = %v, want %v", keys, want)
	}
	for _, store := range list("viewer", stores(project), "secret_stores") {
		if !reflect.DeepEqual(store["credential"], map[string]any{"stored": true}) {
			t.Errorf("listed secret store %v, want a stored credential", store)
		}
	}

	none := "00000000-0000-0000-0000-000000000000"
	for _, refused := range []struct{ caller, path string }{
		{"stranger", providers(project)},
		{"stranger", stores(project)},
		{"viewer", providers(other)},
		{"viewer", stores(other)},
		{"admin", providers(none)},
		{"admin", stores(none)},
	} {
		if status, _ := as(refused.caller).request("GET", refused.path, "", nil); status != http.StatusNotFound {
			t.Errorf("GET %s as %s: status %d, want 404", refused.path, refused.caller, status)
		}
	}
}

// The harnesses that agents can use are listed by name, as the installation
// configures them, and an agent can be created with each.
func TestListOfHarnesses(t *testing.T) {
	t.Parallel()
	type harnesses = []map[string]any
	for _, test := range []struct {
		configured map[string]sandbox.Harness
		listed     harnesses
	}{
		{nil, harnesses{{"name": "codex"}, {"name": "opencode"}}},
		{map[string]sandbox.Harness{"another": {Image: "registry.example.com/harness-another:1.0"}}, harnesses{{"name": "another"}}},
	} {
		s := install(t, func(config *driver.Config) {
			if test.configured != nil {
				config.Harnesses = test.configured
			}
		}).supervisor()
		var answer struct{ Harnesses harnesses }
		if status, _ := s.request("GET", "/v1/harnesses", "", &answer); status != http.StatusOK || !reflect.DeepEqual(answer.Harnesses, test.listed) {
			t.Fatalf("GET /v1/harnesses = %d %v, want %v", status, answer.Harnesses, test.listed)
		}
		project := s.project()
		for _, harness := range answer.Harnesses {
			s.agent(project, fmt.Sprintf(`{"harness": %q}`, harness["name"]))
		}
	}
}

// Without an identity provider, every session is listed to every caller.
func TestListsOfAnInstallationThatAsksNobody(t *testing.T) {
	s := install(t).supervisor()
	agent := s.agent(s.project(), `{"harness": "opencode"}`)
	older, newer := s.create("/v1/agents/"+agent+"/sessions", ""), s.create("/v1/agents/"+agent+"/sessions", "")
	var answer struct{ Sessions []struct{ ID string } }
	if status, _ := s.request("GET", "/v1/agents/"+agent+"/sessions", "", &answer); status != http.StatusOK ||
		len(answer.Sessions) != 2 || answer.Sessions[0].ID != newer || answer.Sessions[1].ID != older {
		t.Errorf("list sessions %s and %s = %d %+v, want newest first", older, newer, status, answer.Sessions)
	}
}
