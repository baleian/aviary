// Package memory implements store.Store in memory, for tests. The
// Supervisors of a test share one Store as those of an installation share a
// database.
package memory

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// Store is a store.Store in memory. Use New to create one.
type Store struct {
	// mu guards the records and last. Every operation holds it from start to
	// end, as a database transaction would.
	mu sync.Mutex
	// announcing serializes AnnounceQueue.
	announcing sync.Mutex
	// last is the time that now returned last.
	last time.Time

	projects map[uuid.UUID]store.Project
	// members are in the order in which they were added.
	members []*member
	// roles are keyed by name, and users by subject.
	roles        map[string]*store.Role
	users        map[string]*store.User
	agents       map[uuid.UUID]store.Agent
	sessions     map[uuid.UUID]*session
	providers    map[uuid.UUID]store.Provider
	mcpServers   map[uuid.UUID]store.McpServer
	secretStores map[uuid.UUID]store.SecretStore
	turns        map[uuid.UUID]*turn
	// created holds the turns in creation order.
	created []*turn
	// attachments are in the order in which they were recorded.
	attachments []*store.Attachment
}

type session struct {
	store.Session
	// turns are in sequence order.
	turns []*turn
	// mcp are the MCP servers that the session turned on or off where it
	// differs from its agent.
	mcp map[uuid.UUID]bool
	// attachments is whether the object storage may have attachments of the
	// session.
	attachments bool
}

type turn struct {
	store.Turn
	dispatchedAt *time.Time
	items        []json.RawMessage
	// approvals are oldest first.
	approvals []*approval
	// attachments are those that the message carries, in its order.
	attachments []*store.Attachment
}

// asked returns when the oldest of the turn's approval requests that wait for
// an answer was made, or zero if none waits.
func (t *turn) asked() time.Time {
	for _, approval := range t.approvals {
		if !approval.answered && !approval.resolved {
			return approval.requestedAt
		}
	}
	return time.Time{}
}

type approval struct {
	id                     string
	request                json.RawMessage
	requestedAt            time.Time
	optionID               string
	answeredBy             uuid.UUID
	answered, resolved     bool
	answeredAt, resolvedAt time.Time
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
		mcpServers:   map[uuid.UUID]store.McpServer{},
		secretStores: map[uuid.UUID]store.SecretStore{},
		turns:        map[uuid.UUID]*turn{},
	}
}

// Close does nothing, since the Supervisors of a test share the Store.
func (*Store) Close() {}

// Age moves all recorded times of the turns and of their approval requests
// back by the given duration, so that tests can let time pass.
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

// begin locks mu for an operation, unless ctx is done. The caller unlocks it.
func (s *Store) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	return nil
}

// now returns the current time, strictly later than any time that it returned
// before, so that no two recorded events happen at once. The caller holds mu.
func (s *Store) now() time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(s.last) {
		now = s.last.Add(time.Microsecond)
	}
	s.last = now
	return now
}

func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// valid rejects invalid JSON, as a database does.
func valid(what string, given json.RawMessage) error {
	if !json.Valid(given) {
		return fmt.Errorf("%s: invalid JSON: %.100s", what, given)
	}
	return nil
}

// The Store copies records on the way in and out, so that callers cannot
// change them in place.

func providerOf(provider store.Provider) store.Provider {
	provider.Headers = headersOf(provider.Headers)
	provider.APIKey, provider.APIKeyAt = bytes.Clone(provider.APIKey), clone(provider.APIKeyAt)
	return provider
}

// headersOf copies the headers of an endpoint. Without headers they are
// nil, not empty.
func headersOf(headers []store.Header) []store.Header {
	if len(headers) == 0 {
		return nil
	}
	headers = slices.Clone(headers)
	for i, header := range headers {
		headers[i].Sealed, headers[i].At = bytes.Clone(header.Sealed), clone(header.At)
	}
	return headers
}

