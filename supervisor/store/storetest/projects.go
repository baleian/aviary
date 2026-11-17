package storetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
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
	given := store.Agent{
		Harness: "opencode", Instructions: "Be brief.", Resources: json.RawMessage(`{"limits": {"memory": "2Gi"}}`), Approval: "ask",
		Model: &store.Model{Provider: provider.ID, Name: "a-model"},
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
		if agent.ID == uuid.Nil || agent.ID != created.ID || agent.ProjectID != project.ID || agent.Harness != "opencode" ||
			agent.Instructions != "Be brief." || !keeps(t, agent.Resources, string(given.Resources)) || agent.Approval != "ask" ||
			!reflect.DeepEqual(agent.Model, given.Model) || agent.CreatedAt.IsZero() || !agent.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("the agent is %s as %+v, with the model %+v", how, agent, agent.Model)
		}
	}
	// An agent with nothing of its own but its harness.
	plain, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Agent(ctx, plain.ID); err != nil || read.Harness != "codex" || read.Instructions != "" || read.Resources != nil ||
		read.Approval != "" || read.Model != nil {
		t.Errorf("the agent with nothing of its own is read as %+v: %v", read, err)
	}

	session, err := st.CreateSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read, err := st.Session(ctx, session.ID); err != nil || read.ID != session.ID || read.AgentID != created.ID ||
		session.AgentID != created.ID || session.CreatedAt.IsZero() || !read.CreatedAt.Equal(session.CreatedAt) {
		t.Errorf("the session %+v is read as %+v: %v", session, read, err)
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

	// The token of a running turn tells of the provider with where its key
	// is, and of the secret store as it is then.
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: created.ID, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := st.running(session.ID)
	origin, err := st.RunningTurn(ctx, dispatch.Token)
	if err != nil {
		t.Fatal(err)
	}
	if origin.Provider == nil || origin.Provider.APIKey != nil || !reflect.DeepEqual(origin.Provider.APIKeyAt, elsewhere) {
		t.Errorf("the token of the running turn is that of the provider %+v", origin.Provider)
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
	origin, err = st.another().RunningTurn(ctx, dispatch.Token)
	if in := origin.SecretStore; err != nil || in == nil || in.Kind != "another kind" || !keeps(t, in.Settings, `{"where": "elsewhere"}`) ||
		string(in.Credential) != "another" || !in.UpdatedAt.Equal(moved.UpdatedAt) || !in.UpdatedAt.After(secrets.UpdatedAt) {
		t.Errorf("after the secret store was replaced, the key of the provider is in %+v: %v", in, err)
	}
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
// provider. A turn of the agent is dispatched with the provider's URL and
// the model, and while it runs, its token tells where it comes from and
// which provider its agent uses, as the provider is then.
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
	// runner take its turn.
	session := func(model *store.Model) (store.Agent, store.Session, store.Dispatch) {
		t.Helper()
		agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		if read, err := st.Agent(ctx, agent.ID); err != nil || read.ID != agent.ID || read.ProjectID != project.ID ||
			!reflect.DeepEqual(read.Model, model) || !reflect.DeepEqual(agent.Model, model) {
			t.Errorf("the agent with the model %+v is %+v and is read as %+v: %v", model, agent, read, err)
		}
		session, err := st.CreateSession(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		dispatch, _ := st.running(session.ID)
		return agent, session, dispatch
	}

	agent, held, dispatch := session(&store.Model{Provider: provider.ID, Name: "a-model"})
	if dispatch.ProviderURL != provider.URL || dispatch.Model != "a-model" || dispatch.AgentID != agent.ID || dispatch.ProjectID != project.ID {
		t.Errorf("the turn of an agent that names its model was dispatched as %+v", dispatch)
	}
	origin, err := st.RunningTurn(ctx, dispatch.Token)
	if err != nil {
		t.Fatal(err)
	}
	if origin.TurnID != dispatch.TurnID || origin.SessionID != held.ID || origin.AgentID != agent.ID || origin.ProjectID != project.ID ||
		origin.Provider == nil || origin.Provider.ID != provider.ID || origin.Provider.URL != provider.URL || string(origin.Provider.APIKey) != "a sealed key" ||
		origin.Provider.APIKeyAt != nil || origin.SecretStore != nil {
		t.Errorf("the token of the running turn is that of %+v, with the provider %+v", origin, origin.Provider)
	}
	// An agent names a provider that there is.
	if _, err := st.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: uuid.New(), Name: "a-model"}}); err == nil {
		t.Error("an agent was created that names a provider that there is not")
	}
	// A provider that is replaced while a turn uses it.
	if _, err := st.ReplaceProvider(ctx, store.Provider{ID: provider.ID, Name: "a provider", URL: "http://model.example.com:8000"}); err != nil {
		t.Fatal(err)
	}
	origin, err = st.another().RunningTurn(ctx, dispatch.Token)
	if err != nil || origin.Provider == nil || origin.Provider.URL != "http://model.example.com:8000" || origin.Provider.APIKey != nil {
		t.Errorf("after its provider was replaced, the token of the running turn is that of %+v, with the provider %+v: %v", origin, origin.Provider, err)
	}

	// An agent from before agents named their models has none.
	_, _, plain := session(nil)
	if plain.ProviderURL != "" || plain.Model != "" {
		t.Errorf("the turn of an agent that names no model was dispatched as %+v", plain)
	}
	if origin, err := st.RunningTurn(ctx, plain.Token); err != nil || origin.TurnID != plain.TurnID || origin.Provider != nil {
		t.Errorf("the token of the turn of an agent that names no model is that of %+v: %v", origin, err)
	}

	// A token tells of its turn only while the turn runs.
	if _, err := st.RunningTurn(ctx, "a-token-of-no-turn"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a token of no turn: %v", err)
	}
	st.end(dispatch.TurnID, true, store.Failure{})
	if _, err := st.RunningTurn(ctx, dispatch.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the token of a turn that has ended: %v", err)
	}
	waiting, err := st.CreateSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.queue(waiting.ID)
	if _, err := st.RunningTurn(ctx, st.dispatch().Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the token of a turn that no runner has taken: %v", err)
	}
}
