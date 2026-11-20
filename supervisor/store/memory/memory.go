// Package memory keeps the Supervisor's records in the memory of the
// process. It is for tests: the Supervisors of a test share one store as the
// Supervisors of an installation share a database, and nothing outlasts the
// test.
package memory

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// Store is a store in memory. Its zero value is not one: New makes it.
type Store struct {
	// mu guards all that the store holds. An operation holds it from its
	// beginning to its end, as a database runs it in a transaction.
	mu sync.Mutex
	// announcing is held while the turns that wait are told their positions.
	announcing sync.Mutex
	// last is the time that the clock gave last.
	last time.Time

	projects map[uuid.UUID]store.Project
	// members are the members of the projects in the order in which they
	// were added.
	members []*member
	// roles are the roles by their names.
	roles map[string]*store.Role
	// users are the users by their subjects.
	users        map[string]*store.User
	agents       map[uuid.UUID]store.Agent
	sessions     map[uuid.UUID]*session
	providers    map[uuid.UUID]store.Provider
	secretStores map[uuid.UUID]store.SecretStore
	turns        map[uuid.UUID]*turn
	// created are the turns in the order in which they were created.
	created []*turn
}

// session is what the store holds of a session.
type session struct {
	store.Session
	// turns are the turns of the session, in their order.
	turns []*turn
}

// turn is what the store holds of a turn.
type turn struct {
	store.Turn
	dispatchedAt *time.Time
	// items are what the agent said and did in the turn.
	items []json.RawMessage
	// approvals are the requests of the turn for approval, oldest first.
	approvals []*approval
}

// approval is a request of a turn's harness for approval.
type approval struct {
	id          string
	request     json.RawMessage
	requestedAt time.Time
	// optionID is the option that a person chose, if answered, and
	// answeredBy the user who chose it.
	optionID           string
	answeredBy         uuid.UUID
	answered, resolved bool
	// resolvedAt is when the harness got its answer, if resolved.
	resolvedAt time.Time
}

func New() *Store {
	roles := map[string]*store.Role{}
	for name, permissions := range map[string][]string{
		"owner":   {"project.view", "agents.manage", "connections.manage", "sessions.run", "sessions.view_all", "sessions.control_all"},
		"editor":  {"project.view", "agents.manage", "connections.manage", "sessions.run"},
		"user":    {"project.view", "sessions.run"},
		"viewer":  {"project.view"},
		"auditor": {"project.view", "sessions.view_all"},
	} {
		roles[name] = &store.Role{Name: name, Permissions: permissions, UpdatedAt: time.Now()}
	}
	return &Store{
		roles:        roles,
		projects:     map[uuid.UUID]store.Project{},
		users:        map[string]*store.User{},
		agents:       map[uuid.UUID]store.Agent{},
		sessions:     map[uuid.UUID]*session{},
		providers:    map[uuid.UUID]store.Provider{},
		secretStores: map[uuid.UUID]store.SecretStore{},
		turns:        map[uuid.UUID]*turn{},
	}
}

// Close does nothing: the Supervisors of a test share the store.
func (*Store) Close() {}

// Age makes the turns so much older: all that the store has recorded of when
// something happened to them, and to their requests for approval, is that
// long before. It is how a test lets time pass.
func (s *Store) Age(by time.Duration, turns ...uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	earlier := func(at *time.Time) *time.Time {
		if at == nil {
			return nil
		}
		earlier := at.Add(-by)
		return &earlier
	}
	for _, id := range turns {
		if turn, found := s.turns[id]; found {
			turn.CreatedAt = turn.CreatedAt.Add(-by)
			turn.dispatchedAt, turn.StartedAt = earlier(turn.dispatchedAt), earlier(turn.StartedAt)
			turn.CancelRequestedAt, turn.EndedAt = earlier(turn.CancelRequestedAt), earlier(turn.EndedAt)
			for _, approval := range turn.approvals {
				approval.requestedAt, approval.resolvedAt = approval.requestedAt.Add(-by), approval.resolvedAt.Add(-by)
			}
		}
	}
}

// begin begins an operation, which ends when the caller gives the lock back.
// An operation whose caller waits for it no longer does not begin.
func (s *Store) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	return nil
}

// now returns the time, which is later every time that it is asked: of two
// things that the store records, one happened first. The caller holds the
// lock.
func (s *Store) now() time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(s.last) {
		now = s.last.Add(time.Microsecond)
	}
	s.last = now
	return now
}

