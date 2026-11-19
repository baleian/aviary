// Package postgres keeps the Supervisor's records in PostgreSQL. Its queries
// are in queries.sql, and `make sqlc` writes the Go code for them to
// internal/db.
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

// uniqueViolation is the PostgreSQL error code for a broken unique constraint.
const uniqueViolation = "23505"

// dispatchLock is the key of the advisory lock that is held while turns are
// dispatched.
const dispatchLock int64 = 0x617669617279 // "aviary"

// queueLock is the key of the advisory lock that is held while turns that
// wait are told their positions.
const queueLock int64 = dispatchLock + 1

type records struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// Plugin connects the Supervisor to PostgreSQL databases, whose addresses
// begin with postgres:// or postgresql://.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"postgres", "postgresql"} }

// Open connects to the database at the address. Its schema must be at least
// as new as this build needs; Migrate brings it there.
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

func (s *records) CreateProject(ctx context.Context, name string) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	row, err := s.queries.CreateProject(ctx, db.CreateProjectParams{ID: id, Name: name})
	return store.Project(row), err
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
	// A list and not none: no group is asked for, and not every group.
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
	// An empty list and not none: a role may have no permissions.
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

// roleFrom returns a row of the roles table as a Role.
func roleFrom(row db.Role) store.Role {
	role := store.Role{Name: row.Name, Permissions: row.Permissions, UpdatedAt: row.UpdatedAt}
	if row.UpdatedBy != nil {
		role.UpdatedBy = *row.UpdatedBy
	}
	return role
}

func (s *records) UserOf(ctx context.Context, subject, username, displayName string) (store.User, error) {
	// Most requests come from a user of whom nothing has changed.
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
	// A Supervisor that sees the user first at the same moment may have
	// made the user with the username meanwhile: then the user is there.
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

// claim makes the user with the username the member that projects await
// under it.
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
	// A list and not none: no group is asked for, and not every group.
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

// memberFrom returns a row of the members of a project, with the user of
// the member if it has one, as a Member.
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

// userFrom returns a row of the users table as a User.
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
		ID: id, ProjectID: projectID, Harness: agent.Harness, Instructions: agent.Instructions,
		Resources: agent.Resources, Approval: agent.Approval,
	}
	if agent.Model != nil {
		params.ProviderID, params.Model = &agent.Model.Provider, agent.Model.Name
	}
	row, err := s.queries.CreateAgent(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Agent{}, store.ErrNotFound
	}
	return agentFrom(row), err
}

func (s *records) Agent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	row, err := s.queries.GetAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Agent{}, store.ErrNotFound
	}
	return agentFrom(row), err
}

func (s *records) Agents(ctx context.Context, projectID uuid.UUID) ([]store.Agent, error) {
	rows, err := s.queries.ListAgents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	agents := make([]store.Agent, len(rows))
	for i, row := range rows {
		agents[i] = agentFrom(row)
	}
	return agents, nil
}

func (s *records) CreateSession(ctx context.Context, agentID, by uuid.UUID) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	params := db.CreateSessionParams{ID: id, AgentID: agentID}
	if by != uuid.Nil {
		params.CreatedBy = &by
	}
	row, err := s.queries.CreateSession(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	return sessionFrom(row), err
}

func (s *records) Session(ctx context.Context, id uuid.UUID) (store.Session, error) {
	row, err := s.queries.GetSession(ctx, id)
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
	// The session is not there, or has another sandbox than it was said to.
	if _, err := s.queries.GetSession(ctx, sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}
	return false, nil
}

func (s *records) EndSession(ctx context.Context, id uuid.UUID) (store.Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Session{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	// The session is held first, and its turns are looked at then: a message
	// that is taken at the same moment is a turn by then, or finds the
	// session ended.
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
	row, err = queries.EndSession(ctx, id)
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
	params := db.CreateProviderParams{
		ID: provider.ID, ProjectID: provider.ProjectID, Name: provider.Name, Url: provider.URL, ApiKey: provider.APIKey,
	}
	if at := provider.APIKeyAt; at != nil {
		params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = &at.Store, at.Path, at.Key
	}
	row, err := s.queries.CreateProvider(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Provider{}, store.ErrNotFound
	}
	return providerFrom(row), err
}

func (s *records) Provider(ctx context.Context, id uuid.UUID) (store.Provider, error) {
	row, err := s.queries.GetProvider(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Provider{}, store.ErrNotFound
	}
	return providerFrom(row), err
}

func (s *records) ReplaceProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	params := db.ReplaceProviderParams{ID: provider.ID, Name: provider.Name, Url: provider.URL, ApiKey: provider.APIKey}
	if at := provider.APIKeyAt; at != nil {
		params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = &at.Store, at.Path, at.Key
	}
	row, err := s.queries.ReplaceProvider(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Provider{}, store.ErrNotFound
	}
	return providerFrom(row), err
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
		// Messages are accepted one at a time, so that the count holds also
		// when they reach several Supervisors at once.
		if err := queries.Lock(ctx, queueLock); err != nil {
			return store.Turn{}, 0, err
		}
	}
	// The session is held before it is looked at, so that it is not ended
	// while the message is taken.
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