func mcpServerOf(server store.McpServer) store.McpServer {
	server.Headers = headersOf(server.Headers)
	server.Tools, server.ToolsListedAt = slices.Clone(server.Tools), clone(server.ToolsListedAt)
	if server.ToolsListedAt == nil {
		server.Tools = nil
	}
	return server
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

func (s *session) record() store.Session {
	session := s.Session
	session.EndedAt = clone(s.EndedAt)
	return session
}

// busy reports whether the session's last turn has not ended.
func (s *session) busy() bool {
	turns := len(s.turns)
	return turns > 0 && s.turns[turns-1].EndedAt == nil
}

func (t *turn) record() store.Turn {
	turn := t.Turn
	turn.StartedAt, turn.StopReason, turn.EndedAt = clone(t.StartedAt), clone(t.StopReason), clone(t.EndedAt)
	turn.CancelRequestedAt, turn.Failure, turn.Usage = clone(t.CancelRequestedAt), clone(t.Failure), usageOf(t.Usage)
	return turn
}

func (s *Store) CreateProject(ctx context.Context, project store.Project) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Project{}, err
	}
	defer s.mu.Unlock()
	for _, other := range s.projects {
		if other.Slug == project.Slug {
			return store.Project{}, store.ErrSlugTaken
		}
	}
	created := store.Project{ID: id, Slug: project.Slug, Name: project.Name, CreatedAt: s.now()}
	s.projects[id] = created
	return created, nil
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
	for _, other := range s.agents {
		if other.ProjectID == projectID && other.Slug == agent.Slug && other.DeletedAt == nil {
			return store.Agent{}, store.ErrSlugTaken
		}
	}
	if _, found := s.providers[agent.Model.Provider]; !found {
		return store.Agent{}, fmt.Errorf("provider %s does not exist", agent.Model.Provider)
	}
	if err := s.serversExist(agent.McpServers); err != nil {
		return store.Agent{}, err
	}
	now := s.now()
	created := store.Agent{
		ID: id, ProjectID: projectID, Slug: agent.Slug, Name: agent.Name, Description: agent.Description, Harness: agent.Harness,
		CreatedAt: now, UpdatedAt: now, Instructions: agent.Instructions, Model: agent.Model,
		McpServers: slices.Clone(agent.McpServers),
	}
	s.agents[id] = created
	return s.agentOf(created), nil
}

// serversExist returns an error if one of the MCP servers does not exist.
// The caller holds mu.
func (s *Store) serversExist(servers []uuid.UUID) error {
	for _, server := range servers {
		if _, found := s.mcpServers[server]; !found {
			return fmt.Errorf("MCP server %s does not exist", server)
		}
	}
	return nil
}

// agentOf copies the agent, with its MCP servers in the order of the
// project's servers. The caller holds mu.
func (s *Store) agentOf(agent store.Agent) store.Agent {
	agent.DeletedAt = clone(agent.DeletedAt)
	agent.McpServers = s.ordered(func(server uuid.UUID) bool { return slices.Contains(agent.McpServers, server) })
	return agent
}