// clone returns a copy of what the pointer points to, if anything.
func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// valid tells whether JSON that the store is given is JSON.
func valid(what string, given json.RawMessage) error {
	if !json.Valid(given) {
		return fmt.Errorf("%s is not JSON: %.100s", what, given)
	}
	return nil
}

// The store gives out copies of what it holds, and holds copies of what it
// is given: nothing that a caller changes afterwards changes a record.

func agentOf(agent store.Agent) store.Agent {
	agent.Model = clone(agent.Model)
	return agent
}

func providerOf(provider store.Provider) store.Provider {
	provider.APIKey, provider.APIKeyAt = bytes.Clone(provider.APIKey), clone(provider.APIKeyAt)
	return provider
}

func secretStoreOf(secrets store.SecretStore) store.SecretStore {
	secrets.Settings, secrets.Credential = bytes.Clone(secrets.Settings), bytes.Clone(secrets.Credential)
	return secrets
}

func usageOf(usage *store.Usage) *store.Usage {
	usage = clone(usage)
	if usage != nil {
		usage.ThoughtTokens, usage.CachedReadTokens = clone(usage.ThoughtTokens), clone(usage.CachedReadTokens)
		usage.CachedWriteTokens = clone(usage.CachedWriteTokens)
	}
	return usage
}

// record returns the session as its record is read.
func (s *session) record() store.Session {
	session := s.Session
	session.EndedAt = clone(s.EndedAt)
	return session
}

// record returns the turn as its record is read.
func (t *turn) record() store.Turn {
	turn := t.Turn
	turn.StartedAt, turn.StopReason, turn.EndedAt = clone(t.StartedAt), clone(t.StopReason), clone(t.EndedAt)
	turn.CancelRequestedAt, turn.Failure, turn.Usage = clone(t.CancelRequestedAt), clone(t.Failure), usageOf(t.Usage)
	return turn
}

func (s *Store) CreateProject(ctx context.Context, name string) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Project{}, err
	}
	defer s.mu.Unlock()
	project := store.Project{ID: id, Name: name, CreatedAt: s.now()}
	s.projects[id] = project
	return project, nil
}

func (s *Store) Project(ctx context.Context, id uuid.UUID) (store.Project, error) {
	if err := s.begin(ctx); err != nil {
		return store.Project{}, err
	}
	defer s.mu.Unlock()
	project, found := s.projects[id]
	if !found {
		return store.Project{}, store.ErrNotFound
	}
	return project, nil
}

