package mysql

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"aviary/supervisor/store"
	"aviary/supervisor/store/mysql/internal/db"
	"aviary/supervisor/store/mysql/internal/key"
)

// MySQL error numbers.
const (
	duplicateEntry = 1062
	deadlock       = 1213
)

// The unique keys on slugs, as MySQL names them in errors.
const (
	projectSlug   = "projects.projects_slug"
	agentSlug     = "agents.agents_project_slug"
	mcpServerSlug = "mcp_servers.mcp_servers_project_slug"
)

// Rows of the locks table.
const (
	dispatchLock = "dispatch"
	queueLock    = "queue"
)

type records struct {
	pool    *sql.DB
	queries *db.Queries
}

// Open connects to the database at address and checks that its schema suits
// this build.
func (Plugin) Open(ctx context.Context, address string) (store.Store, error) {
	pool, err := connect(address)
	if err != nil {
		return nil, err
	}
	if err := checkSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &records{pool: pool, queries: db.New(pool)}, nil
}

func (s *records) Close() {
	s.pool.Close()
}

// within runs run in a transaction, and commits it if run succeeds. It retries
// a transaction that MySQL rolled back to break a deadlock.
func (s *records) within(ctx context.Context, run func(queries *db.Queries) error) error {
	for attempt := 1; ; attempt++ {
		err := s.once(ctx, run)
		if attempt < 5 && failed(err, deadlock) {
			continue
		}
		return err
	}
}