// ordered returns the MCP servers that are chosen, in the order in which they
// were created, or nil if none is. The caller holds mu.
func (s *Store) ordered(chosen func(uuid.UUID) bool) []uuid.UUID {
	var servers []store.McpServer
	for _, server := range s.mcpServers {
		if chosen(server.ID) {
			servers = append(servers, server)
		}
	}
	slices.SortFunc(servers, func(one, other store.McpServer) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	var ids []uuid.UUID
	for _, server := range servers {
		ids = append(ids, server.ID)
	}
	return ids
}

func (s *Store) ReplaceAgent(ctx context.Context, agent store.Agent) (store.Agent, error) {
	if err := s.begin(ctx); err != nil {
		return store.Agent{}, err
	}
	defer s.mu.Unlock()
	replaced, found := s.agents[agent.ID]
	if !found {
		return store.Agent{}, store.ErrNotFound
	}
	if replaced.DeletedAt != nil {
		return store.Agent{}, store.ErrAgentDeleted
	}
	if _, found := s.providers[agent.Model.Provider]; !found {
		return store.Agent{}, fmt.Errorf("provider %s does not exist", agent.Model.Provider)
	}
	if err := s.serversExist(agent.McpServers); err != nil {
		return store.Agent{}, err
	}
	replaced.Name, replaced.Description = agent.Name, agent.Description
	replaced.Instructions, replaced.Model = agent.Instructions, agent.Model
	replaced.McpServers = slices.Clone(agent.McpServers)
	replaced.UpdatedAt = s.now()
	s.agents[agent.ID] = replaced
	return s.agentOf(replaced), nil
}

func (s *Store) DeleteAgent(ctx context.Context, id uuid.UUID) (store.Agent, []store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Agent{}, nil, err
	}
	defer s.mu.Unlock()
	agent, found := s.agents[id]
	if !found {
		return store.Agent{}, nil, store.ErrNotFound
	}
	if agent.DeletedAt != nil {
		return s.agentOf(agent), nil, nil
	}
	var live []*session
	for _, session := range s.sessions {
		if session.AgentID != id || session.EndedAt != nil {
			continue
		}
		if turns := len(session.turns); turns > 0 && session.turns[turns-1].EndedAt == nil {
			return store.Agent{}, nil, store.ErrSessionBusy
		}
		live = append(live, session)
	}
	now := s.now()
	var ended []store.Session
	for _, session := range live {
		session.EndedAt, session.EndReason = clone(&now), store.EndAgentDeleted
		ended = append(ended, session.record())
	}
	agent.DeletedAt = clone(&now)
	s.agents[id] = agent
	agent.DeletedAt = clone(&now)
	return agent, ended, nil
}

// member refers to its user by subject or, while the user is awaited, by
// username.
type member struct {
	store.Member
	subject, username string
}

// record returns a copy of the member with its user as the user is now.
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

// claim gives the members awaited under username to the user who has it. In a
// project where that user is a member already, the awaited member replaces it.
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

// projectsThat returns the projects that pick accepts, in creation order.
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
	return s.agentOf(agent), nil
}

func (s *Store) Agents(ctx context.Context, projectID uuid.UUID) ([]store.Agent, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	agents := []store.Agent{}
	for _, agent := range s.agents {
		if agent.ProjectID == projectID && agent.DeletedAt == nil {
			agents = append(agents, s.agentOf(agent))
		}
	}
	slices.SortFunc(agents, func(one, other store.Agent) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return agents, nil
}

func (s *Store) CreateSession(ctx context.Context, agentID, by uuid.UUID, title string, mcp map[uuid.UUID]bool) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	agent, found := s.agents[agentID]
	if !found {
		return store.Session{}, store.ErrNotFound
	}
	if agent.DeletedAt != nil {
		return store.Session{}, store.ErrAgentDeleted
	}
	if err := s.serversExist(slices.Collect(maps.Keys(mcp))); err != nil {
		return store.Session{}, err
	}
	created := &session{Session: store.Session{ID: id, AgentID: agentID, Title: title, CreatedBy: by, CreatedAt: s.now()}, mcp: maps.Clone(mcp)}
	s.sessions[id] = created
	return created.record(), nil
}

