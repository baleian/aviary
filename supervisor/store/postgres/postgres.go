// Package postgres implements store.Store on PostgreSQL. The queries are in
// queries.sql, from which `make sqlc` generates internal/db.
package postgres

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aviary/supervisor/store"
	"aviary/supervisor/store/postgres/internal/db"
)

// uniqueViolation is the PostgreSQL error code for a unique constraint
// violation.
const uniqueViolation = "23505"

// The unique constraints on slugs, as the schema names them.
const (
	projectSlug   = "projects_slug"
	agentSlug     = "agents_project_slug"
	mcpServerSlug = "mcp_servers_project_slug"
)

// dispatchLock is the key of the advisory lock that serializes dispatching.
const dispatchLock int64 = 0x617669617279 // "aviary"

// queueLock is the key of the advisory lock that serializes counting the
// queue and announcing it.
const queueLock int64 = dispatchLock + 1

type records struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// Plugin is the store.Plugin for PostgreSQL, whose URLs begin with
// postgres:// or postgresql://.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"postgres", "postgresql"} }

// Open connects to the database at address and checks that its schema suits
// this build.
func (Plugin) Open(ctx context.Context, address string) (store.Store, error) {
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
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

func (s *records) CreateProject(ctx context.Context, project store.Project) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	row, err := s.queries.CreateProject(ctx, db.CreateProjectParams{ID: id, Slug: project.Slug, Name: project.Name})
	if violates(err, projectSlug) {
		return store.Project{}, store.ErrSlugTaken
	}
	return store.Project(row), err
}

// violates reports whether err is a violation of the unique constraint.
func violates(err error, constraint string) bool {
	var refused *pgconn.PgError
	return errors.As(err, &refused) && refused.Code == uniqueViolation && refused.ConstraintName == constraint
}

func (s *records) Project(ctx context.Context, id uuid.UUID) (store.Project, error) {
	row, err := s.queries.GetProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Project{}, store.ErrNotFound
	}
	return store.Project(row), err
}

func (s *records) Projects(ctx context.Context) ([]store.Project, error) {
	rows, err := s.queries.ListProjects(ctx)
	return projectsFrom(rows), err
}

func (s *records) ProjectsOf(ctx context.Context, userID uuid.UUID, groups []string) ([]store.Project, error) {
	// An empty array, not NULL, for no groups.
	params := db.ListProjectsOfMemberParams{Groups: append([]string{}, groups...)}
	if userID != uuid.Nil {
		params.UserID = &userID
	}
	rows, err := s.queries.ListProjectsOfMember(ctx, params)
	return projectsFrom(rows), err
}

func projectsFrom(rows []db.Project) []store.Project {
	projects := make([]store.Project, len(rows))
	for i, row := range rows {
		projects[i] = store.Project(row)
	}
	return projects
}

func (s *records) Roles(ctx context.Context) ([]store.Role, error) {
	rows, err := s.queries.ListRoles(ctx)
	roles := make([]store.Role, len(rows))
	for i, row := range rows {
		roles[i] = roleFrom(row)
	}
	return roles, err
}

func (s *records) SetRole(ctx context.Context, name string, permissions []string, by uuid.UUID) (store.Role, error) {
	// An empty array, not NULL: a role may have no permissions.
	params := db.SetRoleParams{Name: name, Permissions: append([]string{}, permissions...)}
	if by != uuid.Nil {
		params.UpdatedBy = &by
	}
	row, err := s.queries.SetRole(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Role{}, store.ErrNotFound
	}
	return roleFrom(row), err
}

func roleFrom(row db.Role) store.Role {
	role := store.Role{Name: row.Name, Permissions: row.Permissions, UpdatedAt: row.UpdatedAt}
	if row.UpdatedBy != nil {
		role.UpdatedBy = *row.UpdatedBy
	}
	return role
}

func (s *records) UserOf(ctx context.Context, subject, username, displayName string) (store.User, error) {
	// Most requests come from a user whose details have not changed.
	row, err := s.queries.GetUserBySubject(ctx, subject)
	if err == nil && row.DisplayName == displayName && (username == "" || (row.Username != nil && *row.Username == username)) {
		return userFrom(row), nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.User{}, err
	}
	seen := db.SeeUserParams{ID: id, Subject: subject, DisplayName: displayName}
	if username != "" {
		seen.Username = &username
	}
	// Another Supervisor that sees the user for the first time concurrently
	// can make the insert fail on the unique username. A retry finds the user.
	for attempt := 1; ; attempt++ {
		if username != "" {
			if err := s.queries.FreeUsername(ctx, db.FreeUsernameParams{Username: username, Subject: subject}); err != nil {
				return store.User{}, err
			}
		}
		row, err = s.queries.SeeUser(ctx, seen)
		var refused *pgconn.PgError
		if attempt < 5 && errors.As(err, &refused) && refused.Code == uniqueViolation {
			continue
		}
		if err == nil && username != "" {
			err = s.claim(ctx, username)
		}
		return userFrom(row), err
	}
}

// claim gives the members awaited under username to the user who has it.
func (s *records) claim(ctx context.Context, username string) error {
	if err := s.queries.DropMembersBeforeClaim(ctx, username); err != nil {
		return err
	}
	return s.queries.ClaimMembers(ctx, username)
}