func (s *records) once(ctx context.Context, run func(queries *db.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := run(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// failed reports whether err is the MySQL error with the number.
func failed(err error, number uint16) bool {
	var failure *mysql.MySQLError
	return errors.As(err, &failure) && failure.Number == number
}

// duplicate reports whether err is a duplicate entry for the unique key. The
// key is named only in the message, which ends with it.
func duplicate(err error, key string) bool {
	var failure *mysql.MySQLError
	return errors.As(err, &failure) && failure.Number == duplicateEntry && strings.HasSuffix(failure.Message, " for key '"+key+"'")
}

// found maps sql.ErrNoRows to store.ErrNotFound.
func found(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// created returns store.ErrNotFound if a statement affected no rows, as when
// the record that it depends on does not exist.
func created(rows int64, err error) error {
	if err == nil && rows == 0 {
		return store.ErrNotFound
	}
	return err
}

// document dereferences a nullable JSON column.
func document(column *json.RawMessage) json.RawMessage {
	if column == nil {
		return nil
	}
	return *column
}

func since(microseconds int64) time.Duration {
	return time.Duration(microseconds) * time.Microsecond
}

func (s *records) CreateProject(ctx context.Context, project store.Project) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	params := db.CreateProjectParams{ID: key.ID(id), Slug: project.Slug, Name: project.Name}
	switch err := s.queries.CreateProject(ctx, params); {
	case duplicate(err, projectSlug):
		return store.Project{}, store.ErrSlugTaken
	case err != nil:
		return store.Project{}, err
	}
	return s.Project(ctx, id)
}

func (s *records) Project(ctx context.Context, id uuid.UUID) (store.Project, error) {
	row, err := s.queries.GetProject(ctx, key.ID(id))
	return store.Project{ID: uuid.UUID(row.ID), Slug: row.Slug, Name: row.Name, CreatedAt: row.CreatedAt}, found(err)
}

func (s *records) Projects(ctx context.Context) ([]store.Project, error) {
	rows, err := s.queries.ListProjects(ctx)
	return projectsFrom(rows), err
}

func (s *records) ProjectsOf(ctx context.Context, userID uuid.UUID, groups []string) ([]store.Project, error) {
	var params db.ListProjectsOfMemberParams
	if userID != uuid.Nil {
		params.UserID = (*key.ID)(&userID)
	}
	for _, group := range groups {
		params.GroupNames = append(params.GroupNames, &group)
	}
	rows, err := s.queries.ListProjectsOfMember(ctx, params)
	return projectsFrom(rows), err
}

func projectsFrom(rows []db.Project) []store.Project {
	projects := make([]store.Project, len(rows))
	for i, row := range rows {
		projects[i] = store.Project{ID: uuid.UUID(row.ID), Slug: row.Slug, Name: row.Name, CreatedAt: row.CreatedAt}
	}
	return projects
}

func (s *records) Roles(ctx context.Context) ([]store.Role, error) {
	rows, err := s.queries.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	roles := make([]store.Role, len(rows))
	for i, row := range rows {
		if roles[i], err = roleFrom(row); err != nil {
			return nil, err
		}
	}
	return roles, nil
}

func (s *records) SetRole(ctx context.Context, name string, permissions []string, by uuid.UUID) (store.Role, error) {
	// An empty array, not null: a role may have no permissions.
	encoded, err := json.Marshal(append([]string{}, permissions...))
	if err != nil {
		return store.Role{}, err
	}
	params := db.SetRoleParams{Name: name, Permissions: encoded}
	if by != uuid.Nil {
		params.UpdatedBy = (*key.ID)(&by)
	}
	if err := s.queries.SetRole(ctx, params); err != nil {
		return store.Role{}, err
	}
	row, err := s.queries.GetRole(ctx, name)
	if err != nil {
		return store.Role{}, found(err)
	}
	return roleFrom(row)
}

func roleFrom(row db.Role) (store.Role, error) {
	role := store.Role{Name: row.Name, UpdatedAt: row.UpdatedAt}
	if row.UpdatedBy != nil {
		role.UpdatedBy = uuid.UUID(*row.UpdatedBy)
	}
	return role, json.Unmarshal(row.Permissions, &role.Permissions)
}

func (s *records) UserOf(ctx context.Context, subject, username, displayName string) (store.User, error) {
	// Most requests come from a user whose details have not changed.
	row, err := s.queries.GetUserBySubject(ctx, subject)
	if err == nil && row.DisplayName == displayName && (username == "" || (row.Username != nil && *row.Username == username)) {
		return userFrom(row), nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.User{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.User{}, err
	}
	var given *string
	if username != "" {
		if err := s.queries.FreeUsername(ctx, db.FreeUsernameParams{Username: &username, Subject: subject}); err != nil {
			return store.User{}, err
		}
		given = &username
	}
	// The user may exist, or another Supervisor may create it concurrently.
	err = s.queries.CreateUser(ctx, db.CreateUserParams{ID: key.ID(id), Subject: subject, Username: given, DisplayName: displayName})
	if failed(err, duplicateEntry) {
		err = s.queries.UpdateUser(ctx, db.UpdateUserParams{Username: given, DisplayName: displayName, Subject: subject})
	}
	if err == nil && username != "" {
		err = s.claim(ctx, username)
	}
	if err != nil {
		return store.User{}, err
	}
	row, err = s.queries.GetUserBySubject(ctx, subject)
	return userFrom(row), err
}

// claim gives the members awaited under username to the user who has it.
func (s *records) claim(ctx context.Context, username string) error {
	if err := s.queries.DropMembersBeforeClaim(ctx, db.DropMembersBeforeClaimParams{Username: &username}); err != nil {
		return err
	}
	return s.queries.ClaimMembers(ctx, &username)
}

func (s *records) AddMember(ctx context.Context, member store.Member, by uuid.UUID) (store.Member, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Member{}, err
	}
	params := db.AddMemberParams{ID: key.ID(id), ProjectID: key.ID(member.ProjectID)}
	if member.Role != "" {
		params.Role = &member.Role
	} else if params.Permissions, err = encoded(member.Permissions); err != nil {
		return store.Member{}, err
	}
	if by != uuid.Nil {
		params.UpdatedBy = (*key.ID)(&by)
	}
	var username string
	if member.User != nil {
		username = member.User.Username
		params.Username = &username
		if is, err := s.queries.IsMember(ctx, db.IsMemberParams{ProjectID: key.ID(member.ProjectID), Username: &username}); err != nil || is {
			return store.Member{}, cmp.Or(err, store.ErrMemberExists)
		}
	} else {
		params.GroupName = &member.Group
	}
	switch err := created(s.queries.AddMember(ctx, params)); {
	case failed(err, duplicateEntry):
		return store.Member{}, store.ErrMemberExists
	case err != nil:
		return store.Member{}, err
	}
	if username != "" {
		if err := s.claim(ctx, username); err != nil {
			return store.Member{}, err
		}
	}
	return s.member(ctx, id)
}

// encoded returns the permissions as a JSON array, never null.
func encoded(permissions []string) (*json.RawMessage, error) {
	list, err := json.Marshal(append([]string{}, permissions...))
	return (*json.RawMessage)(&list), err
}

func (s *records) SetMember(ctx context.Context, id uuid.UUID, role string, permissions []string, by uuid.UUID) (store.Member, error) {
	params := db.SetMemberParams{ID: key.ID(id)}
	var err error
	if role != "" {
		params.Role = &role
	} else if params.Permissions, err = encoded(permissions); err != nil {
		return store.Member{}, err
	}
	if by != uuid.Nil {
		params.UpdatedBy = (*key.ID)(&by)
	}
	if err := s.queries.SetMember(ctx, params); err != nil {
		return store.Member{}, err
	}
	return s.member(ctx, id)
}

func (s *records) RemoveMember(ctx context.Context, id uuid.UUID) error {
	return created(s.queries.RemoveMember(ctx, key.ID(id)))
}

func (s *records) Members(ctx context.Context, projectID uuid.UUID) ([]store.Member, error) {
	rows, err := s.queries.ListMembers(ctx, key.ID(projectID))
	if err != nil {
		return nil, err
	}
	members := make([]store.Member, len(rows))
	for i, row := range rows {
		if members[i], err = memberFrom(db.GetMemberRow(row)); err != nil {
			return nil, err
		}
	}
	return members, nil
}

func (s *records) Permissions(ctx context.Context, projectID, userID uuid.UUID, groups []string) ([]string, error) {
	params := db.ListPermissionsParams{ProjectID: key.ID(projectID)}
	if userID != uuid.Nil {
		params.UserID = (*key.ID)(&userID)
	}
	for _, group := range groups {
		params.GroupNames = append(params.GroupNames, &group)
	}
	sets, err := s.queries.ListPermissions(ctx, params)
	if err != nil {
		return nil, err
	}
	var held []string
	for _, set := range sets {
		var permissions []string
		if err := json.Unmarshal(set, &permissions); err != nil {
			return nil, err
		}
		held = append(held, permissions...)
	}
	slices.Sort(held)
	return slices.Compact(held), nil
}

func (s *records) member(ctx context.Context, id uuid.UUID) (store.Member, error) {
	row, err := s.queries.GetMember(ctx, key.ID(id))
	if err != nil {
		return store.Member{}, found(err)
	}
	return memberFrom(row)
}

func memberFrom(row db.GetMemberRow) (store.Member, error) {
	member := store.Member{ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.GroupName != nil {
		member.Group = *row.GroupName
	}
	if row.Role != nil {
		member.Role = *row.Role
	}
	if row.UpdatedBy != nil {
		member.UpdatedBy = uuid.UUID(*row.UpdatedBy)
	}
	switch {
	case row.UserID != nil:
		member.User = &store.User{ID: uuid.UUID(*row.UserID), Subject: *row.Subject, DisplayName: row.DisplayName.String, CreatedAt: *row.UserCreatedAt}
		if row.UserUsername != nil {
			member.User.Username = *row.UserUsername
		}
	case row.Username != nil:
		member.User = &store.User{Username: *row.Username}
	}
	if row.Permissions == nil {
		return member, nil
	}
	return member, json.Unmarshal(*row.Permissions, &member.Permissions)
}

func userFrom(row db.User) store.User {
	user := store.User{ID: uuid.UUID(row.ID), Subject: row.Subject, DisplayName: row.DisplayName, CreatedAt: row.CreatedAt}
	if row.Username != nil {
		user.Username = *row.Username
	}
	return user
}

func (s *records) CreateAgent(ctx context.Context, projectID uuid.UUID, agent store.Agent) (store.Agent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Agent{}, err
	}
	params := db.CreateAgentParams{
		ID: key.ID(id), ProjectID: key.ID(projectID), Slug: agent.Slug, Name: agent.Name, Description: agent.Description,
		Harness: agent.Harness, Instructions: agent.Instructions, Approval: agent.Approval,
		ProviderID: key.ID(agent.Model.Provider), Model: agent.Model.Name,
	}
	switch err := created(s.queries.CreateAgent(ctx, params)); {
	case duplicate(err, agentSlug):
		return store.Agent{}, store.ErrSlugTaken
	case err != nil:
		return store.Agent{}, err
	}
	return s.Agent(ctx, id)
}

func (s *records) Agent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	row, err := s.queries.GetAgent(ctx, key.ID(id))
	if err != nil {
		return store.Agent{}, found(err)
	}
	return agentFrom(row), nil
}

func (s *records) ReplaceAgent(ctx context.Context, agent store.Agent) (store.Agent, error) {
	params := db.ReplaceAgentParams{
		ID: key.ID(agent.ID), Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions,
		Approval: agent.Approval, ProviderID: key.ID(agent.Model.Provider), Model: agent.Model.Name,
	}
	if err := created(s.queries.ReplaceAgent(ctx, params)); errors.Is(err, store.ErrNotFound) {
		return store.Agent{}, s.deleted(ctx, agent.ID)
	} else if err != nil {
		return store.Agent{}, err
	}
	return s.Agent(ctx, agent.ID)
}

// deleted returns ErrAgentDeleted if the agent exists, which is why a
// statement for agents that are not deleted found none, and ErrNotFound if
// it does not.
func (s *records) deleted(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Agent(ctx, id); err != nil {
		return err
	}
	return store.ErrAgentDeleted
}

func (s *records) DeleteAgent(ctx context.Context, id uuid.UUID) (store.Agent, []store.Session, error) {
	var agent store.Agent
	var ended []store.Session
	err := s.within(ctx, func(queries *db.Queries) error {
		ended = nil
		// Lock the agent, then its sessions, before checking their turns: a
		// concurrent session or message either is there by then, or finds
		// the agent deleted and the session ended.
		row, err := queries.LockAgent(ctx, key.ID(id))
		if err != nil {
			return found(err)
		}
		if row.DeletedAt == nil {
			if _, err := queries.LockAgentSessions(ctx, key.ID(id)); err != nil {
				return err
			}
			busy, err := queries.IsAgentBusy(ctx, key.ID(id))
			if err != nil {
				return err
			}
			if busy {
				return store.ErrSessionBusy
			}
			if err := queries.EndAgentSessions(ctx, key.ID(id)); err != nil {
				return err
			}
			rows, err := queries.ListSessionsEndedWithAgent(ctx, key.ID(id))
			if err != nil {
				return err
			}
			for _, row := range rows {
				ended = append(ended, sessionFrom(row))
			}
			if err := queries.DeleteAgent(ctx, key.ID(id)); err != nil {
				return err
			}
			if row, err = queries.GetAgent(ctx, key.ID(id)); err != nil {
				return err
			}
		}
		agent = agentFrom(row)
		return nil
	})
	if err != nil {
		return store.Agent{}, nil, err
	}
	return agent, ended, nil
}

func (s *records) Agents(ctx context.Context, projectID uuid.UUID) ([]store.Agent, error) {
	rows, err := s.queries.ListAgents(ctx, key.ID(projectID))
	if err != nil {
		return nil, err
	}
	agents := make([]store.Agent, len(rows))
	for i, row := range rows {
		agents[i] = agentFrom(row)
	}
	return agents, nil
}

func agentFrom(row db.Agent) store.Agent {
	return store.Agent{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Slug: row.Slug, Name: row.Name, Description: row.Description,
		Harness: row.Harness, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, DeletedAt: row.DeletedAt,
		Instructions: row.Instructions, Approval: row.Approval,
		Model: store.Model{Provider: uuid.UUID(row.ProviderID), Name: row.Model},
	}
}

func (s *records) CreateSession(ctx context.Context, agentID, by uuid.UUID, title string) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	params := db.CreateSessionParams{ID: key.ID(id), AgentID: key.ID(agentID), Title: title}
	if by != uuid.Nil {
		params.CreatedBy = (*key.ID)(&by)
	}
	// The agent is held, so that it is not deleted before it has the session.
	err = s.within(ctx, func(queries *db.Queries) error {
		if _, err := queries.HoldAgent(ctx, key.ID(agentID)); err != nil {
			return found(err)
		}
		return created(queries.CreateSession(ctx, params))
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, s.deleted(ctx, agentID)
	} else if err != nil {
		return store.Session{}, err
	}
	return s.Session(ctx, id)
}

func (s *records) Session(ctx context.Context, id uuid.UUID) (store.Session, error) {
	row, err := s.queries.GetSession(ctx, key.ID(id))
	return sessionFrom(row), found(err)
}

func (s *records) SetSessionTitle(ctx context.Context, id uuid.UUID, title string) (store.Session, error) {
	if err := created(s.queries.SetSessionTitle(ctx, db.SetSessionTitleParams{ID: key.ID(id), Title: title})); err != nil {
		return store.Session{}, err
	}
	return s.Session(ctx, id)
}

func (s *records) SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error) {
	set := false
	err := s.within(ctx, func(queries *db.Queries) error {
		if _, err := queries.LockSession(ctx, key.ID(sessionID)); err != nil {
			return found(err)
		}
		row, err := queries.GetSession(ctx, key.ID(sessionID))
		if err != nil {
			return err
		}
		// Set by every attempt, since an attempt that deadlocked was rolled
		// back.
		if set = sessionFrom(row).Sandbox == from; !set {
			return nil
		}
		params := db.SetSandboxParams{ID: key.ID(sessionID)}
		if to != "" {
			params.Sandbox = &to
		}
		return queries.SetSandbox(ctx, params)
	})
	return set && err == nil, err
}

func (s *records) SetSandboxState(ctx context.Context, sessionID uuid.UUID, sandbox, state string) error {
	return s.queries.SetSandboxState(ctx, db.SetSandboxStateParams{ID: key.ID(sessionID), Sandbox: &sandbox, State: &state})
}

func (s *records) SessionsWithSandbox(ctx context.Context) ([]store.Session, error) {
	rows, err := s.queries.ListSessionsWithSandbox(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]store.Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFrom(row)
	}
	return sessions, nil
}

