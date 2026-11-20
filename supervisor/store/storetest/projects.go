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

// An agent is kept with all that it is created with, and a session with the
// agent that it is of.
func (suite suite) testAgents(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	given := store.Agent{Harness: "opencode", Instructions: "Be brief.", Approval: "ask", Model: &store.Model{Provider: provider.ID, Name: "a-model"}}
	created, err := st.CreateAgent(ctx, project.ID, given)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.Agent(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for how, agent := range map[string]store.Agent{"created": created, "read": read} {
		if agent.ID == uuid.Nil || agent.ID != created.ID || agent.ProjectID != project.ID || agent.Harness != "opencode" ||
			agent.Instructions != "Be brief." || agent.Approval != "ask" ||
			!reflect.DeepEqual(agent.Model, given.Model) || agent.CreatedAt.IsZero() || !agent.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("the agent is %s as %+v, with the model %+v", how, agent, agent.Model)
		}
	}
	// An agent with nothing of its own but its harness.
	plain, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Agent(ctx, plain.ID); err != nil || read.Harness != "codex" || read.Instructions != "" ||
		read.Approval != "" || read.Model != nil {
		t.Errorf("the agent with nothing of its own is read as %+v: %v", read, err)
	}

	session, err := st.CreateSession(ctx, created.ID, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Session(ctx, session.ID); err != nil || read.ID != session.ID || read.AgentID != created.ID ||
		session.AgentID != created.ID || session.CreatedAt.IsZero() || !read.CreatedAt.Equal(session.CreatedAt) {
		t.Errorf("the session %+v is read as %+v: %v", session, read, err)
	}
}

// The agents of a project are listed in the order in which they were
// created, each as it is read by itself.
func (suite suite) testAgentsOfAProject(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if agents, err := st.Agents(ctx, project.ID); err != nil || agents == nil || len(agents) != 0 {
		t.Errorf("a project without agents has %+v, with the error %v", agents, err)
	}
	provider, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	var created []store.Agent
	for _, given := range []struct {
		project uuid.UUID
		agent   store.Agent
	}{
		{project.ID, store.Agent{Harness: "opencode", Instructions: "Be brief.", Approval: "ask"}},
		{other.ID, store.Agent{Harness: "opencode"}},
		{project.ID, store.Agent{Harness: "codex", Model: &store.Model{Provider: provider.ID, Name: "a-model"}}},
		{project.ID, store.Agent{Harness: "opencode"}},
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
			t.Fatalf("the agents of a project with %d are %+v, with the error %v", len(listed.agents), agents, err)
		}
		for i, agent := range agents {
			read, err := st.Agent(ctx, listed.agents[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			if agent.ID != read.ID || agent.ProjectID != listed.project || agent.Harness != read.Harness || agent.Instructions != read.Instructions ||
				agent.Approval != read.Approval || !reflect.DeepEqual(agent.Model, read.Model) || !agent.CreatedAt.Equal(read.CreatedAt) {
				t.Errorf("the agent %d of the project is listed as %+v and read as %+v", i, agent, read)
			}
		}
	}
}

// A session keeps the sandbox that it runs in. It is given one, or loses
// it, only if it has the one that whoever asks says it has: of several
// Supervisors that give a session a sandbox at once, one does.
func (suite suite) testSandboxOfASession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	has := func(sandbox string) {
		t.Helper()
		if session, err := st.another().Session(ctx, id); err != nil || session.Sandbox != sandbox {
			t.Errorf("the session has the sandbox %q, not %q: %v", session.Sandbox, sandbox, err)
		}
	}
	has("")
	for _, step := range []struct {
		from, to string
		set      bool
		then     string
	}{
		{from: "", to: "sandbox-1", set: true, then: "sandbox-1"},
		// Another Supervisor made a sandbox for the session too.
		{from: "", to: "sandbox-2", then: "sandbox-1"},
		// The sandbox is gone, and then the session is given the next.
		{from: "sandbox-2", to: "", then: "sandbox-1"},
		{from: "sandbox-1", to: "", set: true, then: ""},
		{from: "sandbox-1", to: "", then: ""},
		{from: "", to: "sandbox-3", set: true, then: "sandbox-3"},
		{from: "sandbox-3", to: "sandbox-3", set: true, then: "sandbox-3"},
	} {
		set, err := st.SetSandbox(ctx, id, step.from, step.to)
		if err != nil || set != step.set {
			t.Errorf("from %q to %q, the session was given the sandbox: %t, %v", step.from, step.to, set, err)
		}
		has(step.then)
	}
	if _, err := st.SetSandbox(ctx, uuid.New(), "", "sandbox-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session that there is not was given a sandbox: %v", err)
	}
	// The sessions of an agent are told with their sandboxes too.
	session, err := st.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := st.Sessions(ctx, session.AgentID, uuid.Nil, uuid.Max, 10)
	if err != nil || len(sessions) != 1 || sessions[0].Sandbox != "sandbox-3" {
		t.Errorf("the sessions of the agent are %+v: %v", sessions, err)
	}

	// Several Supervisors give a new session its sandbox at once.
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
		t.Errorf("of eight Supervisors that gave a session its sandbox at once, %d did", given.Load())
	}
}