func (s *Store) SetSessionMcpServer(ctx context.Context, sessionID, serverID uuid.UUID, enabled bool) (store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	session, err := s.openSession(sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if err := s.serversExist([]uuid.UUID{serverID}); err != nil {
		return store.Session{}, err
	}
	session.mcp = maps.Clone(session.mcp)
	delete(session.mcp, serverID)
	if enabled != slices.Contains(s.agents[session.AgentID].McpServers, serverID) {
		if session.mcp == nil {
			session.mcp = map[uuid.UUID]bool{}
		}
		session.mcp[serverID] = enabled
	}
	return session.record(), nil
}

func (s *Store) ResetSessionMcpServers(ctx context.Context, sessionID uuid.UUID) (store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	session, err := s.openSession(sessionID)
	if err != nil {
		return store.Session{}, err
	}
	session.mcp = nil
	return session.record(), nil
}

// openSession returns the session, ErrNotFound if there is no such session,
// and ErrSessionEnded if it has ended. The caller holds mu.
func (s *Store) openSession(id uuid.UUID) (*session, error) {
	session, found := s.sessions[id]
	switch {
	case !found:
		return nil, store.ErrNotFound
	case session.EndedAt != nil:
		return nil, store.ErrSessionEnded
	}
	return session, nil
}

func (s *Store) SessionMcpServers(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	servers := map[uuid.UUID][]uuid.UUID{}
	for _, id := range sessionIDs {
		session, found := s.sessions[id]
		if !found || session.EndedAt != nil {
			continue
		}
		agent := s.agents[session.AgentID]
		on := s.ordered(func(server uuid.UUID) bool {
			if enabled, chosen := session.mcp[server]; chosen {
				return enabled
			}
			return slices.Contains(agent.McpServers, server) && s.mcpServers[server].ProjectID == agent.ProjectID
		})
		if on != nil {
			servers[id] = on
		}
	}
	return servers, nil
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

func (s *Store) SetSessionTitle(ctx context.Context, id uuid.UUID, title string) (store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return store.Session{}, err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[id]
	if !found {
		return store.Session{}, store.ErrNotFound
	}
	session.Title = title
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
	session.Sandbox, session.SandboxState = to, ""
	if to != "" {
		session.SandboxState = store.SandboxAsleep
	}
	return true, nil
}

func (s *Store) SetSandboxState(ctx context.Context, sessionID uuid.UUID, sandbox, state string) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	session, found := s.sessions[sessionID]
	if !found || sandbox == "" || session.Sandbox != sandbox {
		return nil
	}
	session.SandboxState = state
	return nil
}

func (s *Store) SessionsWithSandbox(ctx context.Context) ([]store.Session, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	sessions := []store.Session{}
	for _, session := range s.sessions {
		if session.Sandbox != "" {
			sessions = append(sessions, session.record())
		}
	}
	return sessions, nil
}

func (s *Store) SandboxCounts(ctx context.Context) (map[string]int, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	counts := map[string]int{}
	for _, session := range s.sessions {
		if session.Sandbox != "" && session.EndedAt == nil {
			counts[session.SandboxState]++
		}
	}
	return counts, nil
}

// open returns the session if it has not ended.
func (s *Store) open(sessionID uuid.UUID) (*session, error) {
	session, found := s.sessions[sessionID]
	if !found {
		return nil, store.ErrNotFound
	}
	if session.EndedAt != nil {
		return nil, store.ErrSessionEnded
	}
	return session, nil
}

func (s *Store) ExpectAttachments(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	session, err := s.open(sessionID)
	if err != nil {
		return err
	}
	session.attachments = true
	return nil
}

func (s *Store) CreateAttachment(ctx context.Context, attachment store.Attachment) (store.Attachment, error) {
	if err := s.begin(ctx); err != nil {
		return store.Attachment{}, err
	}
	defer s.mu.Unlock()
	if _, err := s.open(attachment.SessionID); err != nil {
		return store.Attachment{}, err
	}
	attachment.TurnID, attachment.CreatedAt = uuid.Nil, s.now()
	s.attachments = append(s.attachments, &attachment)
	return attachment, nil
}

func (s *Store) Attachment(ctx context.Context, id uuid.UUID) (store.Attachment, error) {
	if err := s.begin(ctx); err != nil {
		return store.Attachment{}, err
	}
	defer s.mu.Unlock()
	for _, attachment := range s.attachments {
		if attachment.ID == id {
			return *attachment, nil
		}
	}
	return store.Attachment{}, store.ErrNotFound
}

func (s *Store) TurnAttachments(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]store.Attachment, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	carried := map[uuid.UUID][]store.Attachment{}
	for _, id := range turnIDs {
		if turn, found := s.turns[id]; found {
			for _, attachment := range turn.attachments {
				carried[id] = append(carried[id], *attachment)
			}
		}
	}
	return carried, nil
}