func (s *records) CancellingTurns(ctx context.Context) ([]uuid.UUID, error) {
	return s.queries.CancellingTurns(ctx)
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
	// One Supervisor at a time counts the running turns and picks the next,
	// so that several Supervisors together stay within the limit.
	if err := queries.Lock(ctx, dispatchLock); err != nil {
		return nil, err
	}
	rows, err := queries.NextTurns(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	dispatches := []store.Dispatch{}
	for _, row := range rows {
		dispatch := store.Dispatch{
			TurnID:    row.TurnID,
			SessionID: row.SessionID,
			AgentID:   row.AgentID,
			ProjectID: row.ProjectID,
			Harness:   row.Harness,
			Resources: row.Resources,
			Approval:  row.Approval,
			// The agent's provider and model, if it names its model.
			ProviderURL: row.ProviderUrl,
			Model:       row.Model,
			Token:       store.NewToken(),
			Waited:      time.Duration(row.Waited * float64(time.Second)),
		}
		taken, err := queries.DispatchTurn(ctx, db.DispatchTurnParams{ID: row.TurnID, TokenHash: store.HashToken(dispatch.Token)})
		if err != nil {
			return nil, err
		}
		if taken == 1 {
			dispatches = append(dispatches, dispatch)
		}
	}
	return dispatches, tx.Commit(ctx)
}

func (s *records) RequeueTurn(ctx context.Context, turnID uuid.UUID) (bool, error) {
	requeued, err := s.queries.RequeueTurn(ctx, turnID)
	return requeued == 1, err
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
		turns[i] = store.DispatchedTurn{
			ID:         row.ID,
			Taken:      row.Taken,
			Age:        time.Duration(row.Age * float64(time.Second)),
			Cancelling: time.Duration(row.Cancelling * float64(time.Second)),
		}
	}
	return turns, nil
}

func (s *records) TakeTurn(ctx context.Context, token string, runner *uuid.UUID) (store.TakenTurn, error) {
	hash := store.HashToken(token)
	row, err := s.queries.TakeTurn(ctx, db.TakeTurnParams{TokenHash: hash, RunnerID: runner})
	if err == nil {
		return store.TakenTurn{
			Turn: store.Turn{
				ID:        row.ID,
				SessionID: row.SessionID,
				Seq:       int(row.Seq),
				State:     store.TurnRunning,
				Text:      row.Text,
				CreatedAt: row.CreatedAt,
			},
			HarnessSessionID: row.HarnessSessionID,
			Harness:          row.Harness,
			Instructions:     row.Instructions,
			Again:            row.Again,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.TakenTurn{}, err
	}
	state, err := s.queries.GetTurnState(ctx, hash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.TakenTurn{}, store.ErrNotFound
	case err != nil:
		return store.TakenTurn{}, err
	case state == store.TurnDispatching:
		// The turn was not taken, so it was cancelled or given up before
		// its runner came for it.
		return store.TakenTurn{}, store.ErrTurnCancelled
	case state == store.TurnRunning:
		return store.TakenTurn{}, store.ErrTurnTaken
	}
	return store.TakenTurn{}, store.ErrTurnEnded
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
		// The turn ended since it was looked for.
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
		Waiting:      row.Waiting,
		Answered:     time.Duration(row.Answered * float64(time.Second)),
	}
	if row.Sandbox != nil {
		followed.Sandbox = *row.Sandbox
	}
	return followed, nil
}

func (s *records) RunningTurn(ctx context.Context, token string) (store.Origin, error) {
	row, err := s.queries.RunningTurn(ctx, store.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Origin{}, store.ErrNotFound
	}
	return originOf(row), err
}

