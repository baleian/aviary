package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// defaults are the roles that a database has from the start, in the order
// of their names.
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
	// roles returns the roles as a Supervisor reads them, and holds them
	// against those that are expected.
	roles := func(st supervisor, expected []store.Role) []store.Role {
		t.Helper()
		roles, err := st.Roles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(roles) != len(expected) {
			t.Fatalf("the roles are %+v, not %+v", roles, expected)
		}
		for i, role := range roles {
			if role.Name != expected[i].Name || !slices.Equal(role.Permissions, expected[i].Permissions) ||
				role.UpdatedBy != expected[i].UpdatedBy || role.UpdatedAt.IsZero() {
				t.Errorf("the role %s is %+v, not %+v", expected[i].Name, role, expected[i])
			}
		}
		return roles
	}
	first := roles(st, defaults)

	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	// The permissions are kept in the order in which they are given.
	editor := store.Role{Name: "editor", Permissions: []string{"sessions.run", "project.view"}, UpdatedBy: user.ID}
	set, err := st.SetRole(ctx, editor.Name, editor.Permissions, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != editor.Name || !slices.Equal(set.Permissions, editor.Permissions) || set.UpdatedBy != user.ID || set.UpdatedAt.Before(first[1].UpdatedAt) {
		t.Errorf("the role was set to %+v, not %+v", set, editor)
	}
	// A role may have no permissions, and may be changed by nobody.
	viewer := store.Role{Name: "viewer"}
	if set, err := st.SetRole(ctx, viewer.Name, nil, uuid.Nil); err != nil || len(set.Permissions) != 0 || set.UpdatedBy != uuid.Nil {
		t.Errorf("the role was set to %+v, with the error %v", set, err)
	}
	changed := roles(st.another(), []store.Role{defaults[0], editor, defaults[2], defaults[3], viewer})
	if !changed[1].UpdatedAt.Equal(set.UpdatedAt) {
		t.Errorf("the role was changed at %s, and is read as changed at %s", set.UpdatedAt, changed[1].UpdatedAt)
	}

	if _, err := st.SetRole(ctx, "another", []string{"project.view"}, uuid.Nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a role that there is not was set with: %v", err)
	}
	roles(st, []store.Role{defaults[0], editor, defaults[2], defaults[3], viewer})
}