func (s *records) SandboxCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.queries.CountSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		if row.State != nil {
			counts[*row.State] = int(row.Sandboxes)
		}
	}
	return counts, nil
}

func (s *records) EndSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
	var session store.Session
	params := db.EndSessionParams{ID: key.ID(id)}
	if reason != "" {
		params.Reason = &reason
	}
	err := s.within(ctx, func(queries *db.Queries) error {
		// Lock the session before checking its turns, so that a concurrent
		// message either has added its turn or finds the session ended.
		row, err := queries.LockSession(ctx, key.ID(id))
		if err != nil {
			return found(err)
		}
		if row.EndedAt == nil {
			busy, err := queries.IsSessionBusy(ctx, key.ID(id))
			if err != nil {
				return err
			}
			if busy {
				return store.ErrSessionBusy
			}
			if err := queries.EndSession(ctx, params); err != nil {
				return err
			}
			if row, err = queries.GetSession(ctx, key.ID(id)); err != nil {
				return err
			}
		}
		session = sessionFrom(row)
		return nil
	})
	if err != nil {
		return store.Session{}, err
	}
	return session, nil
}

func (s *records) Sessions(ctx context.Context, agentID, by, before uuid.UUID, limit int) ([]store.Session, error) {
	var rows []db.Session
	var err error
	if by == uuid.Nil {
		rows, err = s.queries.ListSessions(ctx, db.ListSessionsParams{AgentID: key.ID(agentID), Before: key.ID(before), Limit: int32(limit)})
	} else {
		rows, err = s.queries.ListSessionsOf(ctx, db.ListSessionsOfParams{
			AgentID: key.ID(agentID), CreatedBy: (*key.ID)(&by), Before: key.ID(before), Limit: int32(limit),
		})
	}
	if err != nil {
		return nil, err
	}
	sessions := make([]store.Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFrom(row)
	}
	return sessions, nil
}