// The sessions of an agent are listed newest first, a few at a time, and
// those of one user by themselves.
func (suite suite) testSessionsOfAnAgent(t *testing.T) {
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
		t.Errorf("an agent that there is not has the sessions %+v, with the error %v", sessions, err)
	}
	// The sessions of the agent, oldest first: nobody's, and those of two
	// users.
	created := []store.Session{first}
	for _, by := range []uuid.UUID{alice, bob, alice, uuid.Nil, alice} {
		session, err := st.CreateSession(ctx, agent, by)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, session)
	}
	elsewhere, err := st.Session(ctx, st.session())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, elsewhere.AgentID, alice); err != nil {
		t.Fatal(err)
	}

	for _, listed := range []struct {
		what   string
		by     uuid.UUID
		before uuid.UUID
		limit  int
		// sessions are the sessions of the answer, by when they were created.
		sessions []int
	}{
		{"all", uuid.Nil, uuid.Max, 10, []int{5, 4, 3, 2, 1, 0}},
		{"as many as there are", uuid.Nil, uuid.Max, 6, []int{5, 4, 3, 2, 1, 0}},
		{"the newest", uuid.Nil, uuid.Max, 2, []int{5, 4}},
		{"those before one", uuid.Nil, created[4].ID, 3, []int{3, 2, 1}},
		{"those before the second", uuid.Nil, created[1].ID, 3, []int{0}},
		{"those before the first", uuid.Nil, created[0].ID, 3, []int{}},
		{"those of a user", alice, uuid.Max, 10, []int{5, 3, 1}},
		{"the newest of a user", alice, uuid.Max, 2, []int{5, 3}},
		{"those of a user before one of them", alice, created[3].ID, 10, []int{1}},
		{"those of a user before one of another", alice, created[4].ID, 10, []int{3, 1}},
		{"those of another user", bob, uuid.Max, 10, []int{2}},
		{"those of a user who has none", carol, uuid.Max, 10, []int{}},
	} {
		sessions, err := st.Sessions(ctx, agent, listed.by, listed.before, listed.limit)
		if err != nil || sessions == nil {
			t.Fatalf("%s: the sessions are %+v, with the error %v", listed.what, sessions, err)
		}
		var answer []int
		for _, session := range sessions {
			at := slices.IndexFunc(created, func(one store.Session) bool { return one.ID == session.ID })
			if at < 0 || session.AgentID != agent || session.CreatedBy != created[at].CreatedBy || !session.CreatedAt.Equal(created[at].CreatedAt) {
				t.Errorf("%s: a session is listed as %+v", listed.what, session)
			}
			answer = append(answer, at)
		}
		if !slices.Equal(answer, listed.sessions) {
			t.Errorf("%s: the sessions are %v, not %v", listed.what, answer, listed.sessions)
		}
	}
}

