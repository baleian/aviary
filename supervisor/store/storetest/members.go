package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// membership is a store with what the tests of the members ask of it.
type membership struct {
	supervisor
}

func (m membership) project() uuid.UUID {
	m.t.Helper()
	project, err := m.CreateProject(ctx, "test")
	if err != nil {
		m.t.Fatal(err)
	}
	return project.ID
}

func (m membership) user(subject, username string) store.User {
	m.t.Helper()
	user, err := m.UserOf(ctx, subject, username, "")
	if err != nil {
		m.t.Fatal(err)
	}
	return user
}

// add makes a member of the project: the user with the username, or the
// group if no username is given.
func (m membership) add(project uuid.UUID, username, group, role string, permissions ...string) store.Member {
	m.t.Helper()
	member, err := m.AddMember(ctx, of(project, username, group, role, permissions...), uuid.Nil)
	if err != nil {
		m.t.Fatal(err)
	}
	return member
}

func of(project uuid.UUID, username, group, role string, permissions ...string) store.Member {
	member := store.Member{ProjectID: project, Group: group, Role: role, Permissions: permissions}
	if username != "" {
		member.User = &store.User{Username: username}
	}
	return member
}

// told is what is told of a member: who it is and what it may do.
type told struct {
	// user is the ID of the user, and username the name of someone who has
	// not been seen yet.
	user                  uuid.UUID
	username, group, role string
	permissions           []string
}

// members returns what the store tells of the members of the project.
func (m membership) members(project uuid.UUID) []told {
	m.t.Helper()
	members, err := m.Members(ctx, project)
	if err != nil {
		m.t.Fatal(err)
	}
	all := []told{}
	for _, member := range members {
		one := told{group: member.Group, role: member.Role, permissions: member.Permissions}
		if member.User != nil && member.User.ID != uuid.Nil {
			one.user = member.User.ID
		} else if member.User != nil {
			one.username = member.User.Username
		}
		if member.ProjectID != project || member.ID == uuid.Nil || member.CreatedAt.IsZero() || member.UpdatedAt.IsZero() {
			m.t.Errorf("a member of the project %s is %+v", project, member)
		}
		all = append(all, one)
	}
	return all
}

func sameMembers(one, other []told) bool {
	return slices.EqualFunc(one, other, func(a, b told) bool {
		return a.user == b.user && a.username == b.username && a.group == b.group && a.role == b.role && slices.Equal(a.permissions, b.permissions)
	})
}

