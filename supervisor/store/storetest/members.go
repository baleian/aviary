package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// membership adds helpers for the tests of members.
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

// add adds the user with the username, or the group if username is empty, to
// the project.
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

// told is a member as the tests compare it.
type told struct {
	// user is set for a known user, and username for an awaited one.
	user                  uuid.UUID
	username, group, role string
	permissions           []string
}

// members returns the members of the project as told values.
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

	// A group, a known user and an awaited one, listed in the order in which
	// they are added.
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
	// A member with a role has no permissions of its own, even if some are
	// given.
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

	// A group, a user or an awaited username is a member of a project once,
	// and may be a member of another project too.
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

	// The user that gets an awaited username becomes the member in every
	// project that awaits it.
	bob := st.user("subject-b", "bob@example.com")
	expected[2] = told{user: bob.ID, role: "viewer"}
	if members := (membership{st.another()}).members(project); !sameMembers(members, expected) {
		t.Errorf("after the awaited user was seen, the members are %+v, not %+v", members, expected)
	}
	// The members of the other project were added in random order.
	elsewhere := st.members(other)
	users := func(user uuid.UUID) int {
		return len(slices.DeleteFunc(slices.Clone(elsewhere), func(member told) bool { return member.user != user }))
	}
	if len(elsewhere) != 3 || users(alice.ID) != 1 || users(bob.ID) != 1 || users(uuid.Nil) != 1 {
		t.Errorf("the members of the other project are %+v", elsewhere)
	}

	// A role replaces the permissions of a member, and permissions replace
	// its role.
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
	// A removed user can be added again.
	st.add(project, "alice@example.com", "", "user")
	if members := st.members(project); !sameMembers(members, []told{expected[0], expected[2], {user: alice.ID, role: "user"}}) {
		t.Errorf("after the user was made a member again, the members are %+v", members)
	}
}

// When usernames change, members follow the users, not the usernames.
func (suite suite) testMembersWhoseUsernamesChange(t *testing.T) {
	t.Parallel()
	st := membership{suite.open(t)}
	project, other := st.project(), st.project()
	alice := st.user("subject-a", "alice@example.com")
	st.add(project, "alice@example.com", "", "editor")
	st.add(other, "alice@example.com", "", "user")

	// Another user takes the username, but not the membership.
	taker := st.user("subject-c", "alice@example.com")
	if members := st.members(project); !sameMembers(members, []told{{user: alice.ID, role: "editor"}}) {
		t.Errorf("after the username of a member was given to another user, the members are %+v", members)
	}
	// The user that has the username now can be added under it.
	st.add(project, "alice@example.com", "", "viewer")
	expected := []told{{user: alice.ID, role: "editor"}, {user: taker.ID, role: "viewer"}}
	if members := st.members(project); !sameMembers(members, expected) {
		t.Errorf("the members are %+v, not %+v", members, expected)
	}

	// The first user, a member already, is awaited under a new username. Once
	// it gets that username, the awaited member replaces its membership. Its
	// membership in the other project stays as it was.
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

// Projects and ProjectsOf list projects in creation order.
func (suite suite) testProjects(t *testing.T) {
	t.Parallel()
	st := membership{suite.open(t)}
	if all, err := st.Projects(ctx); err != nil || len(all) != 0 {
		t.Errorf("a database without projects has %+v, with the error %v", all, err)
	}
	one, two, three, four := st.project(), st.project(), st.project(), st.project()
	alice, bob := st.user("subject-a", "alice@example.com"), st.user("subject-b", "")
	st.add(three, "alice@example.com", "", "viewer")
	st.add(one, "alice@example.com", "", "", "project.view")
	st.add(two, "", "team-a", "editor")
	st.add(three, "", "team-a", "user")
	st.add(four, "", "team-b", "user")
	st.add(four, "carol@example.com", "", "user")

	// projects returns the IDs of the listed projects.
	projects := func(list func() ([]store.Project, error)) []uuid.UUID {
		t.Helper()
		listed, err := list()
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, len(listed))
		for i, project := range listed {
			if ids[i] = project.ID; project.Name != "test" || project.CreatedAt.IsZero() {
				t.Errorf("a project is listed as %+v", project)
			}
		}
		return ids
	}
	of := func(user uuid.UUID, groups ...string) func() ([]store.Project, error) {
		return func() ([]store.Project, error) { return st.ProjectsOf(ctx, user, groups) }
	}
	for _, listed := range []struct {
		who      string
		list     func() ([]store.Project, error)
		projects []uuid.UUID
	}{
		{"all", func() ([]store.Project, error) { return st.Projects(ctx) }, []uuid.UUID{one, two, three, four}},
		{"a user", of(alice.ID), []uuid.UUID{one, three}},
		{"a user in a group", of(alice.ID, "team-b", "others"), []uuid.UUID{one, three, four}},
		{"a user in groups, one of which is a member with the user", of(alice.ID, "team-a", "team-b"), []uuid.UUID{one, two, three, four}},
		{"a user who is a member of none, in a group", of(bob.ID, "team-a"), []uuid.UUID{two, three}},
		{"a user who is a member of none, in no group", of(bob.ID), []uuid.UUID{}},
		{"nobody, in a group", of(uuid.Nil, "team-b"), []uuid.UUID{four}},
		{"nobody, in no group", of(uuid.Nil), []uuid.UUID{}},
	} {
		if ids := projects(listed.list); !slices.Equal(ids, listed.projects) {
			t.Errorf("the projects of %s are %v, not %v", listed.who, ids, listed.projects)
		}
	}
}

