package app

import (
	"net/http"
	"slices"
	"testing"

	"aviary/supervisor/internal/api"
)

// role is a role as the API returns it.
type role struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	UpdatedBy   string   `json:"updated_by"`
}

// roles asks the API for the roles, and returns them by their names.
func (s *supervisor) roles() map[string]role {
	s.t.Helper()
	var listed struct{ Roles []role }
	if status, _ := s.request(http.MethodGet, "/v1/roles", "", &listed); status != http.StatusOK {
		s.t.Fatalf("the roles are answered with %d", status)
	}
	roles := map[string]role{}
	for _, role := range listed.Roles {
		roles[role.Name] = role
	}
	if len(roles) != len(listed.Roles) {
		s.t.Fatalf("the roles are %+v", listed.Roles)
	}
	return roles
}

// An installation has five roles, and an administrator changes what a role
// may do.
func TestRoles(t *testing.T) {
	s := install(t).supervisor()
	roles := s.roles()
	for name, permissions := range map[string][]string{
		"owner":   {"project.view", "agents.manage", "connections.manage", "sessions.run", "sessions.view_all", "sessions.control_all"},
		"editor":  {"project.view", "agents.manage", "connections.manage", "sessions.run"},
		"user":    {"project.view", "sessions.run"},
		"viewer":  {"project.view"},
		"auditor": {"project.view", "sessions.view_all"},
	} {
		if !slices.Equal(roles[name].Permissions, permissions) {
			t.Errorf("the role %s may %q, not %q", name, roles[name].Permissions, permissions)
		}
	}
	if len(roles) != 5 {
		t.Errorf("the roles are %+v", roles)
	}

	// Every set has the permission to see the project, a set with the
	// permission to control the sessions of others has the one to read
	// them, and a set is written in the order of the permissions.
	for _, given := range []struct {
		asked string
		set   []string
	}{
		{`[]`, []string{"project.view"}},
		{`["sessions.control_all"]`, []string{"project.view", "sessions.view_all", "sessions.control_all"}},
		{`["sessions.run", "agents.manage", "sessions.run", "project.view"]`, []string{"project.view", "agents.manage", "sessions.run"}},
	} {
		asked, set := given.asked, given.set
		var changed role
		if status, _ := s.request(http.MethodPut, "/v1/roles/editor", `{"permissions": `+asked+`}`, &changed); status != http.StatusOK ||
			changed.Name != "editor" || !slices.Equal(changed.Permissions, set) || changed.UpdatedBy != "" {
			t.Errorf("the role that is given %s is answered with %d %+v, not with %q", asked, status, changed, set)
		}
		if read := s.roles()["editor"]; !slices.Equal(read.Permissions, set) {
			t.Errorf("the role that was given %s is read as %+v", asked, read)
		}
	}
	if after := s.roles(); !slices.Equal(after["owner"].Permissions, roles["owner"].Permissions) || len(after) != 5 {
		t.Errorf("after one role was changed, the roles are %+v", after)
	}

	for _, wrong := range []struct {
		path, body string
		status     int
		code       string
	}{
		{"/v1/roles/editor", `{"permissions": ["sessions.run", "sessions.fly"]}`, http.StatusBadRequest, "invalid_request"},
		{"/v1/roles/editor", `{}`, http.StatusBadRequest, "invalid_request"},
		{"/v1/roles/editor", `{"permissions": "sessions.run"}`, http.StatusBadRequest, "invalid_request"},
		{"/v1/roles/another", `{"permissions": ["sessions.run"]}`, http.StatusNotFound, "not_found"},
	} {
		var answer struct{ Code string }
		if status, _ := s.request(http.MethodPut, wrong.path, wrong.body, &answer); status != wrong.status || answer.Code != wrong.code {
			t.Errorf("PUT %s %s is answered with %d %q, not %d %q", wrong.path, wrong.body, status, answer.Code, wrong.status, wrong.code)
		}
	}
	if read := s.roles()["editor"]; !slices.Equal(read.Permissions, []string{"project.view", "agents.manage", "sessions.run"}) {
		t.Errorf("after requests that were refused, the role is %+v", read)
	}
}

// Everyone who may use the API reads the roles, and only an administrator
// changes one.
func TestRolesAreChangedByAdministrators(t *testing.T) {
	installation := install(t)
	installation.callers = api.Callers{
		Provider: tokens{"an-administrator": {Subject: "1", Groups: []string{"platform"}}, "a-user": {Subject: "2"}},
		Admins:   []string{"platform"},
	}
	s := installation.supervisor()
	s.token = "a-user"
	before := s.roles()["user"]
	var answer struct{ Code string }
	if status, _ := s.request(http.MethodPut, "/v1/roles/user", `{"permissions": ["sessions.view_all"]}`, &answer); status != http.StatusForbidden || answer.Code != "forbidden" {
		t.Errorf("a user changes a role, and that is answered with %d %q", status, answer.Code)
	}
	if after := s.roles()["user"]; !slices.Equal(after.Permissions, before.Permissions) {
		t.Errorf("after a user tried to change it, the role is %+v", after)
	}

	s.token = "an-administrator"
	var admin me
	s.request(http.MethodGet, "/v1/me", "", &admin)
	var changed role
	if status, _ := s.request(http.MethodPut, "/v1/roles/user", `{"permissions": ["sessions.view_all"]}`, &changed); status != http.StatusOK ||
		!slices.Equal(changed.Permissions, []string{"project.view", "sessions.view_all"}) || changed.UpdatedBy != admin.ID || admin.ID == "" {
		t.Errorf("an administrator, the user %s, changes a role, and that is answered with %d %+v", admin.ID, status, changed)
	}
	s.token = "a-user"
	if read := s.roles()["user"]; !slices.Equal(read.Permissions, changed.Permissions) || read.UpdatedBy != admin.ID {
		t.Errorf("the role that an administrator changed is read as %+v", read)
	}
}