func (suite suite) testMembers(t *testing.T) {
	t.Parallel()
	st := membership{suite.open(t)}
	project, other := st.project(), st.project()
	alice := st.user("subject-a", "alice@example.com")
	admin := st.user("subject-z", "")

	// A group, a user who has been seen and someone who has not, in the
	// order in which they are added.
	team, err := st.AddMember(ctx, of(project, "", "team-a", "editor"), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if team.Group != "team-a" || team.User != nil || team.Role != "editor" || team.Permissions != nil || team.UpdatedBy != admin.ID || team.ProjectID != project {
		t.Errorf("a group was added as %+v", team)
	}
	known := st.add(project, "alice@example.com", "", "", "sessions.run", "project.view")
	if known.User == nil || known.User.ID != alice.ID || known.User.Subject != "subject-a" || known.User.Username != "alice@example.com" ||
		known.Role != "" || !slices.Equal(known.Permissions, []string{"sessions.run", "project.view"}) || known.UpdatedBy != uuid.Nil {
		t.Errorf("a user who has been seen was added as %+v, with the user %+v", known, known.User)
	}
	// A member with a role has no permissions of its own, whatever is given
	// with the role.
	awaited := st.add(project, "bob@example.com", "", "viewer", "sessions.run")
	if awaited.Role != "viewer" || awaited.Permissions != nil {
		t.Errorf("a member with a role and with permissions was added as %+v", awaited)
	}
	if awaited.User == nil || awaited.User.ID != uuid.Nil || awaited.User.Username != "bob@example.com" {
		t.Errorf("someone who has not been seen was added as %+v, with the user %+v", awaited, awaited.User)
	}
	expected := []told{
		{group: "team-a", role: "editor"},
		{user: alice.ID, permissions: []string{"sessions.run", "project.view"}},
		{username: "bob@example.com", role: "viewer"},
	}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("the members are %+v, not %+v", members, expected)
	}

	// A project has a group, a user and an awaited username once, and
	// another project may have them too.
	for who, again := range map[string]store.Member{
		"a group":                of(project, "", "team-a", "viewer"),
		"a user":                 of(project, "alice@example.com", "", "viewer"),
		"someone who is awaited": of(project, "bob@example.com", "", "editor"),
	} {
		if _, err := st.AddMember(ctx, again, uuid.Nil); !errors.Is(err, store.ErrMemberExists) {
			t.Errorf("%s was added again with: %v", who, err)
		}
		again.ProjectID = other
		if _, err := st.AddMember(ctx, again, uuid.Nil); err != nil {
			t.Errorf("%s was added to another project with: %v", who, err)
		}
	}
	if _, err := st.AddMember(ctx, of(uuid.New(), "", "team-a", "viewer"), uuid.Nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a member was added to a project that there is not with: %v", err)
	}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("after members were added again, the members are %+v, not %+v", members, expected)
	}

	// The user who is seen with the username that a project awaits is the
	// member, in every project that awaits it.
	bob := st.user("subject-b", "bob@example.com")
	expected[2] = told{user: bob.ID, role: "viewer"}
	if members := (membership{st.another()}).members(project); !sameMembers(members, expected) {
		t.Errorf("after the awaited user was seen, the members are %+v, not %+v", members, expected)
	}
	// They were added to the other project in no order.
	elsewhere := st.members(other)
	users := func(user uuid.UUID) int {
		return len(slices.DeleteFunc(slices.Clone(elsewhere), func(member told) bool { return member.user != user }))
	}
	if len(elsewhere) != 3 || users(alice.ID) != 1 || users(bob.ID) != 1 || users(uuid.Nil) != 1 {
		t.Errorf("the members of the other project are %+v", elsewhere)
	}

	// A member is given a role in place of its permissions, and permissions
	// in place of its role.
	set, err := st.SetMember(ctx, known.ID, "auditor", []string{"sessions.run"}, admin.ID)
	if err != nil || set.Role != "auditor" || set.Permissions != nil || set.UpdatedBy != admin.ID || set.ID != known.ID || set.User == nil || set.User.ID != alice.ID {
		t.Errorf("a member was given a role as %+v, with the error %v", set, err)
	}
	set, err = st.SetMember(ctx, team.ID, "", []string{"project.view", "agents.manage"}, uuid.Nil)
	if err != nil || set.Role != "" || !slices.Equal(set.Permissions, []string{"project.view", "agents.manage"}) || set.UpdatedBy != uuid.Nil || set.Group != "team-a" {
		t.Errorf("a member was given permissions as %+v, with the error %v", set, err)
	}
	expected[0], expected[1] = told{group: "team-a", permissions: []string{"project.view", "agents.manage"}}, told{user: alice.ID, role: "auditor"}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("after two members were changed, the members are %+v, not %+v", members, expected)
	}

	if err := st.RemoveMember(ctx, known.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveMember(ctx, known.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a member that is none any more was removed with: %v", err)
	}
	if _, err := st.SetMember(ctx, known.ID, "viewer", nil, uuid.Nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a member that is none any more was changed with: %v", err)
	}
	if members := st.members(project); !sameMembers(members, []told{expected[0], expected[2]}) {
		t.Errorf("after a member was removed, the members are %+v", members)
	}
	// The user can be made a member again.
	st.add(project, "alice@example.com", "", "user")
	if members := st.members(project); !sameMembers(members, []told{expected[0], expected[2], {user: alice.ID, role: "user"}}) {
		t.Errorf("after the user was made a member again, the members are %+v", members)
	}
}

// A username goes from one user to another, or a user is given another.
// The members follow the users and not the names.
func (suite suite) testMembersWhoseUsernamesChange(t *testing.T) {
	t.Parallel()
	st := membership{suite.open(t)}
	project, other := st.project(), st.project()
	alice := st.user("subject-a", "alice@example.com")
	st.add(project, "alice@example.com", "", "editor")
	st.add(other, "alice@example.com", "", "user")

	// Another user is given the username. That user is no member, and the
	// first one still is.
	taker := st.user("subject-c", "alice@example.com")
	if members := st.members(project); !sameMembers(members, []told{{user: alice.ID, role: "editor"}}) {
		t.Errorf("after the username of a member was given to another user, the members are %+v", members)
	}
	// The user that has the username now is made a member under it.
	st.add(project, "alice@example.com", "", "viewer")
	expected := []told{{user: alice.ID, role: "editor"}, {user: taker.ID, role: "viewer"}}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("the members are %+v, not %+v", members, expected)
	}

	// The first user is awaited under a new username in a project of which
	// it is a member, and then is given that username: the user is the
	// member that was awaited, once. In the other project it is as it was.
	st.add(project, "alice.kim@example.com", "", "auditor")
	if members := st.members(project); len(members) != 3 || members[2].username != "alice.kim@example.com" {
		t.Errorf("the members are %+v", members)
	}
	st.user("subject-a", "alice.kim@example.com")
	expected = []told{{user: taker.ID, role: "viewer"}, {user: alice.ID, role: "auditor"}}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("after the user was given the username that was awaited, the members are %+v, not %+v", members, expected)
	}
	if elsewhere := st.members(other); !sameMembers(elsewhere, []told{{user: alice.ID, role: "user"}}) {
		t.Errorf("the members of the other project are %+v", elsewhere)
	}
}