// The permissions of a user in a project combine those of the members that are
// the user or one of the user's groups.
func (suite suite) testPermissions(t *testing.T) {
	t.Parallel()
	st := membership{suite.open(t)}
	project, other := st.project(), st.project()
	alice, bob := st.user("subject-a", "alice@example.com"), st.user("subject-b", "")
	st.add(project, "alice@example.com", "", "", "project.view", "sessions.view_all")
	st.add(project, "", "team-a", "editor")
	st.add(project, "", "team-b", "", "sessions.control_all", "project.view")
	st.add(project, "carol@example.com", "", "owner")
	st.add(other, "", "team-a", "viewer")

	editor := []string{"agents.manage", "connections.manage", "project.view", "sessions.run"}
	for _, asked := range []struct {
		who     string
		project uuid.UUID
		user    uuid.UUID
		groups  []string
		held    []string
	}{
		{"a member with permissions of its own", project, alice.ID, nil, []string{"project.view", "sessions.view_all"}},
		{"a member in a group with a role", project, alice.ID, []string{"others", "team-a"}, []string{"agents.manage", "connections.manage", "project.view", "sessions.run", "sessions.view_all"}},
		{"a member in two groups", project, alice.ID, []string{"team-a", "team-b"}, []string{"agents.manage", "connections.manage", "project.view", "sessions.control_all", "sessions.run", "sessions.view_all"}},
		{"a user who is a member through a group", project, bob.ID, []string{"team-a"}, editor},
		{"nobody, in a group", project, uuid.Nil, []string{"team-b"}, []string{"project.view", "sessions.control_all"}},
		{"a user who is no member, in no group", project, bob.ID, nil, nil},
		{"a user who is no member, in a group that is none", project, bob.ID, []string{"others"}, nil},
		{"nobody, in no group", project, uuid.Nil, nil, nil},
		{"a member of another project", other, alice.ID, nil, nil},
		{"a group in another project", other, alice.ID, []string{"team-a"}, []string{"project.view"}},
		{"a project that there is not", uuid.New(), alice.ID, []string{"team-a"}, nil},
	} {
		if held, err := st.Permissions(ctx, asked.project, asked.user, asked.groups); err != nil || !slices.Equal(held, asked.held) {
			t.Errorf("%s may %q, not %q: %v", asked.who, held, asked.held, err)
		}
	}

	// Changing a role changes the permissions of its members, but not those
	// of members with permissions of their own.
	if _, err := st.SetRole(ctx, "editor", []string{"project.view", "sessions.run"}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if held, err := st.Permissions(ctx, project, bob.ID, []string{"team-a"}); err != nil || !slices.Equal(held, []string{"project.view", "sessions.run"}) {
		t.Errorf("after its role was changed, a member may %q: %v", held, err)
	}
	if held, err := st.Permissions(ctx, project, alice.ID, nil); err != nil || !slices.Equal(held, []string{"project.view", "sessions.view_all"}) {
		t.Errorf("after a role was changed, a member with permissions of its own may %q: %v", held, err)
	}
	// An awaited member has no permissions until its user is known.
	if held, err := st.Permissions(ctx, project, uuid.Nil, nil); err != nil || len(held) != 0 {
		t.Errorf("nobody may %q in a project that awaits a member: %v", held, err)
	}
	carol := st.user("subject-c", "carol@example.com")
	if held, err := st.Permissions(ctx, project, carol.ID, nil); err != nil || len(held) != 6 {
		t.Errorf("the member that was awaited may %q: %v", held, err)
	}
}
