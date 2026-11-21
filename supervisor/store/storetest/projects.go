package storetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// A project has a slug that no other project has, and a name that may be
// empty.
func (suite suite) testProjectSlugs(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	named, err := st.CreateProject(ctx, store.Project{Slug: "demo", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	unnamed, err := st.CreateProject(ctx, store.Project{Slug: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.another().Projects(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("Projects = %+v, want two: %v", listed, err)
	}
	for i, expected := range []struct {
		created    store.Project
		slug, name string
	}{
		{named, "demo", "Demo"},
		{unnamed, "plain", ""},
	} {
		read, err := st.another().Project(ctx, expected.created.ID)
		if err != nil {
			t.Fatal(err)
		}
		for how, project := range map[string]store.Project{"created": expected.created, "read": read, "listed": listed[i]} {
			if project.ID != expected.created.ID || project.Slug != expected.slug || project.Name != expected.name ||
				project.CreatedAt.IsZero() || !project.CreatedAt.Equal(expected.created.CreatedAt) {
				t.Errorf("%s project = %+v, want slug %q and name %q", how, project, expected.slug, expected.name)
			}
		}
	}

	// Another Supervisor refuses a taken slug, whatever the name.
	for _, refused := range []store.Project{{Slug: "demo"}, {Slug: "demo", Name: "Demo"}, {Slug: "plain", Name: "Plain"}} {
		if _, err := st.another().CreateProject(ctx, refused); !errors.Is(err, store.ErrSlugTaken) {
			t.Errorf("CreateProject(%+v) = %v, want ErrSlugTaken", refused, err)
		}
	}
	st.contend("CreateProject", func(s supervisor) error {
		_, err := s.CreateProject(ctx, store.Project{Slug: "contested"})
		return err
	})
	if all, err := st.Projects(ctx); err != nil || len(all) != 3 {
		t.Errorf("Projects after refused creations = %+v, want three: %v", all, err)
	}
}

// An agent has a slug that no other agent of its project has. Agents of other
// projects may have it.
func (suite suite) testAgentSlugs(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, other := st.project(), st.project()
	// model returns a model of a new provider of the project.
	model := func(project store.Project) store.Model {
		t.Helper()
		provider, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
		if err != nil {
			t.Fatal(err)
		}
		return store.Model{Provider: provider.ID, Name: "a-model"}
	}
	ours, theirs := model(project), model(other)
	if _, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: "reviewer", Harness: "opencode", Model: ours}); err != nil {
		t.Fatal(err)
	}
	refused := store.Agent{Slug: "reviewer", Name: "Another reviewer", Harness: "codex", Model: ours}
	if _, err := st.another().CreateAgent(ctx, project.ID, refused); !errors.Is(err, store.ErrSlugTaken) {
		t.Errorf("CreateAgent with a slug taken in the project = %v, want ErrSlugTaken", err)
	}
	elsewhere, err := st.CreateAgent(ctx, other.ID, store.Agent{Slug: "reviewer", Harness: "opencode", Model: theirs})
	if err != nil || elsewhere.ProjectID != other.ID || elsewhere.Slug != "reviewer" {
		t.Errorf("CreateAgent with the slug in another project = %+v: %v", elsewhere, err)
	}
	st.contend("CreateAgent", func(s supervisor) error {
		_, err := s.CreateAgent(ctx, project.ID, store.Agent{Slug: "contested", Harness: "opencode", Model: ours})
		return err
	})
	for _, listed := range []struct {
		project store.Project
		slugs   []string
	}{
		{project, []string{"reviewer", "contested"}},
		{other, []string{"reviewer"}},
	} {
		agents, err := st.Agents(ctx, listed.project.ID)
		var slugs []string
		for _, agent := range agents {
			slugs = append(slugs, agent.Slug)
		}
		if err != nil || !slices.Equal(slugs, listed.slugs) {
			t.Errorf("Agents of project %s = %v, want %v: %v", listed.project.Slug, slugs, listed.slugs, err)
		}
	}
}

// contend has several Supervisors call create at once, and fails the test
// unless one call succeeds and the others return ErrSlugTaken.
func (s supervisor) contend(what string, create func(supervisor) error) {
	s.t.Helper()
	supervisors := s.supervisors(8)
	var created, taken atomic.Int32
	atOnce(len(supervisors), func(i int) {
		switch err := create(supervisors[i]); {
		case err == nil:
			created.Add(1)
		case errors.Is(err, store.ErrSlugTaken):
			taken.Add(1)
		default:
			s.t.Error(err)
		}
	})
	if created.Load() != 1 || int(taken.Load()) != len(supervisors)-1 {
		s.t.Errorf("concurrent %s with one slug: %d succeeded and %d returned ErrSlugTaken, want 1 and %d",
			what, created.Load(), taken.Load(), len(supervisors)-1)
	}
}

// Agents and sessions are stored as they are created.
func (suite suite) testAgents(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	provider, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	given := store.Agent{
		Slug: "reviewer", Name: "Code reviewer", Description: "Reviews a branch before it is merged.", Harness: "opencode",
		Instructions: "Be brief.", Approval: "ask", Model: store.Model{Provider: provider.ID, Name: "a-model"},
	}
	created, err := st.CreateAgent(ctx, project.ID, given)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.Agent(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for how, agent := range map[string]store.Agent{"created": created, "read": read} {
		if agent.ID == uuid.Nil || agent.ID != created.ID || agent.ProjectID != project.ID || agent.Slug != "reviewer" ||
			agent.Name != "Code reviewer" || agent.Description != "Reviews a branch before it is merged." || agent.Harness != "opencode" ||
			agent.Instructions != "Be brief." || agent.Approval != "ask" ||
			agent.Model != given.Model || agent.CreatedAt.IsZero() || !agent.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("%s agent = %+v, model %+v", how, agent, agent.Model)
		}
	}
	// An agent with only a slug, a harness and a model.
	plain, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: "plain", Harness: "codex", Model: given.Model})
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Agent(ctx, plain.ID); err != nil || read.Slug != "plain" || read.Name != "" || read.Description != "" ||
		read.Harness != "codex" || read.Instructions != "" || read.Approval != "" || read.Model != given.Model {
		t.Errorf("agent with only a slug, a harness and a model = %+v: %v", read, err)
	}

	session, err := st.CreateSession(ctx, created.ID, uuid.Nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Session(ctx, session.ID); err != nil || read.ID != session.ID || read.AgentID != created.ID ||
		session.AgentID != created.ID || session.CreatedAt.IsZero() || !read.CreatedAt.Equal(session.CreatedAt) {
		t.Errorf("created session %+v, read %+v: %v", session, read, err)
	}
}

// Agents lists the agents of a project in creation order, as Agent reads
// them.
func (suite suite) testListAgents(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, other := st.project(), st.project()
	if agents, err := st.Agents(ctx, project.ID); err != nil || agents == nil || len(agents) != 0 {
		t.Errorf("Agents of a project without agents = %+v, %v, want empty", agents, err)
	}
	provider, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: other.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	model := store.Model{Provider: provider.ID, Name: "a-model"}
	var created []store.Agent
	for _, given := range []struct {
		project uuid.UUID
		agent   store.Agent
	}{
		{project.ID, store.Agent{Slug: "first", Name: "First", Description: "The first of three.", Harness: "opencode", Instructions: "Be brief.", Approval: "ask", Model: model}},
		{other.ID, store.Agent{Slug: "first", Harness: "opencode", Model: store.Model{Provider: theirs.ID, Name: "a-model"}}},
		{project.ID, store.Agent{Slug: "second", Harness: "codex", Model: store.Model{Provider: provider.ID, Name: "another-model"}}},
		{project.ID, store.Agent{Slug: "third", Harness: "opencode", Model: model}},
	} {
		agent, err := st.CreateAgent(ctx, given.project, given.agent)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, agent)
	}
	for _, listed := range []struct {
		project uuid.UUID
		agents  []store.Agent
	}{
		{project.ID, []store.Agent{created[0], created[2], created[3]}},
		{other.ID, []store.Agent{created[1]}},
		{uuid.New(), []store.Agent{}},
	} {
		agents, err := st.Agents(ctx, listed.project)
		if err != nil || len(agents) != len(listed.agents) {
			t.Fatalf("want %d agents, got %+v: %v", len(listed.agents), agents, err)
		}
		for i, agent := range agents {
			read, err := st.Agent(ctx, listed.agents[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			if agent.ID != read.ID || agent.ProjectID != listed.project || agent.Slug != read.Slug || agent.Name != read.Name ||
				agent.Description != read.Description || agent.Harness != read.Harness ||
				agent.Instructions != read.Instructions || agent.Approval != read.Approval ||
				agent.Model != read.Model || !agent.CreatedAt.Equal(read.CreatedAt) {
				t.Errorf("agent %d: listed %+v, read %+v", i, agent, read)
			}
		}
	}
}

// SetSandbox is a compare-and-swap: of several Supervisors that set a
// session's sandbox at once, one succeeds.
func (suite suite) testSetSandbox(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	// A sandbox that is set is awake.
	has := func(sandbox string) {
		t.Helper()
		state := ""
		if sandbox != "" {
			state = store.SandboxAwake
		}
		if session, err := st.another().Session(ctx, id); err != nil || session.Sandbox != sandbox || session.SandboxState != state {
			t.Errorf("sandbox = %q %q, want %q %q: %v", session.Sandbox, session.SandboxState, sandbox, state, err)
		}
	}
	has("")
	for _, step := range []struct {
		from, to string
		set      bool
		then     string
	}{
		{from: "", to: "sandbox-1", set: true, then: "sandbox-1"},
		// Another Supervisor created a sandbox for the session too.
		{from: "", to: "sandbox-2", then: "sandbox-1"},
		// The sandbox is deleted, and the session gets the next one.
		{from: "sandbox-2", to: "", then: "sandbox-1"},
		{from: "sandbox-1", to: "", set: true, then: ""},
		{from: "sandbox-1", to: "", then: ""},
		{from: "", to: "sandbox-3", set: true, then: "sandbox-3"},
		{from: "sandbox-3", to: "sandbox-3", set: true, then: "sandbox-3"},
	} {
		set, err := st.SetSandbox(ctx, id, step.from, step.to)
		if err != nil || set != step.set {
			t.Errorf("SetSandbox(%q, %q) = %t, %v", step.from, step.to, set, err)
		}
		has(step.then)
	}
	if _, err := st.SetSandbox(ctx, uuid.New(), "", "sandbox-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSandbox of a nonexistent session = %v, want ErrNotFound", err)
	}
	// Sessions returns the sandboxes too.
	session, err := st.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := st.Sessions(ctx, session.AgentID, uuid.Nil, uuid.Max, 10)
	if err != nil || len(sessions) != 1 || sessions[0].Sandbox != "sandbox-3" {
		t.Errorf("Sessions = %+v, want one with sandbox-3: %v", sessions, err)
	}

	fresh, supervisors := st.session(), st.supervisors(8)
	var given atomic.Int32
	atOnce(len(supervisors), func(i int) {
		set, err := supervisors[i].SetSandbox(ctx, fresh, "", fmt.Sprintf("sandbox-of-%d", i))
		if err != nil {
			t.Error(err)
		}
		if set {
			given.Add(1)
		}
	})
	if given.Load() != 1 {
		t.Errorf("%d of 8 concurrent SetSandbox calls succeeded, want 1", given.Load())
	}
}

// SetSandboxState records the state of the sandbox that the session has, and
// of no other.
func (suite suite) testSetSandboxState(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	for _, step := range []struct {
		// from and to change the session's sandbox, if they differ. Otherwise
		// the state of sandbox is recorded.
		from, to       string
		sandbox, state string
		then           string
	}{
		// A session without a sandbox has no state.
		{sandbox: "sandbox-1", state: store.SandboxAsleep, then: ""},
		{sandbox: "", state: store.SandboxAsleep, then: ""},
		{from: "", to: "sandbox-1", then: store.SandboxAwake},
		{sandbox: "sandbox-1", state: store.SandboxAsleep, then: store.SandboxAsleep},
		{sandbox: "sandbox-1", state: store.SandboxAsleep, then: store.SandboxAsleep},
		// A sandbox that the session does not have, such as an earlier one.
		{sandbox: "sandbox-0", state: store.SandboxAwake, then: store.SandboxAsleep},
		{sandbox: "sandbox-1", state: store.SandboxAwake, then: store.SandboxAwake},
		{sandbox: "sandbox-1", state: store.SandboxLost, then: store.SandboxLost},
		// The next sandbox is awake, whatever the one before it was.
		{from: "sandbox-1", to: "", then: ""},
		{sandbox: "sandbox-1", state: store.SandboxAsleep, then: ""},
		{from: "", to: "sandbox-2", then: store.SandboxAwake},
		{sandbox: "sandbox-2", state: store.SandboxAsleep, then: store.SandboxAsleep},
	} {
		if step.from != step.to {
			if set, err := st.SetSandbox(ctx, id, step.from, step.to); err != nil || !set {
				t.Fatalf("SetSandbox(%q, %q) = %t, %v", step.from, step.to, set, err)
			}
		} else if err := st.SetSandboxState(ctx, id, step.sandbox, step.state); err != nil {
			t.Fatalf("SetSandboxState(%q, %q) = %v", step.sandbox, step.state, err)
		}
		if session, err := st.another().Session(ctx, id); err != nil || session.SandboxState != step.then {
			t.Errorf("sandbox state after %+v = %q, want %q: %v", step, session.SandboxState, step.then, err)
		}
	}
	if err := st.SetSandboxState(ctx, uuid.New(), "sandbox-2", store.SandboxAwake); err != nil {
		t.Errorf("SetSandboxState of a nonexistent session = %v", err)
	}

	// Sessions and EndSession return the state too.
	session, err := st.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := st.Sessions(ctx, session.AgentID, uuid.Nil, uuid.Max, 10)
	if err != nil || len(sessions) != 1 || sessions[0].SandboxState != store.SandboxAsleep {
		t.Errorf("Sessions = %+v, want one that is asleep: %v", sessions, err)
	}
	if ended, err := st.EndSession(ctx, id); err != nil || ended.Sandbox != "sandbox-2" || ended.SandboxState != store.SandboxAsleep {
		t.Errorf("EndSession = %+v, want it with sandbox-2 asleep: %v", ended, err)
	}
}

// SessionsWithSandbox returns the sessions that have a sandbox, ended or not,
// and no others.
func (suite suite) testSessionsWithSandbox(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	if sessions, err := st.SessionsWithSandbox(ctx); err != nil || len(sessions) != 0 {
		t.Errorf("SessionsWithSandbox of a database without sessions = %+v, %v, want none", sessions, err)
	}
	set := func(id uuid.UUID, from, to string) {
		t.Helper()
		if set, err := st.SetSandbox(ctx, id, from, to); err != nil || !set {
			t.Fatalf("SetSandbox(%q, %q) = %t, %v", from, to, set, err)
		}
	}
	awake, asleep, ended, deleted := st.session(), st.session(), st.session(), st.session()
	st.session()
	set(awake, "", "sandbox-1")
	set(asleep, "", "sandbox-2")
	if err := st.SetSandboxState(ctx, asleep, "sandbox-2", store.SandboxAsleep); err != nil {
		t.Fatal(err)
	}
	set(ended, "", "sandbox-3")
	if _, err := st.EndSession(ctx, ended); err != nil {
		t.Fatal(err)
	}
	set(deleted, "", "sandbox-4")
	set(deleted, "sandbox-4", "")

	sessions, err := st.another().SessionsWithSandbox(ctx)
	if err != nil || len(sessions) != 3 {
		t.Fatalf("SessionsWithSandbox = %+v, %v, want three sessions", sessions, err)
	}
	for _, session := range sessions {
		read, err := st.Session(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		var sandbox, state string
		switch session.ID {
		case awake:
			sandbox, state = "sandbox-1", store.SandboxAwake
		case asleep:
			sandbox, state = "sandbox-2", store.SandboxAsleep
		case ended:
			sandbox, state = "sandbox-3", store.SandboxAwake
		}
		if session.Sandbox != sandbox || session.SandboxState != state || (session.EndedAt != nil) != (session.ID == ended) ||
			session.AgentID != read.AgentID || !session.CreatedAt.Equal(read.CreatedAt) {
			t.Errorf("listed session %+v, ended at %v; want sandbox %q, recorded as %q, of session %+v", session, session.EndedAt, sandbox, state, read)
		}
	}
}

// Sessions pages through the sessions of an agent newest first, all of them
// or those of one user.
func (suite suite) testListSessions(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	var users []uuid.UUID
	for _, subject := range []string{"subject-a", "subject-b", "subject-c"} {
		user, err := st.UserOf(ctx, subject, "", "")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, user.ID)
	}
	alice, bob, carol := users[0], users[1], users[2]
	first, err := st.Session(ctx, st.session())
	if err != nil {
		t.Fatal(err)
	}
	agent := first.AgentID
	if sessions, err := st.Sessions(ctx, uuid.New(), uuid.Nil, uuid.Max, 10); err != nil || sessions == nil || len(sessions) != 0 {
		t.Errorf("Sessions of a nonexistent agent = %+v, %v, want empty", sessions, err)
	}
	// The sessions of the agent, oldest first, created by nobody and by two
	// users.
	created := []store.Session{first}
	for _, by := range []uuid.UUID{alice, bob, alice, uuid.Nil, alice} {
		session, err := st.CreateSession(ctx, agent, by, "")
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, session)
	}
	elsewhere, err := st.Session(ctx, st.session())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, elsewhere.AgentID, alice, ""); err != nil {
		t.Fatal(err)
	}

	for _, listed := range []struct {
		what   string
		by     uuid.UUID
		before uuid.UUID
		limit  int
		// sessions are indexes into created.
		sessions []int
	}{
		{"all", uuid.Nil, uuid.Max, 10, []int{5, 4, 3, 2, 1, 0}},
		{"limit equal to count", uuid.Nil, uuid.Max, 6, []int{5, 4, 3, 2, 1, 0}},
		{"newest", uuid.Nil, uuid.Max, 2, []int{5, 4}},
		{"before a session", uuid.Nil, created[4].ID, 3, []int{3, 2, 1}},
		{"before the second", uuid.Nil, created[1].ID, 3, []int{0}},
		{"before the first", uuid.Nil, created[0].ID, 3, []int{}},
		{"by user", alice, uuid.Max, 10, []int{5, 3, 1}},
		{"newest by user", alice, uuid.Max, 2, []int{5, 3}},
		{"by user before own session", alice, created[3].ID, 10, []int{1}},
		{"by user before another user's session", alice, created[4].ID, 10, []int{3, 1}},
		{"by another user", bob, uuid.Max, 10, []int{2}},
		{"by user without sessions", carol, uuid.Max, 10, []int{}},
	} {
		sessions, err := st.Sessions(ctx, agent, listed.by, listed.before, listed.limit)
		if err != nil || sessions == nil {
			t.Fatalf("%s: Sessions = %+v, %v", listed.what, sessions, err)
		}
		var answer []int
		for _, session := range sessions {
			at := slices.IndexFunc(created, func(one store.Session) bool { return one.ID == session.ID })
			if at < 0 || session.AgentID != agent || session.CreatedBy != created[at].CreatedBy || !session.CreatedAt.Equal(created[at].CreatedAt) {
				t.Errorf("%s: unexpected session %+v", listed.what, session)
			}
			answer = append(answer, at)
		}
		if !slices.Equal(answer, listed.sessions) {
			t.Errorf("%s: sessions = %v, want %v", listed.what, answer, listed.sessions)
		}
	}
}