func sessionFrom(row db.Session) store.Session {
	session := store.Session{
		ID: uuid.UUID(row.ID), AgentID: uuid.UUID(row.AgentID), Title: row.Title, CreatedAt: row.CreatedAt, EndedAt: row.EndedAt,
	}
	if row.CreatedBy != nil {
		session.CreatedBy = uuid.UUID(*row.CreatedBy)
	}
	if row.Sandbox != nil {
		session.Sandbox = *row.Sandbox
	}
	if row.SandboxState != nil {
		session.SandboxState = *row.SandboxState
	}
	if row.EndReason != nil {
		session.EndReason = *row.EndReason
	}
	return session
}

// keyAt returns the secret store columns of the provider's key.
func keyAt(provider store.Provider) (storeID *key.ID, path, name string) {
	if at := provider.APIKeyAt; at != nil {
		return (*key.ID)(&at.Store), at.Path, at.Key
	}
	return nil, "", ""
}

// headersIn returns the headers of an endpoint as their column holds them:
// null if there are none.
func headersIn(headers []store.Header) (*json.RawMessage, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		return nil, err
	}
	column := json.RawMessage(encoded)
	return &column, nil
}

// headersFrom returns the headers that the column of an endpoint holds.
func headersFrom(column *json.RawMessage) ([]store.Header, error) {
	if column == nil {
		return nil, nil
	}
	var headers []store.Header
	if err := json.Unmarshal(*column, &headers); err != nil {
		return nil, fmt.Errorf("headers of an endpoint: %w", err)
	}
	return headers, nil
}

func (s *records) CreateProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	headers, err := headersIn(provider.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	params := db.CreateProviderParams{
		ID: key.ID(provider.ID), ProjectID: key.ID(provider.ProjectID), Name: provider.Name, Url: provider.URL,
		Headers: headers, ApiKey: provider.APIKey,
	}
	params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = keyAt(provider)
	if err := created(s.queries.CreateProvider(ctx, params)); err != nil {
		return store.Provider{}, err
	}
	return s.Provider(ctx, provider.ID)
}

func (s *records) Provider(ctx context.Context, id uuid.UUID) (store.Provider, error) {
	row, err := s.queries.GetProvider(ctx, key.ID(id))
	if err != nil {
		return store.Provider{}, found(err)
	}
	return providerFrom(row)
}

func (s *records) Providers(ctx context.Context, projectID uuid.UUID) ([]store.Provider, error) {
	rows, err := s.queries.ListProviders(ctx, key.ID(projectID))
	if err != nil {
		return nil, err
	}
	providers := make([]store.Provider, len(rows))
	for i, row := range rows {
		if providers[i], err = providerFrom(row); err != nil {
			return nil, err
		}
	}
	return providers, nil
}

