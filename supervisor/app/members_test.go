package app

import (
	"net/http"
	"slices"
	"testing"

	"aviary/supervisor/internal/api"
)

// member is a member of a project as the API returns it.
type member struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	User      *struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	} `json:"user"`
	Group       string   `json:"group"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	UpdatedBy   string   `json:"updated_by"`
}

// members lists the members of the project.
func (s *supervisor) members(project string) []member {
	s.t.Helper()
	var listed struct{ Members []member }
	if status, _ := s.request(http.MethodGet, "/v1/projects/"+project+"/members", "", &listed); status != http.StatusOK || listed.Members == nil {
		s.t.Fatalf("list members = %d %+v", status, listed)
	}
	return listed.Members
}

// A member is a user, by username, or a group of the identity provider, with
// a role or with custom permissions.
func TestMembers(t *testing.T) {
	s := install(t).supervisor()
	project := s.create("/v1/projects", `{"name": "test"}`)
	if members := s.members(project); len(members) != 0 {
		t.Errorf("members of a new project = %+v, want none", members)
	}

	var team, alice member
	if status, _ := s.request(http.MethodPost, "/v1/projects/"+project+"/members", `{"group": "team-a", "role": "editor"}`, &team); status != http.StatusCreated ||
		team.Group != "team-a" || team.User != nil || team.Role != "editor" || team.Permissions != nil || team.ProjectID != project || team.ID == "" {
		t.Errorf("add group = %d %+v", status, team)
	}
	// Custom permissions are completed and ordered as those of a role.
	if status, _ := s.request(http.MethodPost, "/v1/projects/"+project+"/members", `{"user": "alice@example.com", "permissions": ["sessions.control_all"]}`, &alice); status != http.StatusCreated ||
		alice.User == nil || alice.User.Username != "alice@example.com" || alice.User.ID != "" || alice.Group != "" || alice.Role != "custom" ||
		!slices.Equal(alice.Permissions, []string{"project.view", "sessions.view_all", "sessions.control_all"}) {
		t.Errorf("add user = %d %+v, user %+v", status, alice, alice.User)
	}
	if members := s.members(project); len(members) != 2 || members[0].ID != team.ID || members[1].ID != alice.ID || members[1].Role != "custom" || members[1].User == nil {
		t.Errorf("members = %+v", members)
	}

	// A member can switch between a role and custom permissions.
	var changed member
	if status, _ := s.request(http.MethodPut, "/v1/members/"+alice.ID, `{"role": "viewer"}`, &changed); status != http.StatusOK ||
		changed.ID != alice.ID || changed.Role != "viewer" || changed.Permissions != nil || changed.User == nil || changed.User.Username != "alice@example.com" {
		t.Errorf("set role = %d %+v", status, changed)
	}
	if status, _ := s.request(http.MethodPut, "/v1/members/"+team.ID, `{"role": "custom", "permissions": ["agents.manage"]}`, &changed); status != http.StatusOK ||
		changed.ID != team.ID || changed.Role != "custom" || !slices.Equal(changed.Permissions, []string{"project.view", "agents.manage"}) || changed.Group != "team-a" {
		t.Errorf("set permissions = %d %+v", status, changed)
	}

	none := "00000000-0000-0000-0000-000000000000"
	for _, wrong := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-a", "role": "viewer"}`, http.StatusConflict, "member_exists"},
		{"POST", "/v1/projects/" + project + "/members", `{"user": "alice@example.com", "role": "viewer"}`, http.StatusConflict, "member_exists"},
		{"POST", "/v1/projects/" + project + "/members", `{"role": "viewer"}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"user": "bob@example.com", "group": "team-b", "role": "viewer"}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-b"}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-b", "role": "custom"}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-b", "role": "captain"}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-b", "role": "viewer", "permissions": []}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-b", "permissions": ["sessions.fly"]}`, http.StatusBadRequest, "invalid_request"},
		{"POST", "/v1/projects/" + none + "/members", `{"group": "team-b", "role": "viewer"}`, http.StatusNotFound, "not_found"},
		{"GET", "/v1/projects/" + none + "/members", ``, http.StatusNotFound, "not_found"},
		{"PUT", "/v1/members/" + none, `{"role": "viewer"}`, http.StatusNotFound, "not_found"},
		{"PUT", "/v1/members/" + team.ID, `{"role": "captain"}`, http.StatusBadRequest, "invalid_request"},
		{"PUT", "/v1/members/" + team.ID, `{}`, http.StatusBadRequest, "invalid_request"},
		{"DELETE", "/v1/members/" + none, ``, http.StatusNotFound, "not_found"},
	} {
		var answer struct{ Code string }
		if status, _ := s.request(wrong.method, wrong.path, wrong.body, &answer); status != wrong.status || answer.Code != wrong.code {
			t.Errorf("%s %s %s = %d %q, want %d %q", wrong.method, wrong.path, wrong.body, status, answer.Code, wrong.status, wrong.code)
		}
	}
	if members := s.members(project); len(members) != 2 || members[0].Role != "custom" || members[1].Role != "viewer" {
		t.Errorf("members after refused requests = %+v", members)
	}

	if status, _ := s.request(http.MethodDelete, "/v1/members/"+team.ID, "", nil); status != http.StatusNoContent {
		t.Errorf("DELETE member: status %d, want 204", status)
	}
	if status, _ := s.request(http.MethodDelete, "/v1/members/"+team.ID, "", nil); status != http.StatusNotFound {
		t.Errorf("DELETE removed member: status %d, want 404", status)
	}
	if members := s.members(project); len(members) != 1 || members[0].ID != alice.ID {
		t.Errorf("members after removal = %+v", members)
	}
}