func (s *Store) UnsentAttachments(ctx context.Context, before time.Time, limit int) ([]store.Attachment, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	unsent := []store.Attachment{}
	for _, attachment := range s.attachments {
		if attachment.TurnID == uuid.Nil && attachment.CreatedAt.Before(before) && len(unsent) < limit {
			unsent = append(unsent, *attachment)
		}
	}
	return unsent, nil
}

func (s *Store) DeleteAttachment(ctx context.Context, id uuid.UUID) (bool, error) {
	if err := s.begin(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.attachments, func(attachment *store.Attachment) bool {
		return attachment.ID == id && attachment.TurnID == uuid.Nil
	})
	if at < 0 {
		return false, nil
	}
	s.attachments = slices.Delete(s.attachments, at, at+1)
	return true, nil
}

func (s *Store) LeftAttachments(ctx context.Context, before time.Time, limit int) ([]uuid.UUID, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	var ended []*session
	for _, session := range s.sessions {
		if session.attachments && session.EndedAt != nil && session.EndedAt.Before(before) {
			ended = append(ended, session)
		}
	}
	slices.SortFunc(ended, func(a, b *session) int { return a.EndedAt.Compare(*b.EndedAt) })
	left := []uuid.UUID{}
	for _, session := range ended[:min(limit, len(ended))] {
		left = append(left, session.ID)
	}
	return left, nil
}

func (s *Store) ForgetAttachments(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if session, found := s.sessions[sessionID]; found && session.EndedAt != nil {
		session.attachments = false
	}
	return nil
}

func (s *Store) EndSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
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
		session.EndedAt, session.EndReason = &ended, reason
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
		return store.Provider{}, fmt.Errorf("provider %s already exists", provider.ID)
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