func providerFrom(row db.Provider) (store.Provider, error) {
	headers, err := headersFrom(row.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	provider := store.Provider{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Name: row.Name, URL: row.Url, Headers: headers, APIKey: row.ApiKey,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ApiKeyStoreID != nil {
		provider.APIKeyAt = &store.SecretReference{Store: uuid.UUID(*row.ApiKeyStoreID), Path: row.ApiKeyPath, Key: row.ApiKeyName}
	}
	return provider, nil
}

func (s *records) ReplaceProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	headers, err := headersIn(provider.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	params := db.ReplaceProviderParams{
		ID: key.ID(provider.ID), Name: provider.Name, Url: provider.URL, Headers: headers, ApiKey: provider.APIKey,
	}
	params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = keyAt(provider)
	if err := created(s.queries.ReplaceProvider(ctx, params)); err != nil {
		return store.Provider{}, err
	}
	return s.Provider(ctx, provider.ID)
}

// toolsIn returns the tools of an MCP server as their column holds them:
// null if the server has not listed them.
func toolsIn(tools []store.Tool, listed *time.Time) (*json.RawMessage, error) {
	if listed == nil {
		return nil, nil
	}
	if tools == nil {
		tools = []store.Tool{}
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	column := json.RawMessage(encoded)
	return &column, nil
}

// toolsFrom returns the tools that the column of an MCP server holds.
func toolsFrom(column *json.RawMessage) ([]store.Tool, error) {
	if column == nil {
		return nil, nil
	}
	var tools []store.Tool
	if err := json.Unmarshal(*column, &tools); err != nil {
		return nil, fmt.Errorf("tools of an MCP server: %w", err)
	}
	return tools, nil
}

func (s *records) CreateMcpServer(ctx context.Context, server store.McpServer) (store.McpServer, error) {
	headers, err := headersIn(server.Headers)
	if err != nil {
		return store.McpServer{}, err
	}
	tools, err := toolsIn(server.Tools, server.ToolsListedAt)
	if err != nil {
		return store.McpServer{}, err
	}
	params := db.CreateMcpServerParams{
		ID: key.ID(server.ID), ProjectID: key.ID(server.ProjectID), Slug: server.Slug, Name: server.Name, Url: server.URL,
		Transport: server.Transport, Headers: headers, Tools: tools, ToolsListedAt: server.ToolsListedAt,
	}
	switch err := created(s.queries.CreateMcpServer(ctx, params)); {
	case duplicate(err, mcpServerSlug):
		return store.McpServer{}, store.ErrSlugTaken
	case err != nil:
		return store.McpServer{}, err
	}
	return s.McpServer(ctx, server.ID)
}

func (s *records) McpServer(ctx context.Context, id uuid.UUID) (store.McpServer, error) {
	row, err := s.queries.GetMcpServer(ctx, key.ID(id))
	if err != nil {
		return store.McpServer{}, found(err)
	}
	return mcpServerFrom(row)
}

func (s *records) McpServers(ctx context.Context, projectID uuid.UUID) ([]store.McpServer, error) {
	rows, err := s.queries.ListMcpServers(ctx, key.ID(projectID))
	if err != nil {
		return nil, err
	}
	servers := make([]store.McpServer, len(rows))
	for i, row := range rows {
		if servers[i], err = mcpServerFrom(row); err != nil {
			return nil, err
		}
	}
	return servers, nil
}

func mcpServerFrom(row db.McpServer) (store.McpServer, error) {
	headers, err := headersFrom(row.Headers)
	if err != nil {
		return store.McpServer{}, err
	}
	tools, err := toolsFrom(row.Tools)
	if err != nil {
		return store.McpServer{}, err
	}
	return store.McpServer{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Slug: row.Slug, Name: row.Name, URL: row.Url, Transport: row.Transport,
		Headers: headers, Tools: tools, ToolsListedAt: row.ToolsListedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (s *records) ReplaceMcpServer(ctx context.Context, server store.McpServer) (store.McpServer, error) {
	headers, err := headersIn(server.Headers)
	if err != nil {
		return store.McpServer{}, err
	}
	tools, err := toolsIn(server.Tools, server.ToolsListedAt)
	if err != nil {
		return store.McpServer{}, err
	}
	params := db.ReplaceMcpServerParams{
		ID: key.ID(server.ID), Name: server.Name, Url: server.URL, Transport: server.Transport, Headers: headers, Tools: tools,
		ToolsListedAt: server.ToolsListedAt,
	}
	if err := created(s.queries.ReplaceMcpServer(ctx, params)); err != nil {
		return store.McpServer{}, err
	}
	return s.McpServer(ctx, server.ID)
}

func (s *records) SetMcpServerTools(ctx context.Context, id uuid.UUID, tools []store.Tool, listedAt time.Time) error {
	column, err := toolsIn(tools, &listedAt)
	if err != nil {
		return err
	}
	return created(s.queries.SetMcpServerTools(ctx, db.SetMcpServerToolsParams{ID: key.ID(id), Tools: column, ToolsListedAt: &listedAt}))
}

func (s *records) DeleteMcpServer(ctx context.Context, id uuid.UUID) error {
	return created(s.queries.DeleteMcpServer(ctx, key.ID(id)))
}

func (s *records) CreateSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	err := created(s.queries.CreateSecretStore(ctx, db.CreateSecretStoreParams{
		ID: key.ID(secrets.ID), ProjectID: key.ID(secrets.ProjectID), Name: secrets.Name, Kind: secrets.Kind,
		Settings: secrets.Settings, Credential: secrets.Credential,
	}))
	if err != nil {
		return store.SecretStore{}, err
	}
	return s.SecretStore(ctx, secrets.ID)
}

func (s *records) SecretStore(ctx context.Context, id uuid.UUID) (store.SecretStore, error) {
	row, err := s.queries.GetSecretStore(ctx, key.ID(id))
	return secretStoreFrom(row), found(err)
}

func (s *records) SecretStores(ctx context.Context, projectID uuid.UUID) ([]store.SecretStore, error) {
	rows, err := s.queries.ListSecretStores(ctx, key.ID(projectID))
	if err != nil {
		return nil, err
	}
	stores := make([]store.SecretStore, len(rows))
	for i, row := range rows {
		stores[i] = secretStoreFrom(row)
	}
	return stores, nil
}

func secretStoreFrom(row db.SecretStore) store.SecretStore {
	return store.SecretStore{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Name: row.Name, Kind: row.Kind,
		Settings: row.Settings, Credential: row.Credential, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *records) ReplaceSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	err := created(s.queries.ReplaceSecretStore(ctx, db.ReplaceSecretStoreParams{
		ID: key.ID(secrets.ID), Name: secrets.Name, Kind: secrets.Kind, Settings: secrets.Settings, Credential: secrets.Credential,
	}))
	if err != nil {
		return store.SecretStore{}, err
	}
	return s.SecretStore(ctx, secrets.ID)
}

func (s *records) CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, maxWaiting, maxRunning int) (store.Turn, int, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Turn{}, 0, err
	}
	var turn store.Turn
	var waiting int64
	err = s.within(ctx, func(queries *db.Queries) error {
		if maxWaiting > 0 {
			// Accept messages one at a time across Supervisors, so that the
			// queue limit holds.
			if _, err := queries.Lock(ctx, queueLock); err != nil {
				return err
			}
		}
		// Lock the session, which serializes the messages to it.
		session, err := queries.LockSession(ctx, key.ID(sessionID))
		if err != nil {
			return found(err)
		}
		if session.EndedAt != nil {
			return store.ErrSessionEnded
		}
		seq, err := queries.NextSeq(ctx, key.ID(sessionID))
		if err != nil {
			return err
		}
		params := db.CreateTurnParams{ID: key.ID(id), SessionID: key.ID(sessionID), Seq: int32(seq), Text: text}
		if by != uuid.Nil {
			params.CreatedBy = (*key.ID)(&by)
		}
		err = queries.CreateTurn(ctx, params)
		if failed(err, duplicateEntry) {
			return store.ErrSessionBusy
		}
		if err != nil {
			return err
		}
		queued, err := queries.CountQueuedTurns(ctx)
		if err != nil {
			return err
		}
		running, err := queries.CountRunningTurns(ctx)
		if err != nil {
			return err
		}
		// Queued turns that have room to run do not wait.
		waiting = queued - max(0, int64(maxRunning)-running)
		if maxWaiting > 0 && waiting > int64(maxWaiting) {
			return store.ErrQueueFull
		}
		row, err := queries.GetTurn(ctx, key.ID(id))
		turn = turnFrom(row)
		return err
	})
	if err != nil {
		return store.Turn{}, 0, err
	}
	return turn, int(max(waiting, 0)), nil
}

func (s *records) AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error {
	return s.within(ctx, func(queries *db.Queries) error {
		if _, err := queries.Lock(ctx, queueLock); err != nil {
			return err
		}
		queued, err := queries.QueuedTurns(ctx)
		if err != nil {
			return err
		}
		return announce(ids(queued))
	})
}

func ids(rows []key.ID) []uuid.UUID {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = uuid.UUID(row)
	}
	return ids
}

func (s *records) Turn(ctx context.Context, id uuid.UUID) (store.Turn, error) {
	row, err := s.queries.GetTurn(ctx, key.ID(id))
	return turnFrom(row), found(err)
}

func (s *records) Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]store.Turn, error) {
	rows, err := s.queries.ListTurns(ctx, db.ListTurnsParams{SessionID: key.ID(sessionID), Before: int32(before), Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	turns := make([]store.Turn, len(rows))
	for i, row := range rows {
		turns[i] = turnFrom(row)
	}
	return turns, nil
}

func (s *records) LastTurns(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID]store.Turn, error) {
	of := make([]key.ID, len(sessionIDs))
	for i, id := range sessionIDs {
		of[i] = key.ID(id)
	}
	rows, err := s.queries.ListLastTurns(ctx, of)
	if err != nil {
		return nil, err
	}
	last := make(map[uuid.UUID]store.Turn, len(rows))
	for _, row := range rows {
		last[uuid.UUID(row.SessionID)] = turnFrom(row)
	}
	return last, nil
}

func (s *records) TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error) {
	of := make([]key.ID, len(turnIDs))
	for i, id := range turnIDs {
		of[i] = key.ID(id)
	}
	rows, err := s.queries.ListTurnItems(ctx, of)
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID][]json.RawMessage{}
	for _, row := range rows {
		items[uuid.UUID(row.TurnID)] = append(items[uuid.UUID(row.TurnID)], row.Item)
	}
	return items, nil
}