func (s *records) SandboxTurn(ctx context.Context, sandbox string) (store.Origin, error) {
	if sandbox == "" {
		return store.Origin{}, store.ErrNotFound
	}
	row, err := s.queries.SandboxTurn(ctx, &sandbox)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Origin{}, store.ErrNotFound
	}
	return originOf(db.RunningTurnRow(row)), err
}

// originOf is where a running turn comes from, as a row tells it.
func originOf(row db.RunningTurnRow) store.Origin {
	origin := store.Origin{TurnID: row.TurnID, SessionID: row.SessionID, AgentID: row.AgentID, ProjectID: row.ProjectID, Subject: row.Subject}
	if row.CreatedBy != nil {
		origin.UserID = *row.CreatedBy
	}
	if row.ProviderID != nil {
		origin.Provider = &store.Provider{
			ID: *row.ProviderID, ProjectID: row.ProjectID, URL: row.ProviderUrl, APIKey: row.ProviderApiKey,
		}
	}
	// The key of the provider is in a secret store, which comes with it.
	if row.ApiKeyStoreID != nil && row.StoreCreatedAt != nil && row.StoreUpdatedAt != nil {
		origin.Provider.APIKeyAt = &store.SecretReference{Store: *row.ApiKeyStoreID, Path: row.ApiKeyPath, Key: row.ApiKeyName}
		origin.SecretStore = &store.SecretStore{
			ID: *row.ApiKeyStoreID, ProjectID: row.ProjectID, Name: row.StoreName, Kind: row.StoreKind,
			Settings: row.StoreSettings, Credential: row.StoreCredential,
			CreatedAt: *row.StoreCreatedAt, UpdatedAt: *row.StoreUpdatedAt,
		}
	}
	return origin
}

func (s *records) StartTurn(ctx context.Context, turnID uuid.UUID, harnessSessionID string) (time.Duration, bool, error) {
	startup, err := s.queries.StartTurn(ctx, db.StartTurnParams{TurnID: turnID, HarnessSessionID: harnessSessionID})
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

func (s *records) GiveUpTurn(ctx context.Context, turnID uuid.UUID, failure store.Failure) (bool, error) {
	changed, err := s.queries.GiveUpTurn(ctx, db.GiveUpTurnParams{ID: turnID, Reason: failure.Reason, Message: failure.Message})
	return changed == 1, err
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
	// Of several Supervisors that end the turn at once, only the one that
	// gets here keeps the items.
	said, err := items(ctx)
	if err != nil {
		return store.Turn{}, false, fmt.Errorf("read what the turn said: %w", err)
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

// storable returns an item as PostgreSQL can keep it. It cannot keep the
// character U+0000 in jsonb, so the replacement character takes its place.
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
			// Another escaped character, which may be a backslash.
			kept = append(kept, item[i], item[i+1])
			i++
		}
	}
	return kept
}

// agentFrom returns a row of the agents table as an Agent.
func agentFrom(row db.Agent) store.Agent {
	agent := store.Agent{
		ID:           row.ID,
		ProjectID:    row.ProjectID,
		Harness:      row.Harness,
		CreatedAt:    row.CreatedAt,
		Instructions: row.Instructions,
		Resources:    row.Resources,
		Approval:     row.Approval,
	}
	if row.ProviderID != nil {
		agent.Model = &store.Model{Provider: *row.ProviderID, Name: row.Model}
	}
	return agent
}

// secretStoreFrom returns a row of the secret_stores table as a SecretStore.
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

// providerFrom returns a row of the providers table as a Provider.
func providerFrom(row db.Provider) store.Provider {
	provider := store.Provider{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		Name:      row.Name,
		URL:       row.Url,
		APIKey:    row.ApiKey,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.ApiKeyStoreID != nil {
		provider.APIKeyAt = &store.SecretReference{Store: *row.ApiKeyStoreID, Path: row.ApiKeyPath, Key: row.ApiKeyName}
	}
	return provider
}

// sessionFrom returns a row of the sessions table as a Session.
func sessionFrom(row db.Session) store.Session {
	session := store.Session{ID: row.ID, AgentID: row.AgentID, CreatedAt: row.CreatedAt, EndedAt: row.EndedAt}
	if row.CreatedBy != nil {
		session.CreatedBy = *row.CreatedBy
	}
	if row.Sandbox != nil {
		session.Sandbox = *row.Sandbox
	}
	return session
}

// turnFrom returns a row of the turns table as a Turn.
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
