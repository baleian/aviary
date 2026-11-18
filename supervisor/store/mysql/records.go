package mysql

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"aviary/supervisor/store"
	"aviary/supervisor/store/mysql/internal/db"
	"aviary/supervisor/store/mysql/internal/key"
)

// The MySQL error numbers for a broken unique constraint, and for a
// transaction that was undone because it and another waited for each other.
const (
	duplicateEntry = 1062
	deadlock       = 1213
)

// The rows of the locks table: what only one Supervisor does at a time.
const (
	dispatchLock = "dispatch"
	queueLock    = "queue"
)

type records struct {
	pool    *sql.DB
	queries *db.Queries
}

// Open connects to the database at the address. Its schema must be at least
// as new as this build needs; Migrate brings it there.
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

// within runs the function in a transaction, which is committed if the
// function fails with nothing. MySQL undoes a transaction that waits for
// another that waits for it: such a one is run again.
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

// failed tells whether the error is the MySQL error with the number.
func failed(err error, number uint16) bool {
	var failure *mysql.MySQLError
	return errors.As(err, &failure) && failure.Number == number
}

// found returns the error of reading one row, which is ErrNotFound if there
// is no such row.
func found(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// created returns the error of creating a row of something that is to be
// there: ErrNotFound if no row was created, because it is not.
func created(rows int64, err error) error {
	if err == nil && rows == 0 {
		return store.ErrNotFound
	}
	return err
}

// document returns the JSON of a column that may hold none.
func document(column *json.RawMessage) json.RawMessage {
	if column == nil {
		return nil
	}
	return *column
}

// since returns the time that so many microseconds are.
func since(microseconds int64) time.Duration {
	return time.Duration(microseconds) * time.Microsecond
}

func (s *records) CreateProject(ctx context.Context, name string) (store.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Project{}, err
	}
	if err := s.queries.CreateProject(ctx, db.CreateProjectParams{ID: key.ID(id), Name: name}); err != nil {
		return store.Project{}, err
	}
	return s.Project(ctx, id)
}