func (s *records) DispatchTurns(ctx context.Context, limit int) ([]store.Dispatch, error) {
	var dispatches []store.Dispatch
	err := s.within(ctx, func(queries *db.Queries) error {
		dispatches = []store.Dispatch{}
		// Dispatch on one Supervisor at a time, so that together they keep to
		// the limit.
		if _, err := queries.Lock(ctx, dispatchLock); err != nil {
			return err
		}
		running, err := queries.CountRunningTurns(ctx)
		if err != nil {
			return err
		}
		room := int64(limit) - running
		if room <= 0 {
			return nil
		}
		rows, err := queries.NextTurns(ctx, int32(room))
		if err != nil {
			return err
		}
		for _, row := range rows {
			taken, err := queries.DispatchTurn(ctx, row.TurnID)
			if err != nil {
				return err
			}
			if taken == 1 {
				dispatches = append(dispatches, store.Dispatch{TurnID: uuid.UUID(row.TurnID), Harness: row.Harness, Waited: since(row.Waited)})
			}
		}
		return nil
	})
	return dispatches, err
}

func (s *records) CancelTurn(ctx context.Context, id uuid.UUID) (store.Turn, bool, error) {
	var turn store.Turn
	var unended bool
	err := s.within(ctx, func(queries *db.Queries) error {
		row, err := queries.LockTurn(ctx, key.ID(id))
		if err != nil {
			return found(err)
		}
		if unended = row.EndedAt == nil; unended {
			cancel := queries.AskTurnToStop
			if row.State == store.TurnQueued {
				cancel = queries.CancelQueuedTurn
			}
			if err := cancel(ctx, key.ID(id)); err != nil {
				return err
			}
			if row, err = queries.GetTurn(ctx, key.ID(id)); err != nil {
				return err
			}
		}
		turn = turnFrom(row)
		return nil
	})
	return turn, unended && err == nil, err
}

