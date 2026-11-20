package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// defaults are the predefined roles, sorted by name.
var defaults = []store.Role{
	{Name: "auditor", Permissions: []string{"project.view", "sessions.view_all"}},
	{Name: "editor", Permissions: []string{"project.view", "agents.manage", "connections.manage", "sessions.run"}},
	{Name: "owner", Permissions: []string{"project.view", "agents.manage", "connections.manage", "sessions.run", "sessions.view_all", "sessions.control_all"}},
	{Name: "user", Permissions: []string{"project.view", "sessions.run"}},
	{Name: "viewer", Permissions: []string{"project.view"}},
}

func (suite suite) testRoles(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	// roles returns the roles that st reads, after checking them against
	// expected.
	roles := func(st supervisor, expected []store.Role) []store.Role {
		t.Helper()
		roles, err := st.Roles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(roles) != len(expected) {
			t.Fatalf("Roles = %+v, want %+v", roles, expected)
		}
		for i, role := range roles {
			if role.Name != expected[i].Name || !slices.Equal(role.Permissions, expected[i].Permissions) ||
				role.UpdatedBy != expected[i].UpdatedBy || role.UpdatedAt.IsZero() {
				t.Errorf("role %s = %+v, want %+v", expected[i].Name, role, expected[i])
			}
		}
		return roles
	}
	first := roles(st, defaults)

	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	// Permissions keep the order in which they are given.
	editor := store.Role{Name: "editor", Permissions: []string{"sessions.run", "project.view"}, UpdatedBy: user.ID}
	set, err := st.SetRole(ctx, editor.Name, editor.Permissions, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != editor.Name || !slices.Equal(set.Permissions, editor.Permissions) || set.UpdatedBy != user.ID || set.UpdatedAt.Before(first[1].UpdatedAt) {
		t.Errorf("SetRole = %+v, want %+v", set, editor)
	}
	// A role may have no permissions, and may be changed by an unauthenticated
	// request.
	viewer := store.Role{Name: "viewer"}
	if set, err := st.SetRole(ctx, viewer.Name, nil, uuid.Nil); err != nil || len(set.Permissions) != 0 || set.UpdatedBy != uuid.Nil {
		t.Errorf("SetRole without permissions = %+v, %v", set, err)
	}
	changed := roles(st.another(), []store.Role{defaults[0], editor, defaults[2], defaults[3], viewer})
	if !changed[1].UpdatedAt.Equal(set.UpdatedAt) {
		t.Errorf("SetRole UpdatedAt = %s, Roles UpdatedAt = %s", set.UpdatedAt, changed[1].UpdatedAt)
	}

	if _, err := st.SetRole(ctx, "another", []string{"project.view"}, uuid.Nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetRole of an unknown role = %v, want ErrNotFound", err)
	}
	roles(st, []store.Role{defaults[0], editor, defaults[2], defaults[3], viewer})
}