func (s *records) Project(ctx context.Context, id uuid.UUID) (store.Project, error) {
	row, err := s.queries.GetProject(ctx, key.ID(id))
	return store.Project{ID: uuid.UUID(row.ID), Name: row.Name, CreatedAt: row.CreatedAt}, found(err)
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
		projects[i] = store.Project{ID: uuid.UUID(row.ID), Name: row.Name, CreatedAt: row.CreatedAt}
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
	// An empty list and not none: a role may have no permissions.
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

// roleFrom returns a row of the roles table as a Role.
func roleFrom(row db.Role) (store.Role, error) {
	role := store.Role{Name: row.Name, UpdatedAt: row.UpdatedAt}
	if row.UpdatedBy != nil {
		role.UpdatedBy = uuid.UUID(*row.UpdatedBy)
	}
	return role, json.Unmarshal(row.Permissions, &role.Permissions)
}

func (s *records) UserOf(ctx context.Context, subject, username, displayName string) (store.User, error) {
	// Most requests come from a user of whom nothing has changed.
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
	// Of the Supervisors that see a user first at once, one makes the user,
	// and the others find the user made.
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

// claim makes the user with the username the member that projects await
// under it.
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

// encoded returns the permissions as the JSON that a row keeps them as.
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

// memberFrom returns a row of the members of a project, with the user of
// the member if it has one, as a Member.
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

// userFrom returns a row of the users table as a User.
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
		ID: key.ID(id), ProjectID: key.ID(projectID), Harness: agent.Harness, Instructions: agent.Instructions,
		Resources: &agent.Resources, Approval: agent.Approval,
	}
	if agent.Model != nil {
		params.ProviderID, params.Model = (*key.ID)(&agent.Model.Provider), agent.Model.Name
	}
	if err := created(s.queries.CreateAgent(ctx, params)); err != nil {
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

// agentFrom returns a row of the agents table as an Agent.
func agentFrom(row db.Agent) store.Agent {
	agent := store.Agent{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Harness: row.Harness, CreatedAt: row.CreatedAt,
		Instructions: row.Instructions, Resources: document(row.Resources), Approval: row.Approval,
	}
	if row.ProviderID != nil {
		agent.Model = &store.Model{Provider: uuid.UUID(*row.ProviderID), Name: row.Model}
	}
	return agent
}

func (s *records) CreateSession(ctx context.Context, agentID, by uuid.UUID) (store.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	params := db.CreateSessionParams{ID: key.ID(id), AgentID: key.ID(agentID)}
	if by != uuid.Nil {
		params.CreatedBy = (*key.ID)(&by)
	}
	if err := created(s.queries.CreateSession(ctx, params)); err != nil {
		return store.Session{}, err
	}
	return s.Session(ctx, id)
}

func (s *records) Session(ctx context.Context, id uuid.UUID) (store.Session, error) {
	row, err := s.queries.GetSession(ctx, key.ID(id))
	return sessionFrom(row), found(err)
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

// sessionFrom returns a row of the sessions table as a Session.
func sessionFrom(row db.Session) store.Session {
	session := store.Session{ID: uuid.UUID(row.ID), AgentID: uuid.UUID(row.AgentID), CreatedAt: row.CreatedAt}
	if row.CreatedBy != nil {
		session.CreatedBy = uuid.UUID(*row.CreatedBy)
	}
	return session
}

// keyAt returns where the key of a provider is as the queries take it.
func keyAt(provider store.Provider) (storeID *key.ID, path, name string) {
	if at := provider.APIKeyAt; at != nil {
		return (*key.ID)(&at.Store), at.Path, at.Key
	}
	return nil, "", ""
}

func (s *records) CreateProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	params := db.CreateProviderParams{
		ID: key.ID(provider.ID), ProjectID: key.ID(provider.ProjectID), Name: provider.Name, Url: provider.URL, ApiKey: provider.APIKey,
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
	provider := store.Provider{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Name: row.Name, URL: row.Url, APIKey: row.ApiKey,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ApiKeyStoreID != nil {
		provider.APIKeyAt = &store.SecretReference{Store: uuid.UUID(*row.ApiKeyStoreID), Path: row.ApiKeyPath, Key: row.ApiKeyName}
	}
	return provider, nil
}

func (s *records) ReplaceProvider(ctx context.Context, provider store.Provider) (store.Provider, error) {
	params := db.ReplaceProviderParams{ID: key.ID(provider.ID), Name: provider.Name, Url: provider.URL, ApiKey: provider.APIKey}
	params.ApiKeyStoreID, params.ApiKeyPath, params.ApiKeyName = keyAt(provider)
	if err := created(s.queries.ReplaceProvider(ctx, params)); err != nil {
		return store.Provider{}, err
	}
	return s.Provider(ctx, provider.ID)
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
	return store.SecretStore{
		ID: uuid.UUID(row.ID), ProjectID: uuid.UUID(row.ProjectID), Name: row.Name, Kind: row.Kind,
		Settings: row.Settings, Credential: row.Credential, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, found(err)
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
			// Messages are accepted one at a time, so that the count holds
			// also when they reach several Supervisors at once.
			if _, err := queries.Lock(ctx, queueLock); err != nil {
				return err
			}
		}
		// So are the messages to one session, of which one is accepted.
		if _, err := queries.LockSession(ctx, key.ID(sessionID)); err != nil {
			return found(err)
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
		dispatched, err := queries.CountDispatchedTurns(ctx)
		if err != nil {
			return err
		}
		// A queued turn waits only if the turns that run leave it no room.
		waiting = queued - max(0, int64(maxRunning)-dispatched)
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

// ids returns the IDs as UUIDs.
func ids(rows []key.ID) []uuid.UUID {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = uuid.UUID(row)
	}
	return ids
}

func (s *records) CancellingTurns(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.queries.CancellingTurns(ctx)
	return ids(rows), err
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
		// One Supervisor at a time counts the running turns and picks the
		// next, so that several Supervisors together stay within the limit.
		if _, err := queries.Lock(ctx, dispatchLock); err != nil {
			return err
		}
		dispatched, err := queries.CountDispatchedTurns(ctx)
		if err != nil {
			return err
		}
		room := int64(limit) - dispatched
		if room <= 0 {
			return nil
		}
		rows, err := queries.NextTurns(ctx, int32(room))
		if err != nil {
			return err
		}
		for _, row := range rows {
			dispatch := store.Dispatch{
				TurnID:    uuid.UUID(row.TurnID),
				SessionID: uuid.UUID(row.SessionID),
				AgentID:   uuid.UUID(row.AgentID),
				ProjectID: uuid.UUID(row.ProjectID),
				Harness:   row.Harness,
				Resources: document(row.Resources),
				Approval:  row.Approval,
				// The agent's provider and model, if it names its model.
				ProviderURL: row.ProviderUrl,
				Model:       row.Model,
				Token:       store.NewToken(),
				Waited:      since(row.Waited),
			}
			taken, err := queries.DispatchTurn(ctx, db.DispatchTurnParams{ID: row.TurnID, TokenHash: store.HashToken(dispatch.Token)})
			if err != nil {
				return err
			}
			if taken == 1 {
				dispatches = append(dispatches, dispatch)
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
			// A queued turn ends at once. A dispatched one is only marked.
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
		turns[i] = store.DispatchedTurn{
			ID: uuid.UUID(row.ID), Taken: row.State == store.TurnRunning, Age: since(row.Age), Cancelling: since(row.Cancelling),
		}
	}
	return turns, nil
}

func (s *records) TakeTurn(ctx context.Context, token string, runner *uuid.UUID) (store.TakenTurn, error) {
	var taken store.TakenTurn
	err := s.within(ctx, func(queries *db.Queries) error {
		row, err := queries.LockTurnOfToken(ctx, store.HashToken(token))
		if err != nil {
			return found(err)
		}
		switch {
		// A turn that waits for its runner is given to the runner that asks.
		case row.State == store.TurnDispatching && row.CancelRequestedAt == nil && row.FailureReason == nil:
		// A turn that runs is given again to the runner that took it.
		case row.State == store.TurnRunning && runner != nil && row.RunnerID != nil && uuid.UUID(*row.RunnerID) == *runner:
		case row.State == store.TurnDispatching:
			// The turn was not taken, so it was cancelled or given up before
			// its runner came for it.
			return store.ErrTurnCancelled
		case row.State == store.TurnRunning:
			return store.ErrTurnTaken
		default:
			return store.ErrTurnEnded
		}
		of, err := queries.SessionOfTurn(ctx, row.SessionID)
		if err != nil {
			return err
		}
		taken = store.TakenTurn{
			Turn: store.Turn{
				ID: uuid.UUID(row.ID), SessionID: uuid.UUID(row.SessionID), Seq: int(row.Seq), State: store.TurnRunning,
				Text: row.Text, CreatedAt: row.CreatedAt,
			},
			HarnessSessionID: of.HarnessSessionID, Harness: of.Harness, Instructions: of.Instructions,
			Again: row.State == store.TurnRunning,
		}
		return queries.TakeTurn(ctx, db.TakeTurnParams{ID: row.ID, RunnerID: (*key.ID)(runner)})
	})
	if err != nil {
		return store.TakenTurn{}, err
	}
	return taken, nil
}

func (s *records) RunningTurn(ctx context.Context, token string) (store.Origin, error) {
	row, err := s.queries.RunningTurn(ctx, store.HashToken(token))
	if err != nil {
		return store.Origin{}, found(err)
	}
	origin := store.Origin{
		TurnID: uuid.UUID(row.TurnID), SessionID: uuid.UUID(row.SessionID), AgentID: uuid.UUID(row.AgentID), ProjectID: uuid.UUID(row.ProjectID),
	}
	if row.CreatedBy != nil {
		origin.UserID = uuid.UUID(*row.CreatedBy)
	}
	if row.ProviderID != nil {
		origin.Provider = &store.Provider{
			ID: uuid.UUID(*row.ProviderID), ProjectID: origin.ProjectID, URL: row.ProviderUrl, APIKey: row.ProviderApiKey,
		}
	}
	// The key of the provider is in a secret store, which comes with it.
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

func (s *records) StartTurn(ctx context.Context, turnID uuid.UUID, harnessSessionID string) (time.Duration, bool, error) {
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
		// The session keeps the harness session, for its next turn to
		// continue.
		if err := queries.SetHarnessSession(ctx, db.SetHarnessSessionParams{ID: row.SessionID, HarnessSessionID: &harnessSessionID}); err != nil {
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

func (s *records) GiveUpTurn(ctx context.Context, turnID uuid.UUID, failure store.Failure) (bool, error) {
	changed, err := s.queries.GiveUpTurn(ctx, db.GiveUpTurnParams{ID: key.ID(turnID), Reason: &failure.Reason, Message: failure.Message})
	return changed == 1, err
}

func (s *records) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure,
	items func(context.Context) ([]json.RawMessage, error)) (store.Turn, bool, error) {
	var turn store.Turn
	var ended bool
	err := s.within(ctx, func(queries *db.Queries) error {
		ended = false
		// Of several Supervisors that end the turn at once, one holds it,
		// and the others find it ended when they have it.
		row, err := queries.LockTurn(ctx, key.ID(turnID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && row.EndedAt != nil) {
			return nil
		}
		if err != nil {
			return err
		}
		said, err := items(ctx)
		if err != nil {
			return fmt.Errorf("read what the turn said: %w", err)
		}
		// A turn that was asked to stop is cancelled, unless the harness had
		// ended it by itself. Otherwise it is completed if its pod succeeded
		// and the runner reported why the harness stopped, and failed if
		// not. A failed turn keeps the reason that it was given up for, if
		// it was.
		end := db.EndTurnParams{ID: row.ID, State: store.TurnFailed}
		finished := succeeded && row.StopReason != nil
		switch {
		case row.CancelRequestedAt != nil && !(finished && *row.StopReason != "cancelled"):
			end.State = store.TurnCancelled
		case finished:
			end.State = store.TurnCompleted
		case row.FailureReason != nil:
			end.FailureReason, end.FailureMessage = row.FailureReason, row.FailureMessage
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

// turnFrom returns a row of the turns table as a Turn.
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