func (s *records) ExpireTurns(ctx context.Context, wait time.Duration) ([]store.Turn, error) {
	var turns []store.Turn
	err := s.within(ctx, func(queries *db.Queries) error {
		turns = []store.Turn{}
		waited, err := queries.LockTurnsToExpire(ctx, wait.Microseconds())
		if err != nil {
			return err
		}
		for _, id := range waited {
			if err := queries.ExpireTurn(ctx, id); err != nil {
				return err
			}
			row, err := queries.GetTurn(ctx, id)
			if err != nil {
				return err
			}
			turns = append(turns, turnFrom(row))
		}
		return nil
	})
	return turns, err
}

func (s *records) DispatchedTurns(ctx context.Context) ([]store.DispatchedTurn, error) {
	rows, err := s.queries.DispatchedTurns(ctx)
	if err != nil {
		return nil, err
	}
	turns := make([]store.DispatchedTurn, len(rows))
	for i, row := range rows {
		turns[i] = store.DispatchedTurn{ID: uuid.UUID(row.ID), Cancelling: since(row.Cancelling)}
	}
	return turns, nil
}

func (s *records) UnfinishedTurns(ctx context.Context) ([]store.UnfinishedTurn, error) {
	rows, err := s.queries.ListUnfinishedTurns(ctx)
	if err != nil {
		return nil, err
	}
	turns := make([]store.UnfinishedTurn, len(rows))
	for i, row := range rows {
		turns[i] = store.UnfinishedTurn{
			Harness: row.Harness, State: row.State, Started: row.Started, Age: since(row.Age), Asking: int(row.Asking),
		}
	}
	return turns, nil
}

func (s *records) FollowTurn(ctx context.Context, turnID uuid.UUID) (store.FollowedTurn, error) {
	if err := s.queries.FollowTurn(ctx, key.ID(turnID)); err != nil {
		return store.FollowedTurn{}, err
	}
	row, err := s.queries.FollowedTurn(ctx, key.ID(turnID))
	if err != nil {
		return store.FollowedTurn{}, found(err)
	}
	turn := turnFrom(row.Turn)
	switch turn.State {
	case store.TurnRunning:
	case store.TurnQueued:
		return store.FollowedTurn{}, store.ErrNotFound
	default:
		return store.FollowedTurn{}, store.ErrTurnEnded
	}
	followed := store.FollowedTurn{
		Turn:         turn,
		AgentID:      uuid.UUID(row.AgentID),
		ProjectID:    uuid.UUID(row.ProjectID),
		Harness:      row.Harness,
		Instructions: row.Instructions,
		Approval:     row.Approval,
		ProviderURL:  row.ProviderUrl,
		Model:        row.Model,
		Dispatched:   since(row.Dispatched),
	}
	if row.Sandbox != nil {
		followed.Sandbox = *row.Sandbox
	}
	return followed, nil
}

func (s *records) SessionTurn(ctx context.Context, sessionID uuid.UUID) (store.Origin, error) {
	row, err := s.queries.SessionTurn(ctx, key.ID(sessionID))
	if err != nil {
		return store.Origin{}, found(err)
	}
	return originOf(row)
}

func originOf(row db.SessionTurnRow) (store.Origin, error) {
	headers, err := headersFrom(row.ProviderHeaders)
	if err != nil {
		return store.Origin{}, err
	}
	origin := store.Origin{
		TurnID: uuid.UUID(row.TurnID), SessionID: uuid.UUID(row.SessionID), AgentID: uuid.UUID(row.AgentID), ProjectID: uuid.UUID(row.ProjectID),
		Subject: row.Subject,
		Provider: store.Provider{
			ID: uuid.UUID(row.ProviderID), ProjectID: uuid.UUID(row.ProjectID), URL: row.ProviderUrl, Headers: headers,
			APIKey: row.ProviderApiKey,
		},
	}
	if row.CreatedBy != nil {
		origin.UserID = uuid.UUID(*row.CreatedBy)
	}
	if row.ApiKeyStoreID != nil && row.StoreCreatedAt != nil && row.StoreUpdatedAt != nil {
		in := uuid.UUID(*row.ApiKeyStoreID)
		origin.Provider.APIKeyAt = &store.SecretReference{Store: in, Path: row.ApiKeyPath, Key: row.ApiKeyName}
		origin.SecretStore = &store.SecretStore{
			ID: in, ProjectID: origin.ProjectID, Name: row.StoreName, Kind: row.StoreKind,
			Settings: document(row.StoreSettings), Credential: row.StoreCredential,
			CreatedAt: *row.StoreCreatedAt, UpdatedAt: *row.StoreUpdatedAt,
		}
	}
	return origin, nil
}

func (s *records) StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error) {
	var startup time.Duration
	var first bool
	err := s.within(ctx, func(queries *db.Queries) error {
		row, err := queries.LockTurn(ctx, key.ID(turnID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && row.StartedAt != nil) {
			first = false
			return nil
		}
		if err != nil {
			return err
		}
		if err := queries.StartTurn(ctx, row.ID); err != nil {
			return err
		}
		took, err := queries.Startup(ctx, row.ID)
		startup, first = since(took), true
		return err
	})
	if err != nil {
		return 0, false, err
	}
	return startup, first, nil
}

func (s *records) TurnHarness(ctx context.Context, turnID uuid.UUID) (string, error) {
	harness, err := s.queries.GetTurnHarness(ctx, key.ID(turnID))
	return harness, found(err)
}

func (s *records) SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *store.Usage) error {
	finished := db.SetFinishedParams{ID: key.ID(turnID), StopReason: &stopReason}
	if usage != nil {
		finished.TotalTokens = &usage.TotalTokens
		finished.InputTokens = &usage.InputTokens
		finished.OutputTokens = &usage.OutputTokens
		finished.ThoughtTokens = usage.ThoughtTokens
		finished.CachedReadTokens = usage.CachedReadTokens
		finished.CachedWriteTokens = usage.CachedWriteTokens
	}
	return s.queries.SetFinished(ctx, finished)
}