func (s *Store) CreateAgent(ctx context.Context, projectID uuid.UUID, agent store.Agent) (store.Agent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Agent{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Agent{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.projects[projectID]; !found {
		return store.Agent{}, store.ErrNotFound
	}
	if agent.Model != nil {
		if _, found := s.providers[agent.Model.Provider]; !found {
			return store.Agent{}, fmt.Errorf("the agent names the provider %s, which there is not", agent.Model.Provider)
		}
	}
	created := agentOf(store.Agent{
		ID: id, ProjectID: projectID, Harness: agent.Harness, CreatedAt: s.now(), Instructions: agent.Instructions,
		Approval: agent.Approval, Model: agent.Model,
	})
	s.agents[id] = created
	return agentOf(created), nil
}

// member is a member of a project. If it is a user, that is the user whose
// subject it has, or else someone awaited under the username that it has.
type member struct {
	store.Member
	subject, username string
}

// record returns the member as the store gives it out: with its user as
// the user is now.
func (s *Store) record(of *member) store.Member {
	record := of.Member
	record.Permissions = slices.Clone(of.Permissions)
	switch {
	case of.subject != "":
		user := *s.users[of.subject]
		record.User = &user
	case of.username != "":
		record.User = &store.User{Username: of.username}
	}
	return record
}

// claim makes the user with the username the member that projects await
// under it.
func (s *Store) claim(username string) {
	var user *store.User
	for _, known := range s.users {
		if known.Username == username {
			user = known
		}
	}
	if user == nil {
		return
	}
	awaited := map[uuid.UUID]bool{}
	for _, member := range s.members {
		if member.username == username {
			awaited[member.ProjectID] = true
		}
	}
	s.members = slices.DeleteFunc(s.members, func(member *member) bool {
		return member.subject == user.Subject && awaited[member.ProjectID]
	})
	for _, member := range s.members {
		if member.username == username {
			member.subject, member.username = user.Subject, ""
		}
	}
}

func (s *Store) AddMember(ctx context.Context, added store.Member, by uuid.UUID) (store.Member, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Member{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Member{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.projects[added.ProjectID]; !found {
		return store.Member{}, store.ErrNotFound
	}
	created := &member{Member: store.Member{
		ID: id, ProjectID: added.ProjectID, Group: added.Group, Role: added.Role, CreatedAt: s.now(), UpdatedBy: by,
	}}
	if added.Role == "" {
		created.Permissions = slices.Clone(added.Permissions)
	}
	created.UpdatedAt = created.CreatedAt
	if added.User != nil {
		created.username, created.Group = added.User.Username, ""
	}
	for _, other := range s.members {
		if other.ProjectID != added.ProjectID {
			continue
		}
		user := other.subject != "" && created.username != "" && s.users[other.subject].Username == created.username
		if user || (other.username != "" && other.username == created.username) || (other.Group != "" && other.Group == created.Group) {
			return store.Member{}, store.ErrMemberExists
		}
	}
	s.members = append(s.members, created)
	if created.username != "" {
		s.claim(created.username)
	}
	return s.record(created), nil
}

func (s *Store) SetMember(ctx context.Context, id uuid.UUID, role string, permissions []string, by uuid.UUID) (store.Member, error) {
	if err := s.begin(ctx); err != nil {
		return store.Member{}, err
	}
	defer s.mu.Unlock()
	for _, member := range s.members {
		if member.ID == id {
			member.Role, member.Permissions, member.UpdatedAt, member.UpdatedBy = role, nil, s.now(), by
			if role == "" {
				member.Permissions = slices.Clone(permissions)
			}
			return s.record(member), nil
		}
	}
	return store.Member{}, store.ErrNotFound
}

func (s *Store) RemoveMember(ctx context.Context, id uuid.UUID) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	before := len(s.members)
	if s.members = slices.DeleteFunc(s.members, func(member *member) bool { return member.ID == id }); len(s.members) == before {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) Members(ctx context.Context, projectID uuid.UUID) ([]store.Member, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	members := []store.Member{}
	for _, member := range s.members {
		if member.ProjectID == projectID {
			members = append(members, s.record(member))
		}
	}
	return members, nil
}

func (s *Store) Projects(ctx context.Context) ([]store.Project, error) {
	return s.projectsThat(ctx, func(uuid.UUID) bool { return true })
}

func (s *Store) ProjectsOf(ctx context.Context, userID uuid.UUID, groups []string) ([]store.Project, error) {
	return s.projectsThat(ctx, func(project uuid.UUID) bool {
		return slices.ContainsFunc(s.members, func(member *member) bool {
			user := member.subject != "" && s.users[member.subject].ID == userID
			return member.ProjectID == project && (user || (member.Group != "" && slices.Contains(groups, member.Group)))
		})
	})
}

// projectsThat returns the projects that the function picks, in the order
// in which they were created.
func (s *Store) projectsThat(ctx context.Context, pick func(uuid.UUID) bool) ([]store.Project, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	projects := []store.Project{}
	for _, project := range s.projects {
		if pick(project.ID) {
			projects = append(projects, project)
		}
	}
	slices.SortFunc(projects, func(one, other store.Project) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return projects, nil
}

func (s *Store) Permissions(ctx context.Context, projectID, userID uuid.UUID, groups []string) ([]string, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	var held []string
	for _, member := range s.members {
		user := member.subject != "" && s.users[member.subject].ID == userID
		if member.ProjectID != projectID || !(user || (member.Group != "" && slices.Contains(groups, member.Group))) {
			continue
		}
		if member.Role != "" {
			held = append(held, s.roles[member.Role].Permissions...)
		} else {
			held = append(held, member.Permissions...)
		}
	}
	slices.Sort(held)
	return slices.Compact(held), nil
}

func (s *Store) Roles(ctx context.Context) ([]store.Role, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	roles := make([]store.Role, 0, len(s.roles))
	for _, role := range s.roles {
		roles = append(roles, store.Role{Name: role.Name, Permissions: slices.Clone(role.Permissions), UpdatedAt: role.UpdatedAt, UpdatedBy: role.UpdatedBy})
	}
	slices.SortFunc(roles, func(one, other store.Role) int { return strings.Compare(one.Name, other.Name) })
	return roles, nil
}

func (s *Store) SetRole(ctx context.Context, name string, permissions []string, by uuid.UUID) (store.Role, error) {
	if err := s.begin(ctx); err != nil {
		return store.Role{}, err
	}
	defer s.mu.Unlock()
	role, found := s.roles[name]
	if !found {
		return store.Role{}, store.ErrNotFound
	}
	role.Permissions, role.UpdatedAt, role.UpdatedBy = slices.Clone(permissions), s.now(), by
	return store.Role{Name: name, Permissions: slices.Clone(permissions), UpdatedAt: role.UpdatedAt, UpdatedBy: by}, nil
}

func (s *Store) UserOf(ctx context.Context, subject, username, displayName string) (store.User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.User{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.User{}, err
	}
	defer s.mu.Unlock()
	user, found := s.users[subject]
	if !found {
		user = &store.User{ID: id, Subject: subject, CreatedAt: s.now()}
		s.users[subject] = user
	}
	user.DisplayName = displayName
	if username != "" {
		for _, other := range s.users {
			if other.Username == username {
				other.Username = ""
			}
		}
		user.Username = username
		s.claim(username)
	}
	return *user, nil
}

func (s *Store) Agent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	if err := s.begin(ctx); err != nil {
		return store.Agent{}, err
	}
	defer s.mu.Unlock()
	agent, found := s.agents[id]
	if !found {
		return store.Agent{}, store.ErrNotFound
	}
	return agentOf(agent), nil
}

func (s *Store) Agents(ctx context.Context, projectID uuid.UUID) ([]store.Agent, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	agents := []store.Agent{}
	for _, agent := range s.agents {
		if agent.ProjectID == projectID {
			agents = append(agents, agentOf(agent))
		}
	}
	slices.SortFunc(agents, func(one, other store.Agent) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return agents, nil
}

func (s *Store) CreateSession(ctx context.Context, agentID, by uuid.UUID) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.agents[agentID]; !found {
		return store.Session{}, store.ErrNotFound
	}
	created := &session{Session: store.Session{ID: id, AgentID: agentID, CreatedBy: by, CreatedAt: s.now()}}
	s.sessions[id] = created
	return created.record(), nil
}

func (s *Store) Session(ctx context.Context, id uuid.UUID) (store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[id]
	if !found {
		return store.Session{}, store.ErrNotFound
	}
	return session.record(), nil
}

func (s *Store) SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error) {
	if err := s.begin(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[sessionID]
	if !found {
		return false, store.ErrNotFound
	}
	if session.Sandbox != from {
		return false, nil
	}
	session.Sandbox = to
	return true, nil
}

func (s *Store) EndSession(ctx context.Context, id uuid.UUID) (store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[id]
	if !found {
		return store.Session{}, store.ErrNotFound
	}
	if session.EndedAt == nil {
		if turns := len(session.turns); turns > 0 && session.turns[turns-1].EndedAt == nil {
			return store.Session{}, store.ErrSessionBusy
		}
		ended := s.now()
		session.EndedAt = &ended
	}
	return session.record(), nil
}

func (s *Store) Sessions(ctx context.Context, agentID, by, before uuid.UUID, limit int) ([]store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	sessions := []store.Session{}
	for _, session := range s.sessions {
		if session.AgentID == agentID && (by == uuid.Nil || session.CreatedBy == by) && bytes.Compare(session.ID[:], before[:]) < 0 {
			sessions = append(sessions, session.record())
		}
	}
	slices.SortFunc(sessions, func(one, other store.Session) int { return bytes.Compare(other.ID[:], one.ID[:]) })
	return sessions[:min(limit, len(sessions))], nil
}

func (s *Store) CreateProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	if err := s.begin(ctx); err != nil {
		return store.Provider{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.projects[provider.ProjectID]; !found {
		return store.Provider{}, store.ErrNotFound
	}
	if _, found := s.providers[provider.ID]; found {
		return store.Provider{}, fmt.Errorf("there is a provider %s already", provider.ID)
	}
	if err := s.keyStore(provider); err != nil {
		return store.Provider{}, err
	}
	created := providerOf(provider)
	created.CreatedAt = s.now()
	created.UpdatedAt = created.CreatedAt
	s.providers[created.ID] = created
	return providerOf(created), nil
}

// keyStore tells what is wrong with a provider whose key is in a secret
// store that there is not. The caller holds the lock.
func (s *Store) keyStore(provider store.Provider) error {
	if at := provider.APIKeyAt; at != nil {
		if _, found := s.secretStores[at.Store]; !found {
			return fmt.Errorf("the key of the provider is in the secret store %s, which there is not", at.Store)
		}
	}
	return nil
}

func (s *Store) Provider(ctx context.Context, id uuid.UUID) (store.Provider, error) {
	if err := s.begin(ctx); err != nil {
		return store.Provider{}, err
	}
	defer s.mu.Unlock()
	provider, found := s.providers[id]
	if !found {
		return store.Provider{}, store.ErrNotFound
	}
	return providerOf(provider), nil
}

func (s *Store) ReplaceProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	if err := s.begin(ctx); err != nil {
		return store.Provider{}, err
	}
	defer s.mu.Unlock()
	replaced, found := s.providers[provider.ID]
	if !found {
		return store.Provider{}, store.ErrNotFound
	}
	if err := s.keyStore(provider); err != nil {
		return store.Provider{}, err
	}
	replaced.Name, replaced.URL = provider.Name, provider.URL
	replaced.APIKey, replaced.APIKeyAt = bytes.Clone(provider.APIKey), clone(provider.APIKeyAt)
	replaced.UpdatedAt = s.now()
	s.providers[replaced.ID] = replaced
	return providerOf(replaced), nil
}

func (s *Store) CreateSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	if err := s.begin(ctx); err != nil {
		return store.SecretStore{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.projects[secrets.ProjectID]; !found {
		return store.SecretStore{}, store.ErrNotFound
	}
	if _, found := s.secretStores[secrets.ID]; found {
		return store.SecretStore{}, fmt.Errorf("there is a secret store %s already", secrets.ID)
	}
	if err := valid("the settings of the secret store", secrets.Settings); err != nil {
		return store.SecretStore{}, err
	}
	created := secretStoreOf(secrets)
	created.CreatedAt = s.now()
	created.UpdatedAt = created.CreatedAt
	s.secretStores[created.ID] = created
	return secretStoreOf(created), nil
}

func (s *Store) SecretStore(ctx context.Context, id uuid.UUID) (store.SecretStore, error) {
	if err := s.begin(ctx); err != nil {
		return store.SecretStore{}, err
	}
	defer s.mu.Unlock()
	secrets, found := s.secretStores[id]
	if !found {
		return store.SecretStore{}, store.ErrNotFound
	}
	return secretStoreOf(secrets), nil
}

func (s *Store) ReplaceSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	if err := s.begin(ctx); err != nil {
		return store.SecretStore{}, err
	}
	defer s.mu.Unlock()
	replaced, found := s.secretStores[secrets.ID]
	if !found {
		return store.SecretStore{}, store.ErrNotFound
	}
	if err := valid("the settings of the secret store", secrets.Settings); err != nil {
		return store.SecretStore{}, err
	}
	replaced.Name, replaced.Kind = secrets.Name, secrets.Kind
	replaced.Settings, replaced.Credential = bytes.Clone(secrets.Settings), bytes.Clone(secrets.Credential)
	replaced.UpdatedAt = s.now()
	s.secretStores[replaced.ID] = replaced
	return secretStoreOf(replaced), nil
}

// queued returns the turns that wait to run, in the order in which they will
// run: oldest first. The caller holds the lock.
func (s *Store) queued() []*turn {
	var queued []*turn
	for _, turn := range s.created {
		if turn.State == store.TurnQueued {
			queued = append(queued, turn)
		}
	}
	slices.SortStableFunc(queued, func(one, other *turn) int {
		if order := one.CreatedAt.Compare(other.CreatedAt); order != 0 {
			return order
		}
		return bytes.Compare(one.ID[:], other.ID[:])
	})
	return queued
}

// waiting tells whether the turn waits for a person: a request of its
// harness for approval is not resolved.
func (t *turn) waiting() bool {
	return slices.ContainsFunc(t.approvals, func(approval *approval) bool { return !approval.resolved })
}

// running returns how many turns run or are about to: those that were
// dispatched and have not ended. The caller holds the lock.
func (s *Store) running() int {
	running := 0
	for _, turn := range s.created {
		if turn.State == store.TurnDispatching || turn.State == store.TurnRunning {
			running++
		}
	}
	return running
}

func (s *Store) CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, maxWaiting, maxRunning int) (store.Turn, int, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Turn{}, 0, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Turn{}, 0, err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[sessionID]
	if !found {
		return store.Turn{}, 0, store.ErrNotFound
	}
	if session.EndedAt != nil {
		return store.Turn{}, 0, store.ErrSessionEnded
	}
	if turns := len(session.turns); turns > 0 && session.turns[turns-1].EndedAt == nil {
		return store.Turn{}, 0, store.ErrSessionBusy
	}
	// A queued turn waits only if the turns that run leave it no room.
	waiting := len(s.queued()) + 1 - max(0, maxRunning-s.running())
	if maxWaiting > 0 && waiting > maxWaiting {
		return store.Turn{}, 0, store.ErrQueueFull
	}
	created := &turn{Turn: store.Turn{
		ID: id, SessionID: sessionID, Seq: len(session.turns) + 1, State: store.TurnQueued, Text: text, CreatedBy: by, CreatedAt: s.now(),
	}}
	s.turns[id], s.created, session.turns = created, append(s.created, created), append(session.turns, created)
	return created.record(), max(waiting, 0), nil
}

func (s *Store) AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error {
	s.announcing.Lock()
	defer s.announcing.Unlock()
	if err := s.begin(ctx); err != nil {
		return err
	}
	queued := []uuid.UUID{}
	for _, turn := range s.queued() {
		queued = append(queued, turn.ID)
	}
	s.mu.Unlock()
	return announce(queued)
}

func (s *Store) CancellingTurns(ctx context.Context) ([]uuid.UUID, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	cancelling := []uuid.UUID{}
	for _, turn := range s.created {
		if turn.EndedAt == nil && turn.CancelRequestedAt != nil {
			cancelling = append(cancelling, turn.ID)
		}
	}
	return cancelling, nil
}

func (s *Store) Turn(ctx context.Context, id uuid.UUID) (store.Turn, error) {
	if err := s.begin(ctx); err != nil {
		return store.Turn{}, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[id]
	if !found {
		return store.Turn{}, store.ErrNotFound
	}
	return turn.record(), nil
}

func (s *Store) Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]store.Turn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	turns := []store.Turn{}
	if session, found := s.sessions[sessionID]; found {
		for _, turn := range slices.Backward(session.turns) {
			if turn.Seq < before && len(turns) < limit {
				turns = append(turns, turn.record())
			}
		}
	}
	return turns, nil
}

func (s *Store) TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	items := map[uuid.UUID][]json.RawMessage{}
	for _, id := range turnIDs {
		if turn, found := s.turns[id]; found && len(turn.items) > 0 {
			items[id] = cloned(turn.items)
		}
	}
	return items, nil
}

// cloned returns copies of the JSON values.
func cloned(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for i, value := range values {
		cloned[i] = bytes.Clone(value)
	}
	return cloned
}

func (s *Store) DispatchTurns(ctx context.Context, limit int) ([]store.Dispatch, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	queued := s.queued()
	room := min(max(limit-s.running(), 0), len(queued))
	dispatches := make([]store.Dispatch, room)
	for i, turn := range queued[:room] {
		agent, now := s.agents[s.sessions[turn.SessionID].AgentID], s.now()
		turn.State, turn.dispatchedAt = store.TurnDispatching, &now
		dispatches[i] = store.Dispatch{TurnID: turn.ID, Harness: agent.Harness, Waited: now.Sub(turn.CreatedAt)}
	}
	return dispatches, nil
}

func (s *Store) CancelTurn(ctx context.Context, id uuid.UUID) (store.Turn, bool, error) {
	if err := s.begin(ctx); err != nil {
		return store.Turn{}, false, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[id]
	if !found {
		return store.Turn{}, false, store.ErrNotFound
	}
	if turn.EndedAt != nil {
		return turn.record(), false, nil
	}
	now := s.now()
	// A queued turn ends at once. A dispatched one is only marked.
	if turn.State == store.TurnQueued {
		turn.State, turn.EndedAt = store.TurnCancelled, clone(&now)
	}
	if turn.CancelRequestedAt == nil {
		turn.CancelRequestedAt = &now
	}
	return turn.record(), true, nil
}

func (s *Store) ExpireTurns(ctx context.Context, wait time.Duration) ([]store.Turn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	now, expired := s.now(), []store.Turn{}
	for _, turn := range s.queued() {
		if turn.CreatedAt.Before(now.Add(-wait)) {
			turn.State, turn.EndedAt = store.TurnExpired, clone(&now)
			expired = append(expired, turn.record())
		}
	}
	return expired, nil
}

func (s *Store) DispatchedTurns(ctx context.Context) ([]store.DispatchedTurn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	now, dispatched := s.now(), []store.DispatchedTurn{}
	for _, turn := range s.created {
		if turn.State != store.TurnDispatching && turn.State != store.TurnRunning {
			continue
		}
		one := store.DispatchedTurn{ID: turn.ID}
		if turn.CancelRequestedAt != nil {
			one.Cancelling = now.Sub(*turn.CancelRequestedAt)
		}
		dispatched = append(dispatched, one)
	}
	return dispatched, nil
}

func (s *Store) FollowTurn(ctx context.Context, turnID uuid.UUID) (store.FollowedTurn, error) {
	if err := s.begin(ctx); err != nil {
		return store.FollowedTurn{}, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	now := s.now()
	switch {
	case !found || turn.State == store.TurnQueued:
		return store.FollowedTurn{}, store.ErrNotFound
	case turn.State != store.TurnDispatching && turn.State != store.TurnRunning:
		return store.FollowedTurn{}, store.ErrTurnEnded
	}
	turn.State = store.TurnRunning
	session := s.sessions[turn.SessionID]
	agent := s.agents[session.AgentID]
	followed := store.FollowedTurn{
		Turn: turn.Turn, AgentID: agent.ID, ProjectID: agent.ProjectID,
		Harness: agent.Harness, Instructions: agent.Instructions, Approval: agent.Approval,
		Sandbox: session.Sandbox, Dispatched: now.Sub(turn.CreatedAt), Waiting: turn.waiting(),
	}
	if turn.dispatchedAt != nil {
		followed.Dispatched = now.Sub(*turn.dispatchedAt)
	}
	if agent.Model != nil {
		followed.Model = agent.Model.Name
		followed.ProviderURL = s.providers[agent.Model.Provider].URL
	}
	return followed, nil
}

func (s *Store) SandboxTurn(ctx context.Context, sandbox string) (store.Origin, error) {
	if err := s.begin(ctx); err != nil {
		return store.Origin{}, err
	}
	defer s.mu.Unlock()
	if sandbox == "" {
		return store.Origin{}, store.ErrNotFound
	}
	for _, turn := range s.created {
		if turn.State == store.TurnRunning && s.sessions[turn.SessionID].Sandbox == sandbox {
			return s.originOf(turn), nil
		}
	}
	return store.Origin{}, store.ErrNotFound
}

// originOf is where the running turn comes from. The store is held.
func (s *Store) originOf(turn *turn) store.Origin {
	agent := s.agents[s.sessions[turn.SessionID].AgentID]
	origin := store.Origin{TurnID: turn.ID, SessionID: turn.SessionID, AgentID: agent.ID, ProjectID: agent.ProjectID, UserID: turn.CreatedBy}
	for _, user := range s.users {
		if user.ID == turn.CreatedBy {
			origin.Subject = user.Subject
		}
	}
	if agent.Model != nil {
		provider := s.providers[agent.Model.Provider]
		origin.Provider = &store.Provider{
			ID: provider.ID, ProjectID: agent.ProjectID, URL: provider.URL, APIKey: bytes.Clone(provider.APIKey),
			APIKeyAt: clone(provider.APIKeyAt),
		}
		// The key of the provider is in a secret store, which comes with it.
		if provider.APIKeyAt != nil {
			secrets := secretStoreOf(s.secretStores[provider.APIKeyAt.Store])
			origin.SecretStore = &secrets
		}
	}
	return origin
}

func (s *Store) StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error) {
	if err := s.begin(ctx); err != nil {
		return 0, false, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	if !found || turn.StartedAt != nil {
		return 0, false, nil
	}
	now := s.now()
	turn.StartedAt = &now
	if turn.dispatchedAt == nil {
		return 0, true, nil
	}
	return now.Sub(*turn.dispatchedAt), true, nil
}

func (s *Store) TurnHarness(ctx context.Context, turnID uuid.UUID) (string, error) {
	if err := s.begin(ctx); err != nil {
		return "", err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	if !found {
		return "", store.ErrNotFound
	}
	return s.agents[s.sessions[turn.SessionID].AgentID].Harness, nil
}

func (s *Store) SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *store.Usage) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if turn, found := s.turns[turnID]; found {
		turn.StopReason, turn.Usage = &stopReason, usageOf(usage)
	}
	return nil
}

// EndTurn holds the store while it reads what the turn said, as the
// transaction of a database holds the turn: what else is asked of the store
// waits until the turn has ended or not.
func (s *Store) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure,
	items func(context.Context) ([]json.RawMessage, error)) (store.Turn, bool, error) {
	if err := s.begin(ctx); err != nil {
		return store.Turn{}, false, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	if !found || turn.EndedAt != nil {
		return store.Turn{}, false, nil
	}
	said, err := items(ctx)
	if err != nil {
		return store.Turn{}, false, fmt.Errorf("read what the turn said: %w", err)
	}
	for _, item := range said {
		if err := valid("an item of the turn", item); err != nil {
			return store.Turn{}, false, err
		}
	}
	now := s.now()
	turn.EndedAt, turn.items = &now, cloned(said)
	// A turn that was asked to stop is cancelled, unless the harness had
	// ended it by itself. Otherwise it is completed if its pod succeeded and
	// the runner reported why the harness stopped, and failed if not.
	ended := succeeded && turn.StopReason != nil
	switch {
	case turn.CancelRequestedAt != nil && !(ended && *turn.StopReason != "cancelled"):
		turn.State = store.TurnCancelled
	case ended:
		turn.State = store.TurnCompleted
	default:
		turn.State, turn.Failure = store.TurnFailed, &failure
	}
	return turn.record(), true, nil
}

// asked returns the request of the turn for approval with the ID, and the
// turn, if there are such. The caller holds the lock.
func (s *Store) asked(turnID uuid.UUID, id string) (*approval, *turn) {
	turn, found := s.turns[turnID]
	if !found {
		return nil, nil
	}
	for _, approval := range turn.approvals {
		if approval.id == id {
			return approval, turn
		}
	}
	return nil, turn
}

func (s *Store) AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	kept, turn := s.asked(turnID, id)
	if turn == nil {
		return errors.New("the turn of the request for approval is not there")
	}
	if err := valid("the request for approval", request); err != nil {
		return err
	}
	// A request that its runner reports again is there already.
	if kept == nil {
		turn.approvals = append(turn.approvals, &approval{id: id, request: bytes.Clone(request), requestedAt: s.now()})
	}
	return nil
}

// ResolveApproval keeps no outcome: no operation of a store returns it.
func (s *Store) ResolveApproval(ctx context.Context, turnID uuid.UUID, id, _ string) (store.Approval, time.Duration, bool, error) {
	if err := s.begin(ctx); err != nil {
		return store.Approval{}, 0, false, err
	}
	defer s.mu.Unlock()
	kept, _ := s.asked(turnID, id)
	if kept == nil || kept.resolved {
		return store.Approval{}, 0, false, nil
	}
	kept.resolved, kept.resolvedAt = true, s.now()
	return store.Approval{Request: bytes.Clone(kept.request), Resolved: true}, kept.resolvedAt.Sub(kept.requestedAt), true, nil
}

func (s *Store) Approval(ctx context.Context, turnID uuid.UUID, id string) (store.Approval, error) {
	if err := s.begin(ctx); err != nil {
		return store.Approval{}, err
	}
	defer s.mu.Unlock()
	kept, _ := s.asked(turnID, id)
	if kept == nil {
		return store.Approval{}, store.ErrNotFound
	}
	return store.Approval{Request: bytes.Clone(kept.request), Answered: kept.answered, Resolved: kept.resolved, AnsweredBy: kept.answeredBy}, nil
}

func (s *Store) AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error) {
	if err := s.begin(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	kept, turn := s.asked(turnID, id)
	if kept == nil || kept.answered || kept.resolved || turn.EndedAt != nil {
		return false, nil
	}
	kept.optionID, kept.answeredBy, kept.answered = optionID, by, true
	return true, nil
}

func (s *Store) WaitingApprovals(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	requests := []json.RawMessage{}
	if turn, found := s.turns[turnID]; found {
		for _, approval := range turn.approvals {
			if !approval.answered && !approval.resolved {
				requests = append(requests, bytes.Clone(approval.request))
			}
		}
	}
	return requests, nil
}

func (s *Store) Answers(ctx context.Context, turnID *uuid.UUID) ([]store.Answer, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	answers := []store.Answer{}
	for _, turn := range s.created {
		if turn.EndedAt != nil || turnID != nil && turn.ID != *turnID {
			continue
		}
		for _, approval := range turn.approvals {
			if approval.answered && !approval.resolved {
				answers = append(answers, store.Answer{TurnID: turn.ID, ID: approval.id, OptionID: approval.optionID})
			}
		}
	}
	return answers, nil
}
