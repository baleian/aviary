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
		s.t.Fatalf("the members of the project are answered with %d %+v", status, listed)
	}
	return listed.Members
}

// A member is a user, by username, or a group of the identity provider, with
// a role or with custom permissions.
func TestMembers(t *testing.T) {
	s := install(t).supervisor()
	project := s.create("/v1/projects", `{"name": "test"}`)
	if members := s.members(project); len(members) != 0 {
		t.Errorf("a new project has the members %+v", members)
	}

	var team, alice member
	if status, _ := s.request(http.MethodPost, "/v1/projects/"+project+"/members", `{"group": "team-a", "role": "editor"}`, &team); status != http.StatusCreated ||
		team.Group != "team-a" || team.User != nil || team.Role != "editor" || team.Permissions != nil || team.ProjectID != project || team.ID == "" {
		t.Errorf("a group is added with %d as %+v", status, team)
	}
	// Custom permissions are completed and ordered as those of a role.
	if status, _ := s.request(http.MethodPost, "/v1/projects/"+project+"/members", `{"user": "alice@example.com", "permissions": ["sessions.control_all"]}`, &alice); status != http.StatusCreated ||
		alice.User == nil || alice.User.Username != "alice@example.com" || alice.User.ID != "" || alice.Group != "" || alice.Role != "custom" ||
		!slices.Equal(alice.Permissions, []string{"project.view", "sessions.view_all", "sessions.control_all"}) {
		t.Errorf("a user is added with %d as %+v, the user %+v", status, alice, alice.User)
	}
	if members := s.members(project); len(members) != 2 || members[0].ID != team.ID || members[1].ID != alice.ID || members[1].Role != "custom" || members[1].User == nil {
		t.Errorf("the members are %+v", members)
	}

	// A member can switch between a role and custom permissions.
	var changed member
	if status, _ := s.request(http.MethodPut, "/v1/members/"+alice.ID, `{"role": "viewer"}`, &changed); status != http.StatusOK ||
		changed.ID != alice.ID || changed.Role != "viewer" || changed.Permissions != nil || changed.User == nil || changed.User.Username != "alice@example.com" {
		t.Errorf("a member is given a role with %d as %+v", status, changed)
	}
	if status, _ := s.request(http.MethodPut, "/v1/members/"+team.ID, `{"role": "custom", "permissions": ["agents.manage"]}`, &changed); status != http.StatusOK ||
		changed.ID != team.ID || changed.Role != "custom" || !slices.Equal(changed.Permissions, []string{"project.view", "agents.manage"}) || changed.Group != "team-a" {
		t.Errorf("a member is given permissions with %d as %+v", status, changed)
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
			t.Errorf("%s %s %s is answered with %d %q, not %d %q", wrong.method, wrong.path, wrong.body, status, answer.Code, wrong.status, wrong.code)
		}
	}
	if members := s.members(project); len(members) != 2 || members[0].Role != "custom" || members[1].Role != "viewer" {
		t.Errorf("after requests that were refused, the members are %+v", members)
	}

	if status, _ := s.request(http.MethodDelete, "/v1/members/"+team.ID, "", nil); status != http.StatusNoContent {
		t.Errorf("a member is removed with %d", status)
	}
	if status, _ := s.request(http.MethodDelete, "/v1/members/"+team.ID, "", nil); status != http.StatusNotFound {
		t.Errorf("a member that is none any more is removed with %d", status)
	}
	if members := s.members(project); len(members) != 1 || members[0].ID != alice.ID {
		t.Errorf("after a member was removed, the members are %+v", members)
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
		t.Fatalf("an administrator, the user %s, adds a member with %d as %+v", admin.ID, status, added)
	}

	// Alice's first request links the membership to Alice's user.
	s.token = "token-of-alice"
	var alice me
	s.request(http.MethodGet, "/v1/me", "", &alice)
	members := s.members(project)
	if len(members) != 1 || members[0].ID != added.ID || members[0].User == nil || members[0].User.ID != alice.ID || alice.ID == "" ||
		members[0].User.Username != "alice@example.com" || members[0].User.DisplayName != "Alice Kim" || members[0].Role != "user" {
		t.Errorf("after the user %s was seen, the members are %+v", alice.ID, members)
	}

	for _, request := range [][3]string{
		{"POST", "/v1/projects/" + project + "/members", `{"group": "team-a", "role": "owner"}`},
		{"PUT", "/v1/members/" + added.ID, `{"role": "owner"}`},
		{"DELETE", "/v1/members/" + added.ID, ``},
	} {
		var answer struct{ Code string }
		if status, _ := s.request(request[0], request[1], request[2], &answer); status != http.StatusForbidden || answer.Code != "forbidden" {
			t.Errorf("a user sends %s %s, and that is answered with %d %q", request[0], request[1], status, answer.Code)
		}
	}
	if after := s.members(project); len(after) != 1 || after[0].Role != "user" {
		t.Errorf("after a user tried to change them, the members are %+v", after)
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
			t.Fatalf("the projects of the caller with %s are answered with %d %+v", token, status, listed)
		}
		ids := []string{}
		for _, project := range listed.Projects {
			ids = append(ids, project.ID)
		}
		return ids
	}
	if none := projects("an-administrator"); len(none) != 0 {
		t.Errorf("an installation without projects lists %v", none)
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
			t.Errorf("the caller with %s is listed %v, not %v", token, listed, expected)
		}
	}

	// Without an identity provider, every caller is an admin.
	nobody := install(t).supervisor()
	first, second := nobody.create("/v1/projects", `{"name": "one"}`), nobody.create("/v1/projects", `{"name": "two"}`)
	s = nobody
	if listed := projects(""); !slices.Equal(listed, []string{first, second}) {
		t.Errorf("a caller of an installation that asks nobody is listed %v", listed)
	}
}