// A session is ended when none of its turns waits or runs, and once. From
// then on it takes no message, and what was said in it stays.
func (suite suite) testEndSession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	if set, err := st.SetSandbox(ctx, id, "", "sandbox-1"); err != nil || !set {
		t.Fatalf("the session was not given its sandbox: %v", err)
	}

	// A session with a turn that has not ended is not ended.
	st.queue(id)
	busy := func(turn string) {
		t.Helper()
		if _, err := st.EndSession(ctx, id); !errors.Is(err, store.ErrSessionBusy) {
			t.Errorf("a session with a turn that %s was ended: %v", turn, err)
		}
		if session, err := st.another().Session(ctx, id); err != nil || session.EndedAt != nil {
			t.Errorf("a session with a turn that %s ended at %v: %v", turn, session.EndedAt, err)
		}
	}
	busy("waits to run")
	dispatch := st.dispatch()
	busy("waits to be followed")
	if _, err := st.FollowTurn(ctx, dispatch.TurnID); err != nil {
		t.Fatal(err)
	}
	busy("runs")
	st.end(dispatch.TurnID, true, store.Failure{})

	ended, err := st.EndSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ended.ID != id || ended.EndedAt == nil || ended.EndedAt.Before(ended.CreatedAt) || ended.Sandbox != "sandbox-1" {
		t.Fatalf("the session was ended as %+v, at %v", ended, ended.EndedAt)
	}
	// Who ends it again is given it as it ended.
	again, err := st.another().EndSession(ctx, id)
	if err != nil || again.EndedAt == nil || !again.EndedAt.Equal(*ended.EndedAt) || again.Sandbox != "sandbox-1" {
		t.Errorf("the session that was ended again is %+v, ended at %v and not at %v: %v", again, again.EndedAt, ended.EndedAt, err)
	}
	// So it is read, by itself and among the sessions of its agent.
	read, err := st.another().Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := st.Sessions(ctx, ended.AgentID, uuid.Nil, uuid.Max, 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("the sessions of the agent are %+v: %v", sessions, err)
	}
	for how, session := range map[string]store.Session{"read": read, "listed": sessions[0]} {
		if session.EndedAt == nil || !session.EndedAt.Equal(*ended.EndedAt) {
			t.Errorf("the session is %s as ended at %v, not at %v", how, session.EndedAt, ended.EndedAt)
		}
	}

	// It takes no message, and is told so before anything else: also when
	// too many turns wait for a message to be taken.
	if _, _, err := st.CreateTurn(ctx, id, uuid.Nil, "again", 0, 50); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("a session that has ended took a message: %v", err)
	}
	st.queue(st.session())
	if _, _, err := st.CreateTurn(ctx, id, uuid.Nil, "again", 1, 0); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("a session that has ended took a message when too many turns waited: %v", err)
	}
	if turns, err := st.Turns(ctx, id, math.MaxInt32, 10); err != nil || len(turns) != 1 || turns[0].ID != dispatch.TurnID {
		t.Errorf("the turns of the session that has ended are %+v: %v", turns, err)
	}

	// A session that never took a message is ended too, and one that there
	// is not is not.
	if fresh, err := st.EndSession(ctx, st.session()); err != nil || fresh.EndedAt == nil || fresh.Sandbox != "" {
		t.Errorf("a session without turns was ended as %+v, at %v: %v", fresh, fresh.EndedAt, err)
	}
	if _, err := st.EndSession(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session that there is not was ended: %v", err)
	}
}

// A session is ended or takes a message, also when several Supervisors do
// the one and the other at once: a session that has ended has no turn that
// has not.
func (suite suite) testSupervisorsEndASessionWhileMessagesCome(t *testing.T) {
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
		// Either all who ended the session did and no message was taken, or
		// one message was and nobody ended the session.
		all := int32(len(supervisors) / 2)
		if session.EndedAt != nil && (len(turns) != 0 || accepted.Load() != 0 || ended.Load() != all) ||
			session.EndedAt == nil && (len(turns) != 1 || accepted.Load() != 1 || ended.Load() != 0) {
			t.Fatalf("a session that four Supervisors ended and four sent a message at once ended at %v with %d turns: %d ended it, and %d messages were taken",
				session.EndedAt, len(turns), ended.Load(), accepted.Load())
		}
	}
}