// Only admins manage members. A user who is added by username before their
// first request is linked to the membership on that request.
func TestMembersAreGivenByAdministrators(t *testing.T) {
	installation := install(t)
	installation.callers = api.Callers{
		Provider: tokens{
			"an-administrator": {Subject: "1", Groups: []string{"platform"}},
			"token-of-alice":   {Subject: "2", Username: "alice@example.com", DisplayName: "Alice Kim"},
		},
		Admins: []string{"platform"},
	}
	s := installation.supervisor()
	s.token = "an-administrator"
	var admin me
	s.request(http.MethodGet, "/v1/me", "", &admin)
	project := s.create("/v1/projects", `{"name": "test"}`)
	var added member
	if status, _ := s.request(http.MethodPost, "/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`, &added); status != http.StatusCreated ||
		added.UpdatedBy != admin.ID || admin.ID == "" || added.User == nil || added.User.ID != "" {
		t.Fatalf("admin %s: add member = %d %+v", admin.ID, status, added)
	}

	// Alice's first request links the membership to Alice's user.
	s.token = "token-of-alice"
	var alice me
	s.request(http.MethodGet, "/v1/me", "", &alice)
	members := s.members(project)
	if len(members) != 1 || members[0].ID != added.ID || members[0].User == nil || members[0].User.ID != alice.ID || alice.ID == "" ||
		members[0].User.Username != "alice@example.com" || members[0].User.DisplayName != "Alice Kim" || members[0].Role != "user" {
		t.Errorf("members after the first request of user %s = %+v", alice.ID, members)
	}

	for _, request := range [][3]string{
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-a", "role": "owner"}`},
		{"PUT", "/v1/members/" + added.ID, `{"role": "owner"}`},
		{"DELETE", "/v1/members/" + added.ID, ``},
	} {
		var answer struct{ Code string }
		if status, _ := s.request(request[0], request[1], request[2], &answer); status != http.StatusForbidden || answer.Code != "forbidden" {
			t.Errorf("user: %s %s = %d %q, want 403 forbidden", request[0], request[1], status, answer.Code)
		}
	}
	if after := s.members(project); len(after) != 1 || after[0].Role != "user" {
		t.Errorf("members after refused changes = %+v", after)
	}
}

// A caller sees the projects of which the caller or one of its groups is a
// member, and an admin sees all of them.
func TestProjectsOfACaller(t *testing.T) {
	installation := install(t)
	installation.callers = api.Callers{
		Provider: tokens{
			"an-administrator": {Subject: "1", Groups: []string{"platform"}},
			"token-of-alice":   {Subject: "2", Username: "alice@example.com", Groups: []string{"team-a"}},
			"token-of-bob":     {Subject: "3", Username: "bob@example.com"},
		},
		Admins: []string{"platform"},
	}
	s := installation.supervisor()
	// projects returns the IDs of the projects listed to the caller with
	// token.
	projects := func(token string) []string {
		t.Helper()
		s.token = token
		var listed struct{ Projects []struct{ ID, Name string } }
		if status, _ := s.request(http.MethodGet, "/v1/projects", "", &listed); status != http.StatusOK || listed.Projects == nil {
			t.Fatalf("%s: list projects = %d %+v", token, status, listed)
		}
		ids := []string{}
		for _, project := range listed.Projects {
			ids = append(ids, project.ID)
		}
		return ids
	}
	if none := projects("an-administrator"); len(none) != 0 {
		t.Errorf("projects = %v, want none", none)
	}
	one, two, three := s.create("/v1/projects", `{"name": "one"}`), s.create("/v1/projects", `{"name": "two"}`), s.create("/v1/projects", `{"name": "three"}`)
	s.create("/v1/projects/"+one+"/members", `{"user": "alice@example.com", "role": "viewer"}`)
	s.create("/v1/projects/"+three+"/members", `{"group": "team-a", "role": "user"}`)
	for token, expected := range map[string][]string{
		"an-administrator": {one, two, three},
		"token-of-alice":   {one, three},
		"token-of-bob":     {},
	} {
		if listed := projects(token); !slices.Equal(listed, expected) {
			t.Errorf("%s: projects = %v, want %v", token, listed, expected)
		}
	}

	// Without an identity provider, every caller is an admin.
	nobody := install(t).supervisor()
	first, second := nobody.create("/v1/projects", `{"name": "one"}`), nobody.create("/v1/projects", `{"name": "two"}`)
	s = nobody
	if listed := projects(""); !slices.Equal(listed, []string{first, second}) {
		t.Errorf("projects without an identity provider = %v, want both", listed)
	}
}