// A session has the title that it was created with, which may be empty. The
// title can be changed and emptied, also once the session has ended.
func (suite suite) testSessionTitles(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	untitled, err := st.Session(ctx, st.session())
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, untitled.AgentID, uuid.Nil, "Fix the build")
	if err != nil {
		t.Fatal(err)
	}
	// titled checks the title of the session that another Supervisor reads
	// and lists.
	other := st.another()
	titled := func(id uuid.UUID, want string) {
		t.Helper()
		read, err := other.Session(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := other.Sessions(ctx, untitled.AgentID, uuid.Nil, uuid.Max, 10)
		at := slices.IndexFunc(listed, func(one store.Session) bool { return one.ID == id })
		if err != nil || at < 0 {
			t.Fatalf("Sessions = %+v, want one with ID %v: %v", listed, id, err)
		}
		for how, got := range map[string]store.Session{"read": read, "listed": listed[at]} {
			if got.Title != want {
				t.Errorf("%s title = %q, want %q", how, got.Title, want)
			}
		}
	}
	if untitled.Title != "" || session.Title != "Fix the build" {
		t.Errorf("titles of new sessions = %q and %q, want empty and %q", untitled.Title, session.Title, "Fix the build")
	}
	titled(untitled.ID, "")
	titled(session.ID, "Fix the build")

	// The first title is the one that the session has already.
	for _, title := range []string{"Fix the build", "Fix the tests", "빌드 고치기", ""} {
		set, err := st.SetSessionTitle(ctx, session.ID, title)
		if err != nil || set.ID != session.ID || set.Title != title || !set.CreatedAt.Equal(session.CreatedAt) {
			t.Errorf("SetSessionTitle(%q) = %+v, %v", title, set, err)
		}
		titled(session.ID, title)
	}
	titled(untitled.ID, "")

	if _, err := st.EndSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if set, err := st.SetSessionTitle(ctx, session.ID, "Fixed"); err != nil || set.Title != "Fixed" || set.EndedAt == nil {
		t.Errorf("SetSessionTitle of an ended session = %+v, %v", set, err)
	}
	titled(session.ID, "Fixed")
}

