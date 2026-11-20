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

// roles returns the roles by name.
func (s *supervisor) roles() map[string]role {
	s.t.Helper()
	var listed struct{ Roles []role }
	if status, _ := s.request(http.MethodGet, "/v1/roles", "", &listed); status != http.StatusOK {
		s.t.Fatalf("list roles: status %d", status)
	}
	roles := map[string]role{}
	for _, role := range listed.Roles {
		roles[role.Name] = role
	}
	if len(roles) != len(listed.Roles) {
		s.t.Fatalf("duplicate roles in %+v", listed.Roles)
	}
	return roles
}

// There are five built-in roles, whose permissions an admin can change.
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
			t.Errorf("role %s permissions = %q, want %q", name, roles[name].Permissions, permissions)
		}
	}
	if len(roles) != 5 {
		t.Errorf("roles = %+v, want five", roles)
	}

	// A permission set always includes project.view, sessions.control_all
	// implies sessions.view_all, and the permissions are in a fixed order.
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
			t.Errorf("PUT permissions %s = %d %+v, want %q", asked, status, changed, set)
		}
		if read := s.roles()["editor"]; !slices.Equal(read.Permissions, set) {
			t.Errorf("role after PUT %s = %+v", asked, read)
		}
	}
	if after := s.roles(); !slices.Equal(after["owner"].Permissions, roles["owner"].Permissions) || len(after) != 5 {
		t.Errorf("roles after changing editor = %+v", after)
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
			t.Errorf("PUT %s %s = %d %q, want %d %q", wrong.path, wrong.body, status, answer.Code, wrong.status, wrong.code)
		}
	}
	if read := s.roles()["editor"]; !slices.Equal(read.Permissions, []string{"project.view", "agents.manage", "sessions.run"}) {
		t.Errorf("role after refused requests = %+v", read)
	}
}

// Every caller can read the roles, but only admins can change them.
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
		t.Errorf("PUT role as a user = %d %q, want 403 forbidden", status, answer.Code)
	}
	if after := s.roles()["user"]; !slices.Equal(after.Permissions, before.Permissions) {
		t.Errorf("role after a refused change = %+v", after)
	}

	s.token = "an-administrator"
	var admin me
	s.request(http.MethodGet, "/v1/me", "", &admin)
	var changed role
	if status, _ := s.request(http.MethodPut, "/v1/roles/user", `{"permissions": ["sessions.view_all"]}`, &changed); status != http.StatusOK ||
		!slices.Equal(changed.Permissions, []string{"project.view", "sessions.view_all"}) || changed.UpdatedBy != admin.ID || admin.ID == "" {
		t.Errorf("PUT role as admin %s = %d %+v", admin.ID, status, changed)
	}
	s.token = "a-user"
	if read := s.roles()["user"]; !slices.Equal(read.Permissions, changed.Permissions) || read.UpdatedBy != admin.ID {
		t.Errorf("role after the admin's change = %+v", read)
	}
}