// A project has model providers. A provider is created with the ID that it
// is given, and replaced whole.
func (suite suite) testProviders(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Project(ctx, project.ID); err != nil || read.ID != project.ID || read.Name != "test" || project.Name != "test" ||
		project.CreatedAt.IsZero() || !read.CreatedAt.Equal(project.CreatedAt) {
		t.Errorf("the project %+v is read as %+v: %v", project, read, err)
	}

	given := store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key")}
	created, err := st.CreateProvider(ctx, given)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != given.ID || created.ProjectID != project.ID || created.Name != given.Name || created.URL != given.URL ||
		!bytes.Equal(created.APIKey, given.APIKey) || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("the provider was created as %+v", created)
	}
	if read, err := st.Provider(ctx, given.ID); err != nil || !same(read, created) {
		t.Errorf("the provider is read as %+v, not as %+v: %v", read, created, err)
	}

	// All that is replaced is given: a provider that is given no key has
	// none from then on.
	replaced, err := st.ReplaceProvider(ctx, store.Provider{ID: given.ID, Name: "renamed", URL: "http://model.example.com:8000", APIKey: []byte("another")})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != given.ID || replaced.ProjectID != project.ID || replaced.Name != "renamed" || replaced.URL != "http://model.example.com:8000" ||
		string(replaced.APIKey) != "another" || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("the provider was replaced with %+v, and was %+v", replaced, created)
	}
	keyless, err := st.ReplaceProvider(ctx, store.Provider{ID: given.ID, Name: "renamed", URL: "http://model.example.com:8000"})
	if err != nil || keyless.APIKey != nil {
		t.Errorf("the provider that was given no key is %+v: %v", keyless, err)
	}
	if read, err := st.Provider(ctx, given.ID); err != nil || !same(read, keyless) {
		t.Errorf("the replaced provider is read as %+v, not as %+v: %v", read, keyless, err)
	}
	// A project has a provider once.
	if _, err := st.CreateProvider(ctx, given); err == nil {
		t.Error("a provider was created a second time")
	}
	// A provider without a key has none, which is not an empty key.
	local, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "local", URL: "http://10.0.0.5:8000/v1"})
	if err != nil || local.APIKey != nil {
		t.Errorf("the provider without a key was created as %+v: %v", local, err)
	}

	none := uuid.New()
	for name, err := range map[string]error{
		"a provider that is not there":           second(st.Provider(ctx, none)),
		"the replacement of one that is not":     second(st.ReplaceProvider(ctx, store.Provider{ID: none, Name: "a provider", URL: "https://api.example.com"})),
		"a provider of a project that is not":    second(st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: none, Name: "a provider", URL: "https://api.example.com"})),
		"a secret store that is not there":       second(st.SecretStore(ctx, none)),
		"the replacement of a store that is not": second(st.ReplaceSecretStore(ctx, store.SecretStore{ID: none, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed")})),
		"a store of a project that is not":       second(st.CreateSecretStore(ctx, store.SecretStore{ID: uuid.New(), ProjectID: none, Name: "a store", Kind: "a kind", Settings: json.RawMessage(`{}`), Credential: []byte("sealed")})),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// same tells whether two providers are the same in all that is kept of one.
func same(one, other store.Provider) bool {
	return one.ID == other.ID && one.ProjectID == other.ProjectID && one.Name == other.Name && one.URL == other.URL &&
		bytes.Equal(one.APIKey, other.APIKey) && (one.APIKey == nil) == (other.APIKey == nil) &&
		reflect.DeepEqual(one.APIKeyAt, other.APIKeyAt) &&
		one.CreatedAt.Equal(other.CreatedAt) && one.UpdatedAt.Equal(other.UpdatedAt)
}

// A provider keeps its key in a secret store of the project, in place of a
// key that is stored: it has the one, the other or none.
func (suite suite) testKeyInASecretStore(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("the provider whose key is in a secret store was created as %+v, with its key at %+v", created, created.APIKeyAt)
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || !same(read, created) {
		t.Errorf("the provider is read as %+v, not as %+v: %v", read, created, err)
	}

	// All that is replaced is given: the key that is stored takes the place
	// of the one in the secret store, and the other way around.
	replacement := store.Provider{ID: created.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key")}
	if stored, err := st.ReplaceProvider(ctx, replacement); err != nil || stored.APIKeyAt != nil || string(stored.APIKey) != "a sealed key" {
		t.Errorf("the provider that was given a key to store is %+v, with its key at %+v: %v", stored, stored.APIKeyAt, err)
	}
	elsewhere := &store.SecretReference{Store: secrets.ID, Path: "llm/another", Key: "key"}
	replacement.APIKey, replacement.APIKeyAt = nil, elsewhere
	if kept, err := st.ReplaceProvider(ctx, replacement); err != nil || kept.APIKey != nil || !reflect.DeepEqual(kept.APIKeyAt, elsewhere) {
		t.Errorf("the provider that was given a key in a secret store is %+v, with its key at %+v: %v", kept, kept.APIKeyAt, err)
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || read.APIKey != nil || !reflect.DeepEqual(read.APIKeyAt, elsewhere) {
		t.Errorf("the replaced provider is read as %+v, with its key at %+v: %v", read, read.APIKeyAt, err)
	}

	// The key of a provider is in a secret store that there is.
	nowhere := &store.SecretReference{Store: uuid.New(), Path: "llm/a-provider", Key: "api_key"}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "another", URL: "https://api.example.com/v1", APIKeyAt: nowhere}); err == nil {
		t.Error("a provider was created whose key is in a secret store that there is not")
	}
	replacement.APIKeyAt = nowhere
	if _, err := st.ReplaceProvider(ctx, replacement); err == nil {
		t.Error("a provider was given a key in a secret store that there is not")
	}
	if read, err := st.Provider(ctx, created.ID); err != nil || !reflect.DeepEqual(read.APIKeyAt, elsewhere) {
		t.Errorf("after that, the provider has its key at %+v: %v", read.APIKeyAt, err)
	}

	// The turn that runs in a session tells of the provider with where its
	// key is, and of the secret store as it is then.
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: created.ID, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, agent.ID, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	st.running(session.ID)
	origin, err := st.SessionTurn(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.Provider == nil || origin.Provider.APIKey != nil || !reflect.DeepEqual(origin.Provider.APIKeyAt, elsewhere) {
		t.Errorf("the turn of the session has the provider %+v", origin.Provider)
	}
	if in := origin.SecretStore; in == nil || in.ID != secrets.ID || in.ProjectID != project.ID || in.Name != "a store" || in.Kind != "a kind" ||
		!keeps(t, in.Settings, `{"where": "there"}`) || string(in.Credential) != "sealed" || !in.UpdatedAt.Equal(secrets.UpdatedAt) {
		t.Errorf("the key of the provider is in the secret store %+v", in)
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
		t.Errorf("after the secret store was replaced, the key of the provider is in %+v: %v", in, err)
	}
}

// The turn that runs in a session is found by the session, with where it
// comes from: what it belongs to, who sent its message, and the provider of
// its agent as the provider is then, with the secret store of its key.
func (suite suite) testTurnOfASession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := st.CreateProvider(ctx, store.Provider{
		ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: provider.ID, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := st.UserOf(ctx, "subject-of-ann", "ann", "Ann")
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, agent.ID, sender.ID)
	if err != nil {
		t.Fatal(err)
	}
	notFound := func(session uuid.UUID, what string) {
		t.Helper()
		if _, err := st.another().SessionTurn(ctx, session); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", what, err)
		}
	}

	turn, _, err := st.CreateTurn(ctx, session.ID, sender.ID, "hello", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	notFound(session.ID, "a session whose turn waits to run")
	st.dispatch()
	notFound(session.ID, "a session whose turn no Supervisor follows")
	if _, err := st.FollowTurn(ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	origin, err := st.another().SessionTurn(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.TurnID != turn.ID || origin.SessionID != session.ID || origin.AgentID != agent.ID || origin.ProjectID != project.ID ||
		origin.UserID != sender.ID || origin.Subject != "subject-of-ann" || origin.Provider == nil || origin.Provider.ID != provider.ID ||
		origin.Provider.URL != provider.URL || string(origin.Provider.APIKey) != "a sealed key" || origin.Provider.APIKeyAt != nil || origin.SecretStore != nil {
		t.Errorf("the turn of the session comes from %+v, with the provider %+v", origin, origin.Provider)
	}
	notFound(uuid.New(), "a session that there is not")

	// The key of the provider is put in a secret store while the turn runs.
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
	if err != nil || origin.Provider == nil || origin.Provider.URL != "https://api.example.com/v2" || origin.Provider.APIKey != nil ||
		!reflect.DeepEqual(origin.Provider.APIKeyAt, at) {
		t.Errorf("after its provider was replaced, the turn of the session has the provider %+v: %v", origin.Provider, err)
	}
	if in := origin.SecretStore; in == nil || in.ID != secrets.ID || in.ProjectID != project.ID || in.Kind != "a kind" ||
		!keeps(t, in.Settings, `{"where": "there"}`) || string(in.Credential) != "sealed" || !in.UpdatedAt.Equal(secrets.UpdatedAt) {
		t.Errorf("the key of the provider is in the secret store %+v", in)
	}

	// Once the turn has ended, nothing runs in the session.
	st.end(turn.ID, true, store.Failure{})
	notFound(session.ID, "a session whose turn has ended")
}

// second returns the error of an operation.
func second[T any](_ T, err error) error {
	return err
}

// A project has secret stores, which are kept as providers are.
func (suite suite) testSecretStores(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("the secret store was created as %+v", created)
	}
	read, err := st.SecretStore(ctx, given.ID)
	if err != nil || read.Name != created.Name || read.Kind != created.Kind || !keeps(t, read.Settings, string(given.Settings)) ||
		!bytes.Equal(read.Credential, given.Credential) || !read.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("the secret store is read as %+v: %v", read, err)
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
		t.Errorf("the secret store was replaced with %+v, and was %+v", replaced, created)
	}
}

// An agent names its model: a provider of its project and a model of that
// provider. A turn of the agent is followed with the provider's URL and the
// model, and while it runs, its session tells where it comes from and which
// provider its agent uses, as the provider is then.
func (suite suite) testModelOfAnAgent(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := st.CreateProvider(ctx, store.Provider{
		ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1", APIKey: []byte("a sealed key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// session creates a session of an agent with the model, and has a
	// Supervisor follow its turn.
	session := func(model *store.Model) (store.Agent, store.Session, store.FollowedTurn) {
		t.Helper()
		agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		if read, err := st.Agent(ctx, agent.ID); err != nil || read.ID != agent.ID || read.ProjectID != project.ID ||
			!reflect.DeepEqual(read.Model, model) || !reflect.DeepEqual(agent.Model, model) {
			t.Errorf("the agent with the model %+v is %+v and is read as %+v: %v", model, agent, read, err)
		}
		session, err := st.CreateSession(ctx, agent.ID, uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		st.queue(session.ID)
		followed, err := st.FollowTurn(ctx, st.dispatch().TurnID)
		if err != nil {
			t.Fatal(err)
		}
		return agent, session, followed
	}

	agent, held, followed := session(&store.Model{Provider: provider.ID, Name: "a-model"})
	if followed.ProviderURL != provider.URL || followed.Model != "a-model" || followed.AgentID != agent.ID || followed.ProjectID != project.ID {
		t.Errorf("the turn of an agent that names its model is followed as %+v", followed)
	}
	origin, err := st.SessionTurn(ctx, held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if origin.TurnID != followed.ID || origin.SessionID != held.ID || origin.AgentID != agent.ID || origin.ProjectID != project.ID ||
		origin.Provider == nil || origin.Provider.ID != provider.ID || origin.Provider.URL != provider.URL || string(origin.Provider.APIKey) != "a sealed key" ||
		origin.Provider.APIKeyAt != nil || origin.SecretStore != nil {
		t.Errorf("the turn of the session comes from %+v, with the provider %+v", origin, origin.Provider)
	}
	// An agent names a provider that there is.
	if _, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: uuid.New(), Name: "a-model"}}); err == nil {
		t.Error("an agent was created that names a provider that there is not")
	}
	// A provider that is replaced while a turn uses it.
	if _, err := st.ReplaceProvider(ctx, store.Provider{ID: provider.ID, Name: "a provider", URL: "http://model.example.com:8000"}); err != nil {
		t.Fatal(err)
	}
	origin, err = st.another().SessionTurn(ctx, held.ID)
	if err != nil || origin.Provider == nil || origin.Provider.URL != "http://model.example.com:8000" || origin.Provider.APIKey != nil {
		t.Errorf("after its provider was replaced, the turn of the session comes from %+v, with the provider %+v: %v", origin, origin.Provider, err)
	}

	// An agent from before agents named their models has none.
	_, other, plain := session(nil)
	if plain.ProviderURL != "" || plain.Model != "" {
		t.Errorf("the turn of an agent that names no model is followed as %+v", plain)
	}
	if origin, err := st.SessionTurn(ctx, other.ID); err != nil || origin.TurnID != plain.ID || origin.Provider != nil {
		t.Errorf("the turn of the session of an agent that names no model comes from %+v: %v", origin, err)
	}

	// A session tells of a turn only while the turn runs.
	st.end(followed.ID, true, store.Failure{})
	if _, err := st.SessionTurn(ctx, held.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the session of a turn that has ended: %v", err)
	}
}