// A session can be ended once it has no unfinished turn. It then accepts no
// messages and keeps its turns.
func (suite suite) testEndSession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	if set, err := st.SetSandbox(ctx, id, "", "sandbox-1"); err != nil || !set {
		t.Fatalf("SetSandbox did not set the sandbox: %v", err)
	}

	// A session with an unfinished turn cannot be ended.
	st.queue(id)
	busy := func(turn string) {
		t.Helper()
		if _, err := st.EndSession(ctx, id); !errors.Is(err, store.ErrSessionBusy) {
			t.Errorf("EndSession with a %s turn = %v, want ErrSessionBusy", turn, err)
		}
		if session, err := st.another().Session(ctx, id); err != nil || session.EndedAt != nil {
			t.Errorf("session with a %s turn ended at %v: %v", turn, session.EndedAt, err)
		}
	}
	busy("queued")
	dispatch := st.dispatch()
	busy("dispatched")
	if _, err := st.FollowTurn(ctx, dispatch.TurnID); err != nil {
		t.Fatal(err)
	}
	busy("running")
	st.end(dispatch.TurnID, true, store.Failure{})

	ended, err := st.EndSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ended.ID != id || ended.EndedAt == nil || ended.EndedAt.Before(ended.CreatedAt) || ended.Sandbox != "sandbox-1" {
		t.Fatalf("EndSession = %+v, ended at %v", ended, ended.EndedAt)
	}
	// Ending it again returns it as it ended.
	again, err := st.another().EndSession(ctx, id)
	if err != nil || again.EndedAt == nil || !again.EndedAt.Equal(*ended.EndedAt) || again.Sandbox != "sandbox-1" {
		t.Errorf("second EndSession = %+v, ended at %v, want %v: %v", again, again.EndedAt, ended.EndedAt, err)
	}
	// Session and Sessions return it ended too.
	read, err := st.another().Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := st.Sessions(ctx, ended.AgentID, uuid.Nil, uuid.Max, 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions = %+v, want one: %v", sessions, err)
	}
	for how, session := range map[string]store.Session{"read": read, "listed": sessions[0]} {
		if session.EndedAt == nil || !session.EndedAt.Equal(*ended.EndedAt) {
			t.Errorf("%s session ended at %v, want %v", how, session.EndedAt, ended.EndedAt)
		}
	}

	// It refuses messages with ErrSessionEnded, even when the queue is full.
	if _, _, err := st.CreateTurn(ctx, id, uuid.Nil, "again", 0, 50); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("CreateTurn on an ended session = %v, want ErrSessionEnded", err)
	}
	st.queue(st.session())
	if _, _, err := st.CreateTurn(ctx, id, uuid.Nil, "again", 1, 0); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("CreateTurn on an ended session with a full queue = %v, want ErrSessionEnded", err)
	}
	if turns, err := st.Turns(ctx, id, math.MaxInt32, 10); err != nil || len(turns) != 1 || turns[0].ID != dispatch.TurnID {
		t.Errorf("Turns of the ended session = %+v: %v", turns, err)
	}

	// A session without turns can be ended; a missing one cannot.
	if fresh, err := st.EndSession(ctx, st.session()); err != nil || fresh.EndedAt == nil || fresh.Sandbox != "" {
		t.Errorf("EndSession of a session without turns = %+v, ended at %v: %v", fresh, fresh.EndedAt, err)
	}
	if _, err := st.EndSession(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("EndSession of a nonexistent session = %v, want ErrNotFound", err)
	}
}