func (s *records) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure,
	items func(context.Context) ([]json.RawMessage, error)) (store.Turn, bool, error) {
	var turn store.Turn
	var ended bool
	err := s.within(ctx, func(queries *db.Queries) error {
		ended = false
		// Of concurrent calls, the first to lock the turn ends it, and the
		// others then find it ended.
		row, err := queries.LockTurn(ctx, key.ID(turnID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && row.EndedAt != nil) {
			return nil
		}
		if err != nil {
			return err
		}
		said, err := items(ctx)
		if err != nil {
			return fmt.Errorf("read turn items: %w", err)
		}
		end := db.EndTurnParams{ID: row.ID, State: store.TurnFailed}
		finished := succeeded && row.StopReason != nil
		switch {
		case row.CancelRequestedAt != nil && !(finished && *row.StopReason != "cancelled"):
			end.State = store.TurnCancelled
		case finished:
			end.State = store.TurnCompleted
		default:
			end.FailureReason, end.FailureMessage = &failure.Reason, failure.Message
		}
		if err := queries.EndTurn(ctx, end); err != nil {
			return err
		}
		for i, item := range said {
			if err := queries.AddTurnItem(ctx, db.AddTurnItemParams{TurnID: row.ID, Seq: int32(i + 1), Item: item}); err != nil {
				return err
			}
		}
		if row, err = queries.GetTurn(ctx, row.ID); err != nil {
			return err
		}
		turn, ended = turnFrom(row), true
		return nil
	})
	if err != nil {
		return store.Turn{}, false, err
	}
	return turn, ended, nil
}

func (s *records) AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error {
	return s.queries.AddApproval(ctx, db.AddApprovalParams{TurnID: key.ID(turnID), ID: id, Request: request})
}

func (s *records) ResolveApproval(ctx context.Context, turnID uuid.UUID, id, outcome string) (store.Approval, time.Duration, bool, error) {
	of := db.GetApprovalParams{TurnID: key.ID(turnID), ID: id}
	resolved, err := s.queries.ResolveApproval(ctx, db.ResolveApprovalParams{TurnID: of.TurnID, ID: id, Outcome: &outcome})
	if err != nil || resolved == 0 {
		return store.Approval{}, 0, false, err
	}
	row, err := s.queries.GetApproval(ctx, of)
	if err != nil {
		return store.Approval{}, 0, false, err
	}
	return store.Approval{Request: row.Request, Resolved: true}, row.ResolvedAt.Sub(row.RequestedAt), true, nil
}

func (s *records) Approval(ctx context.Context, turnID uuid.UUID, id string) (store.Approval, error) {
	row, err := s.queries.GetApproval(ctx, db.GetApprovalParams{TurnID: key.ID(turnID), ID: id})
	approval := store.Approval{Request: row.Request, Answered: row.AnsweredAt != nil, Resolved: row.ResolvedAt != nil}
	if row.AnsweredBy != nil {
		approval.AnsweredBy = uuid.UUID(*row.AnsweredBy)
	}
	return approval, found(err)
}

func (s *records) AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error) {
	params := db.AnswerApprovalParams{TurnID: key.ID(turnID), ID: id, OptionID: &optionID}
	if by != uuid.Nil {
		params.AnsweredBy = (*key.ID)(&by)
	}
	answered, err := s.queries.AnswerApproval(ctx, params)
	return answered == 1, err
}

func (s *records) WaitingApprovals(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error) {
	rows, err := s.queries.WaitingApprovals(ctx, key.ID(turnID))
	if err != nil {
		return nil, err
	}
	return append([]json.RawMessage{}, rows...), nil
}

func (s *records) Answers(ctx context.Context, turnID *uuid.UUID) ([]store.Answer, error) {
	rows, err := s.queries.Answers(ctx, db.AnswersParams{TurnID: (*key.ID)(turnID)})
	if err != nil {
		return nil, err
	}
	answers := make([]store.Answer, len(rows))
	for i, row := range rows {
		answers[i] = store.Answer{TurnID: uuid.UUID(row.TurnID), ID: row.ID, OptionID: row.OptionID}
	}
	return answers, nil
}

func turnFrom(row db.Turn) store.Turn {
	turn := store.Turn{
		ID:                uuid.UUID(row.ID),
		SessionID:         uuid.UUID(row.SessionID),
		Seq:               int(row.Seq),
		State:             row.State,
		Text:              row.Text,
		CreatedAt:         row.CreatedAt,
		StartedAt:         row.StartedAt,
		StopReason:        row.StopReason,
		EndedAt:           row.EndedAt,
		CancelRequestedAt: row.CancelRequestedAt,
	}
	if row.FailureReason != nil {
		turn.Failure = &store.Failure{Reason: *row.FailureReason}
		if row.FailureMessage != nil {
			turn.Failure.Message = *row.FailureMessage
		}
	}
	if row.CreatedBy != nil {
		turn.CreatedBy = uuid.UUID(*row.CreatedBy)
	}
	if row.TotalTokens != nil && row.InputTokens != nil && row.OutputTokens != nil {
		turn.Usage = &store.Usage{
			TotalTokens:       *row.TotalTokens,
			InputTokens:       *row.InputTokens,
			OutputTokens:      *row.OutputTokens,
			ThoughtTokens:     row.ThoughtTokens,
			CachedReadTokens:  row.CachedReadTokens,
			CachedWriteTokens: row.CachedWriteTokens,
		}
	}
	return turn
}