func (s *records) AddMember(ctx context.Context, member store.Member, by uuid.UUID) (store.Member, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Member{}, err
	}
	params := db.AddMemberParams{ID: id, ProjectID: member.ProjectID, Permissions: member.Permissions}
	if member.Role != "" {
		params.Role, params.Permissions = &member.Role, nil
	}
	if by != uuid.Nil {
		params.UpdatedBy = &by
	}
	var username string
	if member.User != nil {
		username = member.User.Username
		params.Username = &username
		if is, err := s.queries.IsMember(ctx, db.IsMemberParams{ProjectID: member.ProjectID, Username: username}); err != nil || is {
			return store.Member{}, cmp.Or(err, store.ErrMemberExists)
		}
	} else {
		params.GroupName = &member.Group
	}
	var refused *pgconn.PgError
	switch _, err := s.queries.AddMember(ctx, params); {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Member{}, store.ErrNotFound
	case errors.As(err, &refused) && refused.Code == uniqueViolation:
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

func (s *records) SetMember(ctx context.Context, id uuid.UUID, role string, permissions []string, by uuid.UUID) (store.Member, error) {
	params := db.SetMemberParams{ID: id, Permissions: permissions}
	if role != "" {
		params.Role, params.Permissions = &role, nil
	}
	if by != uuid.Nil {
		params.UpdatedBy = &by
	}
	if changed, err := s.queries.SetMember(ctx, params); err != nil || changed == 0 {
		return store.Member{}, cmp.Or(err, store.ErrNotFound)
	}
	return s.member(ctx, id)
}

func (s *records) RemoveMember(ctx context.Context, id uuid.UUID) error {
	removed, err := s.queries.RemoveMember(ctx, id)
	if err == nil && removed == 0 {
		return store.ErrNotFound
	}
	return err
}

func (s *records) Members(ctx context.Context, projectID uuid.UUID) ([]store.Member, error) {
	rows, err := s.queries.ListMembers(ctx, projectID)
	members := make([]store.Member, len(rows))
	for i, row := range rows {
		members[i] = memberFrom(db.GetMemberRow(row))
	}
	return members, err
}

func (s *records) Permissions(ctx context.Context, projectID, userID uuid.UUID, groups []string) ([]string, error) {
	// An empty array, not NULL, for no groups.
	params := db.ListPermissionsParams{ProjectID: projectID, Groups: append([]string{}, groups...)}
	if userID != uuid.Nil {
		params.UserID = &userID
	}
	sets, err := s.queries.ListPermissions(ctx, params)
	held := slices.Concat(sets...)
	slices.Sort(held)
	return slices.Compact(held), err
}

func (s *records) member(ctx context.Context, id uuid.UUID) (store.Member, error) {
	row, err := s.queries.GetMember(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Member{}, store.ErrNotFound
	}
	return memberFrom(row), err
}

func memberFrom(row db.GetMemberRow) store.Member {
	member := store.Member{ID: row.ID, ProjectID: row.ProjectID, Permissions: row.Permissions, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.GroupName != nil {
		member.Group = *row.GroupName
	}
	if row.Role != nil {
		member.Role = *row.Role
	}
	if row.UpdatedBy != nil {
		member.UpdatedBy = *row.UpdatedBy
	}
	switch {
	case row.UserID != nil:
		member.User = &store.User{ID: *row.UserID, Subject: *row.Subject, DisplayName: *row.DisplayName, CreatedAt: *row.UserCreatedAt}
		if row.UserUsername != nil {
			member.User.Username = *row.UserUsername
		}
	case row.Username != nil:
		member.User = &store.User{Username: *row.Username}
	}
	return member
}

func userFrom(row db.User) store.User {
	user := store.User{ID: row.ID, Subject: row.Subject, DisplayName: row.DisplayName, CreatedAt: row.CreatedAt}
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
		ID: id, ProjectID: projectID, Slug: agent.Slug, Name: agent.Name, Description: agent.Description, Harness: agent.Harness,
		Instructions: agent.Instructions, Approval: agent.Approval, ProviderID: agent.Model.Provider, Model: agent.Model.Name,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Agent{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.CreateAgent(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Agent{}, store.ErrNotFound
	case violates(err, agentSlug):
		return store.Agent{}, store.ErrSlugTaken
	case err != nil:
		return store.Agent{}, err
	}
	if err := setAgentServers(ctx, queries, id, agent.McpServers); err != nil {
		return store.Agent{}, err
	}
	created, err := withServers(ctx, queries, agentFrom(row))
	if err != nil {
		return store.Agent{}, err
	}
	return created, tx.Commit(ctx)
}

func (s *records) Agent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	row, err := s.queries.GetAgent(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Agent{}, store.ErrNotFound
	case err != nil:
		return store.Agent{}, err
	}
	return withServers(ctx, s.queries, agentFrom(row))
}

func (s *records) ReplaceAgent(ctx context.Context, agent store.Agent) (store.Agent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Agent{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.ReplaceAgent(ctx, db.ReplaceAgentParams{
		ID: agent.ID, Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions,
		Approval: agent.Approval, ProviderID: agent.Model.Provider, Model: agent.Model.Name,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Agent{}, s.deleted(ctx, agent.ID)
	case err != nil:
		return store.Agent{}, err
	}
	if err := setAgentServers(ctx, queries, agent.ID, agent.McpServers); err != nil {
		return store.Agent{}, err
	}
	replaced, err := withServers(ctx, queries, agentFrom(row))
	if err != nil {
		return store.Agent{}, err
	}
	return replaced, tx.Commit(ctx)
}

// setAgentServers replaces the MCP servers of the agent.
func setAgentServers(ctx context.Context, queries *db.Queries, agentID uuid.UUID, servers []uuid.UUID) error {
	if err := queries.ClearAgentMcpServers(ctx, agentID); err != nil {
		return err
	}
	for _, server := range servers {
		if err := queries.AddAgentMcpServer(ctx, db.AddAgentMcpServerParams{AgentID: agentID, McpServerID: server}); err != nil {
			return err
		}
	}
	return nil
}

// agentServers returns the MCP servers of the agents, by agent.
func agentServers(ctx context.Context, queries *db.Queries, agentIDs ...uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := queries.ListAgentMcpServers(ctx, agentIDs)
	if err != nil {
		return nil, err
	}
	servers := map[uuid.UUID][]uuid.UUID{}
	for _, row := range rows {
		servers[row.AgentID] = append(servers[row.AgentID], row.McpServerID)
	}
	return servers, nil
}

// withServers returns the agent with its MCP servers.
func withServers(ctx context.Context, queries *db.Queries, agent store.Agent) (store.Agent, error) {
	servers, err := agentServers(ctx, queries, agent.ID)
	agent.McpServers = servers[agent.ID]
	return agent, err
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Agent{}, nil, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// Lock the agent, then its sessions, before checking their turns: a
	// concurrent session or message either is there by then, or finds the
	// agent deleted and the session ended.
	row, err := queries.LockAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Agent{}, nil, store.ErrNotFound
	}
	if err != nil {
		return store.Agent{}, nil, err
	}
	if row.DeletedAt != nil {
		agent, err := withServers(ctx, queries, agentFrom(row))
		return agent, nil, err
	}
	if _, err := queries.LockAgentSessions(ctx, id); err != nil {
		return store.Agent{}, nil, err
	}
	busy, err := queries.IsAgentBusy(ctx, id)
	if err != nil {
		return store.Agent{}, nil, err
	}
	if busy {
		return store.Agent{}, nil, store.ErrSessionBusy
	}
	rows, err := queries.EndAgentSessions(ctx, id)
	if err != nil {
		return store.Agent{}, nil, err
	}
	if row, err = queries.DeleteAgent(ctx, id); err != nil {
		return store.Agent{}, nil, err
	}
	ended := make([]store.Session, len(rows))
	for i, row := range rows {
		ended[i] = sessionFrom(row)
	}
	agent, err := withServers(ctx, queries, agentFrom(row))
	if err != nil {
		return store.Agent{}, nil, err
	}
	return agent, ended, tx.Commit(ctx)
}

func (s *records) Agents(ctx context.Context, projectID uuid.UUID) ([]store.Agent, error) {
	rows, err := s.queries.ListAgents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	agents := make([]store.Agent, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		agents[i], ids[i] = agentFrom(row), row.ID
	}
	servers, err := agentServers(ctx, s.queries, ids...)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		agents[i].McpServers = servers[agents[i].ID]
	}
	return agents, nil
}

func (s *records) CreateSession(ctx context.Context, agentID, by uuid.UUID, title string, mcp map[uuid.UUID]bool) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	params := db.CreateSessionParams{ID: id, AgentID: agentID, Title: title}
	if by != uuid.Nil {
		params.CreatedBy = &by
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Session{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.CreateSession(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Session{}, s.deleted(ctx, agentID)
	case err != nil:
		return store.Session{}, err
	}
	for server, enabled := range mcp {
		if err := queries.AddSessionMcpServer(ctx, db.AddSessionMcpServerParams{SessionID: id, McpServerID: server, Enabled: enabled}); err != nil {
			return store.Session{}, err
		}
	}
	return sessionFrom(row), tx.Commit(ctx)
}

func (s *records) SetSessionMcpServer(ctx context.Context, sessionID, serverID uuid.UUID, enabled bool) (store.Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Session{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	session, err := openSession(ctx, queries, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	agents, err := queries.IsAgentMcpServer(ctx, db.IsAgentMcpServerParams{SessionID: sessionID, McpServerID: serverID})
	if err != nil {
		return store.Session{}, err
	}
	if err := queries.ClearSessionMcpServer(ctx, db.ClearSessionMcpServerParams{SessionID: sessionID, McpServerID: serverID}); err != nil {
		return store.Session{}, err
	}
	if enabled != agents {
		err := queries.AddSessionMcpServer(ctx, db.AddSessionMcpServerParams{SessionID: sessionID, McpServerID: serverID, Enabled: enabled})
		if err != nil {
			return store.Session{}, err
		}
	}
	return session, tx.Commit(ctx)
}

func (s *records) ResetSessionMcpServers(ctx context.Context, sessionID uuid.UUID) (store.Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Session{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	session, err := openSession(ctx, queries, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if err := queries.ClearSessionMcpServers(ctx, sessionID); err != nil {
		return store.Session{}, err
	}
	return session, tx.Commit(ctx)
}

// openSession returns the session, ErrNotFound if there is no such session,
// and ErrSessionEnded if it has ended.
func openSession(ctx context.Context, queries *db.Queries, id uuid.UUID) (store.Session, error) {
	row, err := queries.GetSession(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Session{}, store.ErrNotFound
	case err != nil:
		return store.Session{}, err
	case row.EndedAt != nil:
		return store.Session{}, store.ErrSessionEnded
	}
	return sessionFrom(row), nil
}

func (s *records) SessionMcpServers(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := s.queries.ListSessionMcpServers(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	servers := map[uuid.UUID][]uuid.UUID{}
	for _, row := range rows {
		servers[row.SessionID] = append(servers[row.SessionID], row.McpServerID)
	}
	return servers, nil
}

func (s *records) Session(ctx context.Context, id uuid.UUID) (store.Session, error) {
	row, err := s.queries.GetSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	return sessionFrom(row), err
}

func (s *records) SetSessionTitle(ctx context.Context, id uuid.UUID, title string) (store.Session, error) {
	row, err := s.queries.SetSessionTitle(ctx, db.SetSessionTitleParams{ID: id, Title: title})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	return sessionFrom(row), err
}

func (s *records) SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error) {
	set, err := s.queries.SetSandbox(ctx, db.SetSandboxParams{ID: sessionID, Was: from, Sandbox: to})
	if err != nil || set == 1 {
		return err == nil, err
	}
	// The session does not exist, or its sandbox is not from.
	if _, err := s.queries.GetSession(ctx, sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}
	return false, nil
}

func (s *records) SetSandboxState(ctx context.Context, sessionID uuid.UUID, sandbox, state string) error {
	return s.queries.SetSandboxState(ctx, db.SetSandboxStateParams{ID: sessionID, Sandbox: sandbox, State: state})
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
	counts := make(map[string]int, len(rows)+1)
	for _, row := range rows {
		if row.State != nil {
			counts[*row.State] = int(row.Sandboxes)
		}
	}
	archived, err := s.queries.CountArchivedSessions(ctx)
	if err != nil {
		return nil, err
	}
	counts[store.SandboxArchived] = int(archived)
	return counts, nil
}

func (s *records) ClaimArchive(ctx context.Context, sessionID uuid.UUID, sandbox string, turns int, claim uuid.UUID, since time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// Lock the session before counting its turns, so that a concurrent
	// message either has added its turn or comes after the claim.
	row, err := queries.LockSession(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	session := sessionFrom(row)
	if session.EndedAt != nil || sandbox == "" || session.Sandbox != sandbox || session.SandboxState != store.SandboxAsleep ||
		session.Archive != nil || session.ClaimedAt != nil && session.ClaimedAt.After(since) {
		return false, nil
	}
	if unused, err := unused(ctx, queries, sessionID, turns); err != nil || !unused {
		return false, err
	}
	if err := queries.ClaimSession(ctx, db.ClaimSessionParams{ID: sessionID, Claim: &claim}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// unused reports whether the session has turns turns, all of which have
// ended.
func unused(ctx context.Context, queries *db.Queries, sessionID uuid.UUID, turns int) (bool, error) {
	has, err := queries.CountSessionTurns(ctx, sessionID)
	return err == nil && has.Turns == int64(turns) && has.Unfinished == 0, err
}

func (s *records) ArchiveSession(ctx context.Context, sessionID, claim uuid.UUID, turns int, archive store.Archive) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// Lock the session before counting its turns, so that a concurrent
	// message either has added its turn or finds the home archived.
	row, err := queries.LockSession(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.EndedAt != nil || row.Claim == nil || *row.Claim != claim || claim == uuid.Nil {
		return false, nil
	}
	if unused, err := unused(ctx, queries, sessionID, turns); err != nil || !unused {
		return false, err
	}
	err = queries.ArchiveSession(ctx, db.ArchiveSessionParams{ID: sessionID, Archive: &archive.ID, Bytes: &archive.Bytes})
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *records) ClaimRestore(ctx context.Context, sessionID, claim uuid.UUID) (store.Archive, bool, error) {
	row, err := s.queries.ClaimRestore(ctx, db.ClaimRestoreParams{ID: sessionID, Claim: &claim})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Archive{}, false, nil
	}
	if err != nil {
		return store.Archive{}, false, err
	}
	return *sessionFrom(row).Archive, true, nil
}

func (s *records) RestoreSession(ctx context.Context, sessionID, claim uuid.UUID, sandbox string) (bool, error) {
	restored, err := s.queries.RestoreSession(ctx, db.RestoreSessionParams{ID: sessionID, Claim: &claim, Sandbox: sandbox})
	return restored == 1, err
}

func (s *records) LeftArchives(ctx context.Context, before time.Time, limit int) ([]store.Session, error) {
	rows, err := s.queries.ListLeftArchives(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	old, err := s.queries.ListOldArchives(ctx, db.ListOldArchivesParams{Before: &before, Count: int32(limit)})
	if err != nil {
		return nil, err
	}
	sessions := make([]store.Session, 0, len(rows)+len(old))
	for _, row := range append(rows, old...) {
		sessions = append(sessions, sessionFrom(row))
	}
	return sessions, nil
}

func (s *records) ForgetArchive(ctx context.Context, sessionID, archive uuid.UUID) error {
	return s.queries.ForgetArchive(ctx, db.ForgetArchiveParams{ID: sessionID, Archive: &archive})
}

func (s *records) EndSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Session{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// Lock the session before checking its turns, so that a concurrent
	// message either has added its turn or finds the session ended.
	row, err := queries.LockSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	if err != nil {
		return store.Session{}, err
	}
	if row.EndedAt != nil {
		return sessionFrom(row), nil
	}
	row, err = queries.EndSession(ctx, db.EndSessionParams{ID: id, Reason: reason})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Session{}, store.ErrSessionBusy
	}
	if err != nil {
		return store.Session{}, err
	}
	return sessionFrom(row), tx.Commit(ctx)
}

func (s *records) Sessions(ctx context.Context, agentID, by, before uuid.UUID, limit int) ([]store.Session, error) {
	var rows []db.Session
	var err error
	if by == uuid.Nil {
		rows, err = s.queries.ListSessions(ctx, db.ListSessionsParams{AgentID: agentID, Before: before, Count: int32(limit)})
	} else {
		rows, err = s.queries.ListSessionsOf(ctx, db.ListSessionsOfParams{AgentID: agentID, CreatedBy: by, Before: before, Count: int32(limit)})
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

func (s *records) CreateProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	headers, err := headersIn(provider.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	params := db.CreateProviderParams{
		ID: provider.ID, ProjectID: provider.ProjectID, Name: provider.Name, Url: provider.URL, Headers: headers, ApiKey: provider.APIKey,
	}
	if at := provider.APIKeyAt; at != nil {
		params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = &at.Store, at.Path, at.Key
	}
	row, err := s.queries.CreateProvider(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Provider{}, store.ErrNotFound
	case err != nil:
		return store.Provider{}, err
	}
	return providerFrom(row)
}

func (s *records) Provider(ctx context.Context, id uuid.UUID) (store.Provider, error) {
	row, err := s.queries.GetProvider(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Provider{}, store.ErrNotFound
	case err != nil:
		return store.Provider{}, err
	}
	return providerFrom(row)
}

func (s *records) Providers(ctx context.Context, projectID uuid.UUID) ([]store.Provider, error) {
	rows, err := s.queries.ListProviders(ctx, projectID)
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

func (s *records) ReplaceProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	headers, err := headersIn(provider.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	params := db.ReplaceProviderParams{ID: provider.ID, Name: provider.Name, Url: provider.URL, Headers: headers, ApiKey: provider.APIKey}
	if at := provider.APIKeyAt; at != nil {
		params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = &at.Store, at.Path, at.Key
	}
	row, err := s.queries.ReplaceProvider(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Provider{}, store.ErrNotFound
	case err != nil:
		return store.Provider{}, err
	}
	return providerFrom(row)
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
	row, err := s.queries.CreateMcpServer(ctx, db.CreateMcpServerParams{
		ID: server.ID, ProjectID: server.ProjectID, Slug: server.Slug, Name: server.Name, Url: server.URL, Transport: server.Transport,
		Headers: headers, Tools: tools, ToolsListedAt: server.ToolsListedAt,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.McpServer{}, store.ErrNotFound
	case violates(err, mcpServerSlug):
		return store.McpServer{}, store.ErrSlugTaken
	case err != nil:
		return store.McpServer{}, err
	}
	return mcpServerFrom(row)
}

func (s *records) McpServer(ctx context.Context, id uuid.UUID) (store.McpServer, error) {
	row, err := s.queries.GetMcpServer(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.McpServer{}, store.ErrNotFound
	case err != nil:
		return store.McpServer{}, err
	}
	return mcpServerFrom(row)
}

func (s *records) McpServers(ctx context.Context, projectID uuid.UUID) ([]store.McpServer, error) {
	rows, err := s.queries.ListMcpServers(ctx, projectID)
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

func (s *records) ReplaceMcpServer(ctx context.Context, server store.McpServer) (store.McpServer, error) {
	headers, err := headersIn(server.Headers)
	if err != nil {
		return store.McpServer{}, err
	}
	tools, err := toolsIn(server.Tools, server.ToolsListedAt)
	if err != nil {
		return store.McpServer{}, err
	}
	row, err := s.queries.ReplaceMcpServer(ctx, db.ReplaceMcpServerParams{
		ID: server.ID, Name: server.Name, Url: server.URL, Transport: server.Transport, Headers: headers, Tools: tools,
		ToolsListedAt: server.ToolsListedAt,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.McpServer{}, store.ErrNotFound
	case err != nil:
		return store.McpServer{}, err
	}
	return mcpServerFrom(row)
}

func (s *records) SetMcpServerTools(ctx context.Context, id uuid.UUID, tools []store.Tool, listedAt time.Time) error {
	column, err := toolsIn(tools, &listedAt)
	if err != nil {
		return err
	}
	set, err := s.queries.SetMcpServerTools(ctx, db.SetMcpServerToolsParams{ID: id, Tools: column, ToolsListedAt: listedAt})
	if err == nil && set == 0 {
		return store.ErrNotFound
	}
	return err
}

func (s *records) DeleteMcpServer(ctx context.Context, id uuid.UUID) error {
	deleted, err := s.queries.DeleteMcpServer(ctx, id)
	if err == nil && deleted == 0 {
		return store.ErrNotFound
	}
	return err
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
		ID: row.ID, ProjectID: row.ProjectID, Slug: row.Slug, Name: row.Name, URL: row.Url, Transport: row.Transport,
		Headers: headers, Tools: tools, ToolsListedAt: row.ToolsListedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (s *records) CreateSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	row, err := s.queries.CreateSecretStore(ctx, db.CreateSecretStoreParams{
		ID: secrets.ID, ProjectID: secrets.ProjectID, Name: secrets.Name, Kind: secrets.Kind,
		Settings: secrets.Settings, Credential: secrets.Credential,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.SecretStore{}, store.ErrNotFound
	}
	return secretStoreFrom(row), err
}

func (s *records) SecretStore(ctx context.Context, id uuid.UUID) (store.SecretStore, error) {
	row, err := s.queries.GetSecretStore(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.SecretStore{}, store.ErrNotFound
	}
	return secretStoreFrom(row), err
}

func (s *records) SecretStores(ctx context.Context, projectID uuid.UUID) ([]store.SecretStore, error) {
	rows, err := s.queries.ListSecretStores(ctx, projectID)
	if err != nil {
		return nil, err
	}
	stores := make([]store.SecretStore, len(rows))
	for i, row := range rows {
		stores[i] = secretStoreFrom(row)
	}
	return stores, nil
}

func (s *records) ReplaceSecretStore(ctx context.Context, secrets store.SecretStore) (store.SecretStore, error) {
	row, err := s.queries.ReplaceSecretStore(ctx, db.ReplaceSecretStoreParams{
		ID: secrets.ID, Name: secrets.Name, Kind: secrets.Kind, Settings: secrets.Settings, Credential: secrets.Credential,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.SecretStore{}, store.ErrNotFound
	}
	return secretStoreFrom(row), err
}

func (s *records) CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, maxWaiting, maxRunning int) (store.Turn, int, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Turn{}, 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Turn{}, 0, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	if maxWaiting > 0 {
		// Accept messages one at a time across Supervisors, so that the
		// queue limit holds.
		if err := queries.Lock(ctx, queueLock); err != nil {
			return store.Turn{}, 0, err
		}
	}
	if _, err := queries.HoldSession(ctx, sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Turn{}, 0, store.ErrNotFound
		}
		return store.Turn{}, 0, err
	}
	params := db.CreateTurnParams{ID: id, SessionID: sessionID, Text: text}
	if by != uuid.Nil {
		params.CreatedBy = &by
	}
	row, err := queries.CreateTurn(ctx, params)
	var failure *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Turn{}, 0, store.ErrSessionEnded
	case errors.As(err, &failure) && failure.Code == uniqueViolation:
		return store.Turn{}, 0, store.ErrSessionBusy
	case err != nil:
		return store.Turn{}, 0, err
	}
	waiting, err := queries.CountWaitingTurns(ctx, int64(maxRunning))
	if err != nil {
		return store.Turn{}, 0, err
	}
	if maxWaiting > 0 && waiting > int64(maxWaiting) {
		return store.Turn{}, 0, store.ErrQueueFull
	}
	return turnFrom(row), int(max(waiting, 0)), tx.Commit(ctx)
}

func (s *records) AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	if err := queries.Lock(ctx, queueLock); err != nil {
		return err
	}
	queued, err := queries.QueuedTurns(ctx)
	if err != nil {
		return err
	}
	if err := announce(queued); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *records) Turn(ctx context.Context, id uuid.UUID) (store.Turn, error) {
	row, err := s.queries.GetTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Turn{}, store.ErrNotFound
	}
	return turnFrom(row), err
}

func (s *records) Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]store.Turn, error) {
	rows, err := s.queries.ListTurns(ctx, db.ListTurnsParams{SessionID: sessionID, Before: int32(before), Count: int32(limit)})
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
	rows, err := s.queries.ListLastTurns(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	last := make(map[uuid.UUID]store.Turn, len(rows))
	for _, row := range rows {
		last[row.SessionID] = turnFrom(row)
	}
	return last, nil
}

func (s *records) TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error) {
	rows, err := s.queries.ListTurnItems(ctx, turnIDs)
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID][]json.RawMessage{}
	for _, row := range rows {
		items[row.TurnID] = append(items[row.TurnID], row.Item)
	}
	return items, nil
}

func (s *records) DispatchTurns(ctx context.Context, limit int) ([]store.Dispatch, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// Dispatch on one Supervisor at a time, so that together they keep to the
	// limit.
	if err := queries.Lock(ctx, dispatchLock); err != nil {
		return nil, err
	}
	rows, err := queries.NextTurns(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	dispatches := []store.Dispatch{}
	for _, row := range rows {
		taken, err := queries.DispatchTurn(ctx, row.TurnID)
		if err != nil {
			return nil, err
		}
		if taken == 1 {
			dispatches = append(dispatches, store.Dispatch{
				TurnID: row.TurnID, Harness: row.Harness, Waited: time.Duration(row.Waited * float64(time.Second)),
			})
		}
	}
	return dispatches, tx.Commit(ctx)
}

func (s *records) CancelTurn(ctx context.Context, id uuid.UUID) (store.Turn, bool, error) {
	row, err := s.queries.CancelTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		turn, err := s.Turn(ctx, id)
		return turn, false, err
	}
	return turnFrom(row), err == nil, err
}

func (s *records) ExpireTurns(ctx context.Context, wait time.Duration) ([]store.Turn, error) {
	rows, err := s.queries.ExpireTurns(ctx, wait.Seconds())
	if err != nil {
		return nil, err
	}
	turns := make([]store.Turn, len(rows))
	for i, row := range rows {
		turns[i] = turnFrom(row)
	}
	return turns, nil
}

func (s *records) DispatchedTurns(ctx context.Context) ([]store.DispatchedTurn, error) {
	rows, err := s.queries.DispatchedTurns(ctx)
	if err != nil {
		return nil, err
	}
	turns := make([]store.DispatchedTurn, len(rows))
	for i, row := range rows {
		turns[i] = store.DispatchedTurn{ID: row.ID, Cancelling: time.Duration(row.Cancelling * float64(time.Second))}
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
			Harness: row.Harness, State: row.State, Started: row.Started,
			Age: time.Duration(row.Age * float64(time.Second)), Asking: int(row.Asking),
		}
	}
	return turns, nil
}

func (s *records) FollowTurn(ctx context.Context, turnID uuid.UUID) (store.FollowedTurn, error) {
	given, err := s.queries.FollowTurn(ctx, turnID)
	if err != nil {
		return store.FollowedTurn{}, err
	}
	row, err := s.queries.FollowedTurn(ctx, turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.FollowedTurn{}, store.ErrNotFound
	}
	if err != nil {
		return store.FollowedTurn{}, err
	}
	turn := turnFrom(row.Turn)
	switch {
	case given == 1:
	case turn.State == store.TurnQueued:
		return store.FollowedTurn{}, store.ErrNotFound
	default:
		return store.FollowedTurn{}, store.ErrTurnEnded
	}
	followed := store.FollowedTurn{
		Turn:         turn,
		AgentID:      row.AgentID,
		ProjectID:    row.ProjectID,
		Harness:      row.Harness,
		Instructions: row.Instructions,
		Approval:     row.Approval,
		ProviderURL:  row.ProviderUrl,
		Model:        row.Model,
		Dispatched:   time.Duration(row.Dispatched * float64(time.Second)),
	}
	if row.Sandbox != nil {
		followed.Sandbox = *row.Sandbox
	}
	if row.SandboxState != nil {
		followed.SandboxState = *row.SandboxState
	}
	return followed, nil
}

func (s *records) SessionTurn(ctx context.Context, sessionID uuid.UUID) (store.Origin, error) {
	row, err := s.queries.SessionTurn(ctx, sessionID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.Origin{}, store.ErrNotFound
	case err != nil:
		return store.Origin{}, err
	}
	return originOf(row)
}

func originOf(row db.SessionTurnRow) (store.Origin, error) {
	headers, err := headersFrom(row.ProviderHeaders)
	if err != nil {
		return store.Origin{}, err
	}
	origin := store.Origin{
		TurnID: row.TurnID, SessionID: row.SessionID, AgentID: row.AgentID, ProjectID: row.ProjectID, Subject: row.Subject,
		Provider: store.Provider{
			ID: row.ProviderID, ProjectID: row.ProjectID, URL: row.ProviderUrl, Headers: headers, APIKey: row.ProviderApiKey,
		},
	}
	if row.CreatedBy != nil {
		origin.UserID = *row.CreatedBy
	}
	if row.ApiKeyStoreID != nil && row.StoreCreatedAt != nil && row.StoreUpdatedAt != nil {
		origin.Provider.APIKeyAt = &store.SecretReference{Store: *row.ApiKeyStoreID, Path: row.ApiKeyPath, Key: row.ApiKeyName}
		origin.SecretStore = &store.SecretStore{
			ID: *row.ApiKeyStoreID, ProjectID: row.ProjectID, Name: row.StoreName, Kind: row.StoreKind,
			Settings: row.StoreSettings, Credential: row.StoreCredential,
			CreatedAt: *row.StoreCreatedAt, UpdatedAt: *row.StoreUpdatedAt,
		}
	}
	return origin, nil
}

func (s *records) StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error) {
	startup, err := s.queries.StartTurn(ctx, turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return time.Duration(startup * float64(time.Second)), err == nil, err
}

func (s *records) TurnHarness(ctx context.Context, turnID uuid.UUID) (string, error) {
	harness, err := s.queries.GetTurnHarness(ctx, turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return harness, err
}

func (s *records) SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *store.Usage) error {
	finished := db.SetFinishedParams{ID: turnID, StopReason: stopReason}
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Turn{}, false, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.EndTurn(ctx, db.EndTurnParams{
		ID:        turnID,
		Succeeded: succeeded,
		Reason:    failure.Reason,
		Message:   failure.Message,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Turn{}, false, nil
	}
	if err != nil {
		return store.Turn{}, false, err
	}
	turn := turnFrom(row)
	// Only the Supervisor whose update ended the turn gets here, so items is
	// called once.
	said, err := items(ctx)
	if err != nil {
		return store.Turn{}, false, fmt.Errorf("read turn items: %w", err)
	}
	if len(said) > 0 {
		rows := make([]db.AddTurnItemsParams, len(said))
		for i, item := range said {
			rows[i] = db.AddTurnItemsParams{TurnID: turnID, Seq: int32(i + 1), Item: storable(item)}
		}
		if _, err := queries.AddTurnItems(ctx, rows); err != nil {
			return store.Turn{}, false, err
		}
	}
	return turn, true, tx.Commit(ctx)
}

func (s *records) AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error {
	return s.queries.AddApproval(ctx, db.AddApprovalParams{TurnID: turnID, ID: id, Request: storable(request)})
}

func (s *records) ResolveApproval(ctx context.Context, turnID uuid.UUID, id, outcome string) (store.Approval, time.Duration, bool, error) {
	row, err := s.queries.ResolveApproval(ctx, db.ResolveApprovalParams{TurnID: turnID, ID: id, Outcome: &outcome})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Approval{}, 0, false, nil
	}
	resolved := store.Approval{Request: row.Request, Resolved: true}
	return resolved, time.Duration(row.Waited * float64(time.Second)), err == nil, err
}

func (s *records) Approval(ctx context.Context, turnID uuid.UUID, id string) (store.Approval, error) {
	row, err := s.queries.GetApproval(ctx, db.GetApprovalParams{TurnID: turnID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Approval{}, store.ErrNotFound
	}
	approval := store.Approval{Request: row.Request, Answered: row.AnsweredAt != nil, Resolved: row.ResolvedAt != nil}
	if row.AnsweredBy != nil {
		approval.AnsweredBy = *row.AnsweredBy
	}
	return approval, err
}

func (s *records) AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error) {
	params := db.AnswerApprovalParams{TurnID: turnID, ID: id, OptionID: optionID}
	if by != uuid.Nil {
		params.AnsweredBy = &by
	}
	answered, err := s.queries.AnswerApproval(ctx, params)
	return answered == 1, err
}

func (s *records) WaitingApprovals(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error) {
	rows, err := s.queries.WaitingApprovals(ctx, turnID)
	if err != nil {
		return nil, err
	}
	requests := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		requests[i] = row
	}
	return requests, nil
}

func (s *records) Answers(ctx context.Context, turnID *uuid.UUID) ([]store.Answer, error) {
	rows, err := s.queries.Answers(ctx, turnID)
	if err != nil {
		return nil, err
	}
	answers := make([]store.Answer, len(rows))
	for i, row := range rows {
		answers[i] = store.Answer{TurnID: row.TurnID, ID: row.ID, OptionID: row.OptionID}
	}
	return answers, nil
}

// storable replaces every escaped U+0000 in item with U+FFFD, since jsonb
// cannot store U+0000.
func storable(item json.RawMessage) []byte {
	if !bytes.Contains(item, []byte(`\u0000`)) {
		return item
	}
	var kept []byte
	for i := 0; i < len(item); i++ {
		switch {
		case item[i] != '\\':
			kept = append(kept, item[i])
		case bytes.HasPrefix(item[i:], []byte(`\u0000`)):
			kept = append(kept, `\ufffd`...)
			i += 5
		default:
			// Copy any other escape whole, so that what follows an escaped
			// backslash is not taken for an escape.
			kept = append(kept, item[i], item[i+1])
			i++
		}
	}
	return kept
}

func agentFrom(row db.Agent) store.Agent {
	return store.Agent{
		ID:           row.ID,
		ProjectID:    row.ProjectID,
		Slug:         row.Slug,
		Name:         row.Name,
		Description:  row.Description,
		Harness:      row.Harness,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		DeletedAt:    row.DeletedAt,
		Instructions: row.Instructions,
		Approval:     row.Approval,
		Model:        store.Model{Provider: row.ProviderID, Name: row.Model},
	}
}

func secretStoreFrom(row db.SecretStore) store.SecretStore {
	return store.SecretStore{
		ID:         row.ID,
		ProjectID:  row.ProjectID,
		Name:       row.Name,
		Kind:       row.Kind,
		Settings:   row.Settings,
		Credential: row.Credential,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func providerFrom(row db.Provider) (store.Provider, error) {
	headers, err := headersFrom(row.Headers)
	if err != nil {
		return store.Provider{}, err
	}
	provider := store.Provider{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		Name:      row.Name,
		URL:       row.Url,
		Headers:   headers,
		APIKey:    row.ApiKey,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.ApiKeyStoreID != nil {
		provider.APIKeyAt = &store.SecretReference{Store: *row.ApiKeyStoreID, Path: row.ApiKeyPath, Key: row.ApiKeyName}
	}
	return provider, nil
}

// headersIn returns the headers of an endpoint as their column holds them:
// null if there are none.
func headersIn(headers []store.Header) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	return json.Marshal(headers)
}

// headersFrom returns the headers that the column of an endpoint holds.
func headersFrom(column []byte) ([]store.Header, error) {
	if column == nil {
		return nil, nil
	}
	var headers []store.Header
	if err := json.Unmarshal(column, &headers); err != nil {
		return nil, fmt.Errorf("headers of an endpoint: %w", err)
	}
	return headers, nil
}

// toolsIn returns the tools of an MCP server as their column holds them:
// null if the server has not listed them.
func toolsIn(tools []store.Tool, listed *time.Time) ([]byte, error) {
	if listed == nil {
		return nil, nil
	}
	if tools == nil {
		tools = []store.Tool{}
	}
	return json.Marshal(tools)
}

// toolsFrom returns the tools that the column of an MCP server holds.
func toolsFrom(column []byte) ([]store.Tool, error) {
	if column == nil {
		return nil, nil
	}
	var tools []store.Tool
	if err := json.Unmarshal(column, &tools); err != nil {
		return nil, fmt.Errorf("tools of an MCP server: %w", err)
	}
	return tools, nil
}

func sessionFrom(row db.Session) store.Session {
	session := store.Session{ID: row.ID, AgentID: row.AgentID, Title: row.Title, CreatedAt: row.CreatedAt, EndedAt: row.EndedAt}
	if row.CreatedBy != nil {
		session.CreatedBy = *row.CreatedBy
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
	session.AsleepSince, session.Claimed, session.ClaimedAt = row.AsleepSince, row.Claim != nil, row.ClaimedAt
	if row.Archive != nil && row.ArchiveBytes != nil && row.ArchivedAt != nil {
		session.Archive = &store.Archive{ID: *row.Archive, Bytes: *row.ArchiveBytes, At: *row.ArchivedAt}
	}
	return session
}

func turnFrom(row db.Turn) store.Turn {
	turn := store.Turn{
		ID:                row.ID,
		SessionID:         row.SessionID,
		Seq:               int(row.Seq),
		State:             row.State,
		Text:              row.Text,
		CreatedAt:         row.CreatedAt,
		StartedAt:         row.StartedAt,
		StopReason:        row.StopReason,
		EndedAt:           row.EndedAt,
		CancelRequestedAt: row.CancelRequestedAt,
	}
	if row.CreatedBy != nil {
		turn.CreatedBy = *row.CreatedBy
	}
	if row.FailureReason != nil {
		turn.Failure = &store.Failure{Reason: *row.FailureReason}
		if row.FailureMessage != nil {
			turn.Failure.Message = *row.FailureMessage
		}
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