// Concurrent ends and messages never leave an ended session with an
// unfinished turn.
func (suite suite) testConcurrentEndSessionAndCreateTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	supervisors := st.supervisors(8)
	for range 20 {
		id := st.session()
		var ended, accepted atomic.Int32
		atOnce(len(supervisors), func(i int) {
			if i%2 == 0 {
				switch _, err := supervisors[i].EndSession(ctx, id); {
				case err == nil:
					ended.Add(1)
				case !errors.Is(err, store.ErrSessionBusy):
					t.Error(err)
				}
				return
			}
			switch _, _, err := supervisors[i].CreateTurn(ctx, id, uuid.Nil, "hello", 0, 50); {
			case err == nil:
				accepted.Add(1)
			case !errors.Is(err, store.ErrSessionBusy) && !errors.Is(err, store.ErrSessionEnded):
				t.Error(err)
			}
		})
		session, err := st.Session(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		turns, err := st.Turns(ctx, id, math.MaxInt32, 10)
		if err != nil {
			t.Fatal(err)
		}
		// Either every end succeeded and no message was accepted, or one
		// message was accepted and no end succeeded.
		all := int32(len(supervisors) / 2)
		if session.EndedAt != nil && (len(turns) != 0 || accepted.Load() != 0 || ended.Load() != all) ||
			session.EndedAt == nil && (len(turns) != 1 || accepted.Load() != 1 || ended.Load() != 0) {
			t.Fatalf("concurrent EndSession and CreateTurn: session ended at %v with %d turns, %d ends and %d messages succeeded",
				session.EndedAt, len(turns), ended.Load(), accepted.Load())
		}
	}
}