// keyStore returns an error if the provider's key is in a secret store that
// does not exist. The caller holds mu.
func (s *Store) keyStore(provider store.Provider) error {
	if at := provider.APIKeyAt; at != nil {
		if _, found := s.secretStores[at.Store]; !found {
			return fmt.Errorf("secret store %s does not exist", at.Store)
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

func (s *Store) Providers(ctx context.Context, projectID uuid.UUID) ([]store.Provider, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	providers := []store.Provider{}
	for _, provider := range s.providers {
		if provider.ProjectID == projectID {
			providers = append(providers, providerOf(provider))
		}
	}
	slices.SortFunc(providers, func(one, other store.Provider) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return providers, nil
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
	replaced.Name, replaced.URL, replaced.Headers = provider.Name, provider.URL, headersOf(provider.Headers)
	replaced.APIKey, replaced.APIKeyAt = bytes.Clone(provider.APIKey), clone(provider.APIKeyAt)
	replaced.UpdatedAt = s.now()
	s.providers[replaced.ID] = replaced
	return providerOf(replaced), nil
}

func (s *Store) CreateMcpServer(ctx context.Context, server store.McpServer) (store.McpServer, error) {
	if err := s.begin(ctx); err != nil {
		return store.McpServer{}, err
	}
	defer s.mu.Unlock()
	if _, found := s.projects[server.ProjectID]; !found {
		return store.McpServer{}, store.ErrNotFound
	}
	if _, found := s.mcpServers[server.ID]; found {
		return store.McpServer{}, fmt.Errorf("MCP server %s already exists", server.ID)
	}
	for _, other := range s.mcpServers {
		if other.ProjectID == server.ProjectID && other.Slug == server.Slug {
			return store.McpServer{}, store.ErrSlugTaken
		}
	}
	created := mcpServerOf(server)
	created.CreatedAt = s.now()
	created.UpdatedAt = created.CreatedAt
	s.mcpServers[created.ID] = created
	return mcpServerOf(created), nil
}

func (s *Store) McpServer(ctx context.Context, id uuid.UUID) (store.McpServer, error) {
	if err := s.begin(ctx); err != nil {
		return store.McpServer{}, err
	}
	defer s.mu.Unlock()
	server, found := s.mcpServers[id]
	if !found {
		return store.McpServer{}, store.ErrNotFound
	}
	return mcpServerOf(server), nil
}

func (s *Store) McpServers(ctx context.Context, projectID uuid.UUID) ([]store.McpServer, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	servers := []store.McpServer{}
	for _, server := range s.mcpServers {
		if server.ProjectID == projectID {
			servers = append(servers, mcpServerOf(server))
		}
	}
	slices.SortFunc(servers, func(one, other store.McpServer) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return servers, nil
}

func (s *Store) ReplaceMcpServer(ctx context.Context, server store.McpServer) (store.McpServer, error) {
	if err := s.begin(ctx); err != nil {
		return store.McpServer{}, err
	}
	defer s.mu.Unlock()
	replaced, found := s.mcpServers[server.ID]
	if !found {
		return store.McpServer{}, store.ErrNotFound
	}
	given := mcpServerOf(server)
	replaced.Name, replaced.URL, replaced.Transport, replaced.Headers = given.Name, given.URL, given.Transport, given.Headers
	replaced.Tools, replaced.ToolsListedAt = given.Tools, given.ToolsListedAt
	replaced.UpdatedAt = s.now()
	s.mcpServers[replaced.ID] = replaced
	return mcpServerOf(replaced), nil
}

func (s *Store) SetMcpServerTools(ctx context.Context, id uuid.UUID, tools []store.Tool, listedAt time.Time) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	server, found := s.mcpServers[id]
	if !found {
		return store.ErrNotFound
	}
	server.Tools, server.ToolsListedAt = slices.Clone(tools), &listedAt
	s.mcpServers[id] = server
	return nil
}

func (s *Store) DeleteMcpServer(ctx context.Context, id uuid.UUID) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if _, found := s.mcpServers[id]; !found {
		return store.ErrNotFound
	}
	delete(s.mcpServers, id)
	// Agents and sessions no longer have the server.
	for agentID, agent := range s.agents {
		if slices.Contains(agent.McpServers, id) {
			agent.McpServers = slices.DeleteFunc(slices.Clone(agent.McpServers), func(other uuid.UUID) bool { return other == id })
			s.agents[agentID] = agent
		}
	}
	for _, session := range s.sessions {
		if _, chosen := session.mcp[id]; chosen {
			session.mcp = maps.Clone(session.mcp)
			delete(session.mcp, id)
		}
	}
	return nil
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
		return store.SecretStore{}, fmt.Errorf("secret store %s already exists", secrets.ID)
	}
	if err := valid("secret store settings", secrets.Settings); err != nil {
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

func (s *Store) SecretStores(ctx context.Context, projectID uuid.UUID) ([]store.SecretStore, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	stores := []store.SecretStore{}
	for _, secrets := range s.secretStores {
		if secrets.ProjectID == projectID {
			stores = append(stores, secretStoreOf(secrets))
		}
	}
	slices.SortFunc(stores, func(one, other store.SecretStore) int {
		return cmp.Or(one.CreatedAt.Compare(other.CreatedAt), bytes.Compare(one.ID[:], other.ID[:]))
	})
	return stores, nil
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
	if err := valid("secret store settings", secrets.Settings); err != nil {
		return store.SecretStore{}, err
	}
	replaced.Name, replaced.Kind = secrets.Name, secrets.Kind
	replaced.Settings, replaced.Credential = bytes.Clone(secrets.Settings), bytes.Clone(secrets.Credential)
	replaced.UpdatedAt = s.now()
	s.secretStores[replaced.ID] = replaced
	return secretStoreOf(replaced), nil
}

// queued returns the queued turns, oldest first. The caller holds mu.
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

func (s *Store) CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, attachments []uuid.UUID, maxWaiting int) (store.Turn, int, error) {
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
	carried := make([]*store.Attachment, len(attachments))
	names := make([]string, len(attachments))
	for i, attachment := range attachments {
		at := slices.IndexFunc(s.attachments, func(other *store.Attachment) bool {
			return other.ID == attachment && other.SessionID == sessionID && other.TurnID == uuid.Nil
		})
		// An ID that is named twice is found among those before it.
		if at < 0 || slices.Contains(carried[:i], s.attachments[at]) {
			return store.Turn{}, 0, store.ErrUnknownAttachment
		}
		carried[i], names[i] = s.attachments[at], s.attachments[at].Name
	}
	waiting := len(s.queued()) + 1
	if maxWaiting > 0 && waiting > maxWaiting {
		return store.Turn{}, 0, store.ErrQueueFull
	}
	created := &turn{Turn: store.Turn{
		ID: id, SessionID: sessionID, Seq: len(session.turns) + 1, State: store.TurnQueued, Text: text, CreatedBy: by, CreatedAt: s.now(),
	}, attachments: carried}
	for i, name := range store.UniqueNames(names) {
		carried[i].TurnID, carried[i].Name = id, name
	}
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

func (s *Store) LastTurns(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID]store.Turn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	last := map[uuid.UUID]store.Turn{}
	for _, id := range sessionIDs {
		if session, found := s.sessions[id]; found && len(session.turns) > 0 {
			last[id] = session.turns[len(session.turns)-1].record()
		}
	}
	return last, nil
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

func cloned(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for i, value := range values {
		cloned[i] = bytes.Clone(value)
	}
	return cloned
}

func (s *Store) NextTurns(ctx context.Context, limit int) ([]store.QueuedTurn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	queued := s.queued()
	next := make([]store.QueuedTurn, 0, min(limit, len(queued)))
	now := s.now()
	for _, turn := range queued[:min(limit, len(queued))] {
		session := s.sessions[turn.SessionID]
		next = append(next, store.QueuedTurn{
			TurnID: turn.ID, SessionID: turn.SessionID, Harness: s.agents[session.AgentID].Harness,
			Sandbox: session.Sandbox, SandboxState: session.SandboxState, Waited: now.Sub(turn.CreatedAt),
		})
	}
	return next, nil
}

func (s *Store) DispatchTurn(ctx context.Context, turnID uuid.UUID) (bool, error) {
	if err := s.begin(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	if !found || turn.State != store.TurnQueued {
		return false, nil
	}
	now := s.now()
	turn.State, turn.dispatchedAt = store.TurnDispatching, &now
	return true, nil
}

func (s *Store) IdleSandboxes(ctx context.Context, limit int, waiting bool) ([]store.IdleSandbox, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	now := s.now()
	var idle []store.IdleSandbox
	for _, session := range s.sessions {
		if session.Sandbox == "" || session.SandboxState != store.SandboxAwake {
			continue
		}
		since := session.CreatedAt
		if last := len(session.turns); last > 0 {
			turn := session.turns[last-1]
			switch asked := turn.asked(); {
			case turn.EndedAt != nil:
				since = *turn.EndedAt
			case waiting && turn.CancelRequestedAt == nil && !asked.IsZero():
				since = asked
			default:
				continue
			}
		}
		idle = append(idle, store.IdleSandbox{SessionID: session.ID, Sandbox: session.Sandbox, Idle: now.Sub(since)})
	}
	slices.SortFunc(idle, func(one, other store.IdleSandbox) int {
		if order := cmp.Compare(other.Idle, one.Idle); order != 0 {
			return order
		}
		return bytes.Compare(one.SessionID[:], other.SessionID[:])
	})
	return idle[:min(limit, len(idle))], nil
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

func (s *Store) UnfinishedTurns(ctx context.Context) ([]store.UnfinishedTurn, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	now, unfinished := s.now(), []store.UnfinishedTurn{}
	for _, turn := range s.created {
		if turn.EndedAt != nil {
			continue
		}
		one := store.UnfinishedTurn{
			Harness: s.agents[s.sessions[turn.SessionID].AgentID].Harness, State: turn.State,
			Started: turn.StartedAt != nil, Age: now.Sub(turn.CreatedAt),
		}
		if turn.dispatchedAt != nil {
			one.Age = now.Sub(*turn.dispatchedAt)
		}
		for _, approval := range turn.approvals {
			if !approval.answered && !approval.resolved {
				one.Asking++
			}
		}
		unfinished = append(unfinished, one)
	}
	return unfinished, nil
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
		Harness: agent.Harness, Instructions: agent.Instructions,
		ProviderURL: s.providers[agent.Model.Provider].URL, Model: agent.Model.Name,
		Sandbox: session.Sandbox, SandboxState: session.SandboxState, Dispatched: now.Sub(turn.CreatedAt),
	}
	if turn.dispatchedAt != nil {
		followed.Dispatched = now.Sub(*turn.dispatchedAt)
	}
	return followed, nil
}

func (s *Store) SessionTurn(ctx context.Context, sessionID uuid.UUID) (store.Origin, error) {
	if err := s.begin(ctx); err != nil {
		return store.Origin{}, err
	}
	defer s.mu.Unlock()
	for _, turn := range s.created {
		if turn.State == store.TurnRunning && turn.SessionID == sessionID {
			return s.originOf(turn), nil
		}
	}
	return store.Origin{}, store.ErrNotFound
}

func (s *Store) LastTurn(ctx context.Context, sessionID uuid.UUID) (store.Origin, error) {
	if err := s.begin(ctx); err != nil {
		return store.Origin{}, err
	}
	defer s.mu.Unlock()
	var last *turn
	for _, turn := range s.created {
		if turn.SessionID == sessionID && turn.dispatchedAt != nil && (last == nil || turn.Seq > last.Seq) {
			last = turn
		}
	}
	if last == nil {
		return store.Origin{}, store.ErrNotFound
	}
	origin := s.originOf(last)
	origin.Provider, origin.SecretStore = store.Provider{}, nil
	return origin, nil
}

// originOf returns the origin of the turn. The caller holds mu.
func (s *Store) originOf(turn *turn) store.Origin {
	agent := s.agents[s.sessions[turn.SessionID].AgentID]
	origin := store.Origin{TurnID: turn.ID, SessionID: turn.SessionID, AgentID: agent.ID, ProjectID: agent.ProjectID, UserID: turn.CreatedBy}
	for _, user := range s.users {
		if user.ID == turn.CreatedBy {
			origin.Subject = user.Subject
		}
	}
	provider := s.providers[agent.Model.Provider]
	origin.Provider = store.Provider{
		ID: provider.ID, ProjectID: agent.ProjectID, URL: provider.URL, Headers: headersOf(provider.Headers),
		APIKey: bytes.Clone(provider.APIKey), APIKeyAt: clone(provider.APIKeyAt),
	}
	if provider.APIKeyAt != nil {
		secrets := secretStoreOf(s.secretStores[provider.APIKeyAt.Store])
		origin.SecretStore = &secrets
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

// EndTurn calls items with mu held, as the database stores call it with the
// turn locked.
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
		return store.Turn{}, false, fmt.Errorf("read turn items: %w", err)
	}
	for _, item := range said {
		if err := valid("turn item", item); err != nil {
			return store.Turn{}, false, err
		}
	}
	now := s.now()
	turn.EndedAt, turn.items = &now, cloned(said)
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

// asked returns the turn's approval request with the given ID and the turn,
// each nil if it does not exist. The caller holds mu.
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
		return errors.New("turn does not exist")
	}
	if err := valid("approval request", request); err != nil {
		return err
	}
	// A runner may report a request again.
	if kept == nil {
		turn.approvals = append(turn.approvals, &approval{id: id, request: bytes.Clone(request), requestedAt: s.now()})
	}
	return nil
}

// ResolveApproval does not keep the outcome, which no Store method returns.
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
	kept.optionID, kept.answeredBy, kept.answered, kept.answeredAt = optionID, by, true, s.now()
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

func (s *Store) WaitedForAnswers(ctx context.Context, turnID uuid.UUID) (time.Duration, error) {
	if err := s.begin(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	turn, found := s.turns[turnID]
	if !found {
		return 0, nil
	}
	now := s.now()
	var waited time.Duration
	for _, approval := range turn.approvals {
		until := now
		switch {
		case approval.answered:
			until = approval.answeredAt
		case approval.resolved:
			until = approval.resolvedAt
		}
		waited += until.Sub(approval.requestedAt)
	}
	return waited, nil
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