// A provider is created with the ID that it is given, and replaced whole.
func (suite suite) testProviders(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	given := store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key")}
	created, err := st.CreateProvider(ctx, given)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != given.ID || created.ProjectID != project.ID || created.Name != given.Name || created.URL != given.URL ||
		!bytes.Equal(created.APIKey, given.APIKey) || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreateProvider = %+v", created)
	}
	if read, err := st.Provider(ctx, given.ID); err != nil || !same(read, created) {
		t.Errorf("Provider = %+v, want %+v: %v", read, created, err)
	}

	// A provider given no key has none afterwards.
	replaced, err := st.ReplaceProvider(ctx, store.Provider{ID: given.ID, Name: "renamed", URL: "http://model.example.com:8000", APIKey: []byte("another")})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != given.ID || replaced.ProjectID != project.ID || replaced.Name != "renamed" || replaced.URL != "http://model.example.com:8000" ||
		string(replaced.APIKey) != "another" || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("ReplaceProvider = %+v, was %+v", replaced, created)
	}
	keyless, err := st.ReplaceProvider(ctx, store.Provider{ID: given.ID, Name: "renamed", URL: "http://model.example.com:8000"})
	if err != nil || keyless.APIKey != nil {
		t.Errorf("ReplaceProvider without a key = %+v: %v", keyless, err)
	}
	if read, err := st.Provider(ctx, given.ID); err != nil || !same(read, keyless) {
		t.Errorf("Provider after ReplaceProvider = %+v, want %+v: %v", read, keyless, err)
	}
	// A provider cannot be created twice.
	if _, err := st.CreateProvider(ctx, given); err == nil {
		t.Error("second CreateProvider with the same ID succeeded")
	}
	// A provider without a key has a nil key, not an empty one.
	local, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "local", URL: "http://10.0.0.5:8000/v1"})
	if err != nil || local.APIKey != nil {
		t.Errorf("CreateProvider without a key = %+v: %v", local, err)
	}

	none := uuid.New()
	for name, err := range map[string]error{
		"Provider":           second(st.Provider(ctx, none)),
		"ReplaceProvider":    second(st.ReplaceProvider(ctx, store.Provider{ID: none, Name: "a provider", URL: "https://api.example.com"})),
		"CreateProvider":     second(st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: none, Name: "a provider", URL: "https://api.example.com"})),
		"SecretStore":        second(st.SecretStore(ctx, none)),
		"ReplaceSecretStore": second(st.ReplaceSecretStore(ctx, store.SecretStore{ID: none, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed")})),
		"CreateSecretStore":  second(st.CreateSecretStore(ctx, store.SecretStore{ID: uuid.New(), ProjectID: none, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed")})),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}

// same reports whether two providers have equal stored fields.
func same(one, other store.Provider) bool {
	return one.ID == other.ID && one.ProjectID == other.ProjectID && one.Name == other.Name && one.URL == other.URL &&
		bytes.Equal(one.APIKey, other.APIKey) && (one.APIKey == nil) == (other.APIKey == nil) &&
		reflect.DeepEqual(one.APIKeyAt, other.APIKeyAt) &&
		one.CreatedAt.Equal(other.CreatedAt) && one.UpdatedAt.Equal(other.UpdatedAt)
}

// The key of a provider can be in a secret store instead of stored.
func (suite suite) testProviderKeyInSecretStore(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	secrets, err := st.CreateSecretStore(ctx, store.SecretStore{
		ID: uuid.New(), ProjectID: project.ID, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{"where": "there"}`), Credential: []byte("sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	at := &store.SecretReference{Store: secrets.ID, Path: "llm/a-provider", Key: "api_key"}
	created, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKeyAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if created.APIKey != nil || !reflect.DeepEqual(created.APIKeyAt, at) {
		t.Errorf("CreateProvider = %+v, key at %+v", created, created.APIKeyAt)
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || !same(read, created) {
		t.Errorf("Provider = %+v, want %+v: %v", read, created, err)
	}

	// Replacing switches between a stored key and one in a secret store.
	replacement := store.Provider{ID: created.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key")}
	if stored, err := st.ReplaceProvider(ctx, replacement); err != nil || stored.APIKeyAt != nil || string(stored.APIKey) != "a sealed key" {
		t.Errorf("ReplaceProvider with a stored key = %+v, key at %+v: %v", stored, stored.APIKeyAt, err)
	}
	elsewhere := &store.SecretReference{Store: secrets.ID, Path: "llm/another", Key: "key"}
	replacement.APIKey, replacement.APIKeyAt = nil, elsewhere
	if kept, err := st.ReplaceProvider(ctx, replacement); err != nil || kept.APIKey != nil || !reflect.DeepEqual(kept.APIKeyAt, elsewhere) {
		t.Errorf("ReplaceProvider with a key in a secret store = %+v, key at %+v: %v", kept, kept.APIKeyAt, err)
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || read.APIKey != nil || !reflect.DeepEqual(read.APIKeyAt, elsewhere) {
		t.Errorf("Provider after ReplaceProvider = %+v, key at %+v: %v", read, read.APIKeyAt, err)
	}

	// The secret store of the key must exist.
	nowhere := &store.SecretReference{Store: uuid.New(), Path: "llm/a-provider", Key: "api_key"}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "another", URL: "https://api.example.com/v1", APIKeyAt: nowhere}); err == nil {
		t.Error("CreateProvider succeeded with a key in a nonexistent secret store")
	}
	replacement.APIKeyAt = nowhere
	if _, err := st.ReplaceProvider(ctx, replacement); err == nil {
		t.Error("ReplaceProvider succeeded with a key in a nonexistent secret store")
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || !reflect.DeepEqual(read.APIKeyAt, elsewhere) {
		t.Errorf("APIKeyAt after a failed ReplaceProvider = %+v, want unchanged: %v", read.APIKeyAt, err)
	}

	// The origin of a running turn has the location of the provider's key,
	// and the secret store as it is then.
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "opencode", Model: store.Model{Provider: created.ID, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, agent.ID, uuid.Nil, "")
	if err != nil {
		t.Fatal(err)
	}
	st.running(session.ID)
	origin, err := st.SessionTurn(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.Provider.APIKey != nil || !reflect.DeepEqual(origin.Provider.APIKeyAt, elsewhere) {
		t.Errorf("SessionTurn provider = %+v", origin.Provider)
	}
	if in := origin.SecretStore; in == nil || in.ID != secrets.ID || in.ProjectID != project.ID || in.Name != "a store" || in.Kind != "a kind" ||
		!keeps(t, in.Settings, `{"where": "there"}`) || string(in.Credential) != "sealed" || !in.UpdatedAt.Equal(secrets.UpdatedAt) {
		t.Errorf("SessionTurn secret store = %+v", in)
	}
	moved, err := st.ReplaceSecretStore(ctx, store.SecretStore{
		ID: secrets.ID, Name: "a store", Kind: "another kind", Settings: json.RawMessage(`{"where": "elsewhere"}`), Credential: []byte("another"),
	})
	if err != nil {
		t.Fatal(err)
	}
	origin, err = st.another().SessionTurn(ctx, session.ID)
	if in := origin.SecretStore; err != nil || in == nil || in.Kind != "another kind" || !keeps(t, in.Settings, `{"where": "elsewhere"}`) ||
		string(in.Credential) != "another" || !in.UpdatedAt.Equal(moved.UpdatedAt) || !in.UpdatedAt.After(secrets.UpdatedAt) {
		t.Errorf("SessionTurn secret store after ReplaceSecretStore = %+v: %v", in, err)
	}
}

// SessionTurn returns the origin of the turn that runs in a session, with the
// current provider of its agent and the secret store of the provider's key.
func (suite suite) testSessionTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	provider, err := st.CreateProvider(ctx, store.Provider{
		ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "opencode", Model: store.Model{Provider: provider.ID, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := st.UserOf(ctx, "subject-of-ann", "ann", "Ann")
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, agent.ID, sender.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	notFound := func(session uuid.UUID, what string) {
		t.Helper()
		if _, err := st.another().SessionTurn(ctx, session); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: SessionTurn = %v, want ErrNotFound", what, err)
		}
	}

	turn, _, err := st.CreateTurn(ctx, session.ID, sender.ID, "hello", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	notFound(session.ID, "queued turn")
	st.dispatch()
	notFound(session.ID, "unfollowed dispatched turn")
	if _, err := st.FollowTurn(ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	origin, err := st.another().SessionTurn(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.TurnID != turn.ID || origin.SessionID != session.ID || origin.AgentID != agent.ID || origin.ProjectID != project.ID ||
		origin.UserID != sender.ID || origin.Subject != "subject-of-ann" || origin.Provider.ID != provider.ID ||
		origin.Provider.URL != provider.URL || string(origin.Provider.APIKey) != "a sealed key" || origin.Provider.APIKeyAt != nil || origin.SecretStore != nil {
		t.Errorf("SessionTurn = %+v, provider %+v", origin, origin.Provider)
	}
	notFound(uuid.New(), "nonexistent session")

	// The key of the provider moves to a secret store while the turn runs.
	secrets, err := st.CreateSecretStore(ctx, store.SecretStore{
		ID: uuid.New(), ProjectID: project.ID, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{"where": "there"}`), Credential: []byte("sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	at := &store.SecretReference{Store: secrets.ID, Path: "llm/a-provider", Key: "api_key"}
	if _, err := st.ReplaceProvider(ctx, store.Provider{ID: provider.ID, Name: "a provider", URL: "https://api.example.com/v2", APIKeyAt: at}); err != nil {
		t.Fatal(err)
	}
	origin, err = st.another().SessionTurn(ctx, session.ID)
	if err != nil || origin.Provider.URL != "https://api.example.com/v2" || origin.Provider.APIKey != nil ||
		!reflect.DeepEqual(origin.Provider.APIKeyAt, at) {
		t.Errorf("SessionTurn provider after ReplaceProvider = %+v: %v", origin.Provider, err)
	}
	if in := origin.SecretStore; in == nil || in.ID != secrets.ID || in.ProjectID != project.ID || in.Kind != "a kind" ||
		!keeps(t, in.Settings, `{"where": "there"}`) || string(in.Credential) != "sealed" || !in.UpdatedAt.Equal(secrets.UpdatedAt) {
		t.Errorf("SessionTurn secret store = %+v", in)
	}

	// Once the turn has ended, no turn runs in the session.
	st.end(turn.ID, true, store.Failure{})
	notFound(session.ID, "ended turn")
}

// second returns the error of a call that returns a value and an error.
func second[T any](_ T, err error) error {
	return err
}

// A secret store is created with the ID that it is given, and replaced whole,
// as a provider is.
func (suite suite) testSecretStores(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	given := store.SecretStore{
		ID: uuid.New(), ProjectID: project.ID, Name: "a store", Kind: "a kind",
		Settings: json.RawMessage(`{"address": "https://secrets.example.com:8200", "nested": {"b": 2, "a": [1, "two"]}}`), Credential: []byte("a sealed credential"),
	}
	created, err := st.CreateSecretStore(ctx, given)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != given.ID || created.ProjectID != project.ID || created.Name != given.Name || created.Kind != given.Kind ||
		!keeps(t, created.Settings, string(given.Settings)) || !bytes.Equal(created.Credential, given.Credential) ||
		created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreateSecretStore = %+v", created)
	}
	read, err := st.SecretStore(ctx, given.ID)
	if err != nil || read.Name != created.Name || read.Kind != created.Kind || !keeps(t, read.Settings, string(given.Settings)) ||
		!bytes.Equal(read.Credential, given.Credential) || !read.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("SecretStore = %+v: %v", read, err)
	}

	replaced, err := st.ReplaceSecretStore(ctx, store.SecretStore{
		ID: given.ID, Name: "renamed", Kind: "another kind", Settings: json.RawMessage(`{"region": "here"}`), Credential: []byte("another"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != given.ID || replaced.ProjectID != project.ID || replaced.Name != "renamed" || replaced.Kind != "another kind" ||
		!keeps(t, replaced.Settings, `{"region": "here"}`) || string(replaced.Credential) != "another" ||
		!replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("ReplaceSecretStore = %+v, was %+v", replaced, created)
	}
}

// Providers lists the providers of a project in creation order, as Provider
// reads them.
func (suite suite) testListProviders(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, other := st.project(), st.project()
	if providers, err := st.Providers(ctx, project.ID); err != nil || providers == nil || len(providers) != 0 {
		t.Errorf("Providers of a project without providers = %+v, %v, want empty", providers, err)
	}
	secrets, err := st.CreateSecretStore(ctx, store.SecretStore{
		ID: uuid.New(), ProjectID: project.ID, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var created []store.Provider
	for _, given := range []store.Provider{
		{ProjectID: project.ID, Name: "stored", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key")},
		{ProjectID: other.ID, Name: "theirs", URL: "https://api.example.com/v1"},
		{ProjectID: project.ID, Name: "elsewhere", URL: "http://10.0.0.5:8000/v1", APIKeyAt: &store.SecretReference{Store: secrets.ID, Path: "llm/a-provider", Key: "api_key"}},
		{ProjectID: project.ID, Name: "keyless", URL: "http://model.example.com:8000"},
	} {
		// As the API does: IDs that increase with creation time.
		given.ID = uuid.Must(uuid.NewV7())
		provider, err := st.CreateProvider(ctx, given)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, provider)
	}
	for _, listed := range []struct {
		project   uuid.UUID
		providers []store.Provider
	}{
		{project.ID, []store.Provider{created[0], created[2], created[3]}},
		{other.ID, []store.Provider{created[1]}},
		{uuid.New(), []store.Provider{}},
	} {
		providers, err := st.Providers(ctx, listed.project)
		if err != nil || len(providers) != len(listed.providers) {
			t.Fatalf("want %d providers, got %+v: %v", len(listed.providers), providers, err)
		}
		for i, provider := range providers {
			if read, err := st.Provider(ctx, listed.providers[i].ID); err != nil || !same(provider, read) {
				t.Errorf("provider %d: listed %+v, read %+v: %v", i, provider, read, err)
			}
		}
	}
}

// SecretStores lists the secret stores of a project in creation order, as
// SecretStore reads them.
func (suite suite) testListSecretStores(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, other := st.project(), st.project()
	if stores, err := st.SecretStores(ctx, project.ID); err != nil || stores == nil || len(stores) != 0 {
		t.Errorf("SecretStores of a project without secret stores = %+v, %v, want empty", stores, err)
	}
	var created []store.SecretStore
	for _, given := range []store.SecretStore{
		{ProjectID: project.ID, Name: "first", Kind: "a kind", Settings: json.RawMessage(`{"where": "here"}`), Credential: []byte("a sealed credential")},
		{ProjectID: other.ID, Name: "theirs", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed")},
		{ProjectID: project.ID, Name: "second", Kind: "another kind", Settings: json.RawMessage(`{"where": "there"}`), Credential: []byte("another")},
	} {
		given.ID = uuid.Must(uuid.NewV7())
		secrets, err := st.CreateSecretStore(ctx, given)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, secrets)
	}
	for _, listed := range []struct {
		project uuid.UUID
		stores  []store.SecretStore
	}{
		{project.ID, []store.SecretStore{created[0], created[2]}},
		{other.ID, []store.SecretStore{created[1]}},
		{uuid.New(), []store.SecretStore{}},
	} {
		stores, err := st.SecretStores(ctx, listed.project)
		if err != nil || len(stores) != len(listed.stores) {
			t.Fatalf("want %d secret stores, got %+v: %v", len(listed.stores), stores, err)
		}
		for i, secrets := range stores {
			read, err := st.SecretStore(ctx, listed.stores[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			if secrets.ID != read.ID || secrets.ProjectID != listed.project || secrets.Name != read.Name || secrets.Kind != read.Kind ||
				!keeps(t, secrets.Settings, string(read.Settings)) || !bytes.Equal(secrets.Credential, read.Credential) ||
				!secrets.CreatedAt.Equal(read.CreatedAt) || !secrets.UpdatedAt.Equal(read.UpdatedAt) {
				t.Errorf("secret store %d: listed %+v, read %+v", i, secrets, read)
			}
		}
	}
}

// A followed turn has the provider URL and the model of its agent, and the
// origin of a running turn has the current provider.
func (suite suite) testAgentModel(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project := st.project()
	provider, err := st.CreateProvider(ctx, store.Provider{
		ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	model := store.Model{Provider: provider.ID, Name: "a-model"}
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "opencode", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Agent(ctx, agent.ID); err != nil || read.ID != agent.ID || read.ProjectID != project.ID ||
		read.Model != model || agent.Model != model {
		t.Errorf("agent with model %+v: created %+v, read %+v: %v", model, agent, read, err)
	}
	session, err := st.CreateSession(ctx, agent.ID, uuid.Nil, "")
	if err != nil {
		t.Fatal(err)
	}
	st.queue(session.ID)
	followed, err := st.FollowTurn(ctx, st.dispatch().TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if followed.ProviderURL != provider.URL || followed.Model != "a-model" || followed.AgentID != agent.ID || followed.ProjectID != project.ID {
		t.Errorf("FollowTurn = %+v", followed)
	}
	origin, err := st.SessionTurn(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.TurnID != followed.ID || origin.SessionID != session.ID || origin.AgentID != agent.ID || origin.ProjectID != project.ID ||
		origin.Provider.ID != provider.ID || origin.Provider.URL != provider.URL || string(origin.Provider.APIKey) != "a sealed key" ||
		origin.Provider.APIKeyAt != nil || origin.SecretStore != nil {
		t.Errorf("SessionTurn = %+v, provider %+v", origin, origin.Provider)
	}
	// The provider of the model must exist.
	if _, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "opencode", Model: store.Model{Provider: uuid.New(), Name: "a-model"}}); err == nil {
		t.Error("CreateAgent succeeded with a nonexistent provider")
	}
	// The provider is replaced while a turn uses it.
	if _, err := st.ReplaceProvider(ctx, store.Provider{ID: provider.ID, Name: "a provider", URL: "http://model.example.com:8000"}); err != nil {
		t.Fatal(err)
	}
	origin, err = st.another().SessionTurn(ctx, session.ID)
	if err != nil || origin.Provider.URL != "http://model.example.com:8000" || origin.Provider.APIKey != nil {
		t.Errorf("SessionTurn after ReplaceProvider = %+v, provider %+v: %v", origin, origin.Provider, err)
	}

	// SessionTurn finds a turn only while it runs.
	st.end(followed.ID, true, store.Failure{})
	if _, err := st.SessionTurn(ctx, session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SessionTurn after the turn ended = %v, want ErrNotFound", err)
	}
}
