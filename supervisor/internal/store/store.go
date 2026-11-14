// Package store keeps the Supervisor's records in PostgreSQL.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrSessionBusy = errors.New("session has an unfinished turn")
	ErrTurnTaken   = errors.New("turn was already taken")
)

// The states of a turn. A queued turn waits to run, a dispatching one waits
// for its runner, and a running one has been taken by its runner. A turn ends
// as completed or failed.
const (
	TurnQueued      = "queued"
	TurnDispatching = "dispatching"
	TurnRunning     = "running"
	TurnCompleted   = "completed"
	TurnFailed      = "failed"
)

// uniqueViolation is the PostgreSQL error code for a broken unique constraint.
const uniqueViolation = "23505"

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	pool *pgxpool.Pool
}

type Project struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Agent struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Harness   string    `json:"harness"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Turn is one message to a session and the work the agent does in reply.
type Turn struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Seq       int       `json:"seq"`
	State     string    `json:"state"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	// StopReason is why the harness ended the turn, once it has.
	StopReason *string    `json:"stop_reason,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}

// Dispatch is what it takes to run a turn.
type Dispatch struct {
	TurnID    uuid.UUID
	SessionID uuid.UUID
	AgentID   uuid.UUID
	ProjectID uuid.UUID
	Harness   string
	// Token is what the runner takes the turn with. Only its hash is stored.
	Token string
}

// Open connects to the database at url and brings its schema up to date.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Project{}, err
	}
	project := Project{ID: id, Name: name}
	err = s.pool.QueryRow(ctx,
		`insert into projects (id, name) values ($1, $2) returning created_at`,
		project.ID, project.Name,
	).Scan(&project.CreatedAt)
	return project, err
}

func (s *Store) Project(ctx context.Context, id uuid.UUID) (Project, error) {
	project := Project{ID: id}
	err := s.pool.QueryRow(ctx,
		`select name, created_at from projects where id = $1`, id,
	).Scan(&project.Name, &project.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

// CreateAgent returns ErrNotFound if the project does not exist.
func (s *Store) CreateAgent(ctx context.Context, projectID uuid.UUID, harness string) (Agent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Agent{}, err
	}
	agent := Agent{ID: id, ProjectID: projectID, Harness: harness}
	err = s.pool.QueryRow(ctx,
		`insert into agents (id, project_id, harness)
		 select $1, id, $3 from projects where id = $2
		 returning created_at`,
		agent.ID, agent.ProjectID, agent.Harness,
	).Scan(&agent.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return agent, err
}

func (s *Store) Agent(ctx context.Context, id uuid.UUID) (Agent, error) {
	agent := Agent{ID: id}
	err := s.pool.QueryRow(ctx,
		`select project_id, harness, created_at from agents where id = $1`, id,
	).Scan(&agent.ProjectID, &agent.Harness, &agent.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return agent, err
}

// CreateSession returns ErrNotFound if the agent does not exist.
func (s *Store) CreateSession(ctx context.Context, agentID uuid.UUID) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: id, AgentID: agentID}
	err = s.pool.QueryRow(ctx,
		`insert into sessions (id, agent_id)
		 select $1, id from agents where id = $2
		 returning created_at`,
		session.ID, session.AgentID,
	).Scan(&session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

func (s *Store) Session(ctx context.Context, id uuid.UUID) (Session, error) {
	session := Session{ID: id}
	err := s.pool.QueryRow(ctx,
		`select agent_id, created_at from sessions where id = $1`, id,
	).Scan(&session.AgentID, &session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

// CreateTurn queues a message as the session's next turn. It returns
// ErrNotFound if the session does not exist and ErrSessionBusy if the session
// has a turn that has not finished.
func (s *Store) CreateTurn(ctx context.Context, sessionID uuid.UUID, text string) (Turn, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Turn{}, err
	}
	turn := Turn{ID: id, SessionID: sessionID, State: TurnQueued, Text: text}
	err = s.pool.QueryRow(ctx,
		`insert into turns (id, session_id, seq, state, text)
		 select $1, id, (select coalesce(max(seq), 0) + 1 from turns where session_id = $2), $3, $4
		 from sessions where id = $2
		 returning seq, created_at`,
		turn.ID, turn.SessionID, turn.State, turn.Text,
	).Scan(&turn.Seq, &turn.CreatedAt)
	var failure *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Turn{}, ErrNotFound
	case errors.As(err, &failure) && failure.Code == uniqueViolation:
		return Turn{}, ErrSessionBusy
	}
	return turn, err
}

func (s *Store) Turn(ctx context.Context, id uuid.UUID) (Turn, error) {
	turn := Turn{ID: id}
	err := s.pool.QueryRow(ctx,
		`select session_id, seq, state, text, created_at, stop_reason, ended_at from turns where id = $1`, id,
	).Scan(&turn.SessionID, &turn.Seq, &turn.State, &turn.Text, &turn.CreatedAt, &turn.StopReason, &turn.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	return turn, err
}

// DispatchTurn makes a queued turn wait for its runner and returns what it
// takes to run it. It returns ErrNotFound if no queued turn has the ID.
func (s *Store) DispatchTurn(ctx context.Context, turnID uuid.UUID) (Dispatch, error) {
	dispatch := Dispatch{TurnID: turnID, Token: rand.Text()}
	err := s.pool.QueryRow(ctx,
		`update turns set state = $2, token_hash = $3
		 from sessions, agents
		 where turns.id = $1 and turns.state = $4
		   and sessions.id = turns.session_id and agents.id = sessions.agent_id
		 returning sessions.id, agents.id, agents.project_id, agents.harness`,
		turnID, TurnDispatching, hashToken(dispatch.Token), TurnQueued,
	).Scan(&dispatch.SessionID, &dispatch.AgentID, &dispatch.ProjectID, &dispatch.Harness)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dispatch{}, ErrNotFound
	}
	return dispatch, err
}

// TakeTurn gives the turn that the token opens to its runner. A turn is given
// only once, because a runner that took it may already have acted on it. It
// returns ErrNotFound if the token opens no turn and ErrTurnTaken if the turn
// was given before.
func (s *Store) TakeTurn(ctx context.Context, token string) (Turn, error) {
	hash := hashToken(token)
	turn := Turn{State: TurnRunning}
	err := s.pool.QueryRow(ctx,
		`update turns set state = $2 where token_hash = $1 and state = $3
		 returning id, session_id, seq, text, created_at`,
		hash, turn.State, TurnDispatching,
	).Scan(&turn.ID, &turn.SessionID, &turn.Seq, &turn.Text, &turn.CreatedAt)
	if !errors.Is(err, pgx.ErrNoRows) {
		return turn, err
	}
	var known bool
	err = s.pool.QueryRow(ctx,
		`select exists (select from turns where token_hash = $1)`, hash,
	).Scan(&known)
	if err != nil {
		return Turn{}, err
	}
	if known {
		return Turn{}, ErrTurnTaken
	}
	return Turn{}, ErrNotFound
}

// SetStopReason records why the harness ended the turn.
func (s *Store) SetStopReason(ctx context.Context, turnID uuid.UUID, stopReason string) error {
	_, err := s.pool.Exec(ctx, `update turns set stop_reason = $2 where id = $1`, turnID, stopReason)
	return err
}

// EndTurn records that the pod of the turn has ended. The turn is completed
// if the pod succeeded and the runner reported why the harness stopped, and
// failed otherwise. From then on the session accepts its next message. The
// second result is false if the turn had ended before, or does not exist.
func (s *Store) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool) (Turn, bool, error) {
	turn := Turn{ID: turnID}
	err := s.pool.QueryRow(ctx,
		`update turns set ended_at = now(),
		        state = case when $2 and stop_reason is not null then $3 else $4 end
		 where id = $1 and ended_at is null
		 returning session_id, seq, state, text, created_at, stop_reason, ended_at`,
		turnID, succeeded, TurnCompleted, TurnFailed,
	).Scan(&turn.SessionID, &turn.Seq, &turn.State, &turn.Text, &turn.CreatedAt, &turn.StopReason, &turn.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, false, nil
	}
	return turn, err == nil, err
}

// hashToken returns what is stored of a turn token. The token is random, so
// a plain hash is enough to keep it from being read back.
func hashToken(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

// migrate holds a lock in a table rather than on the connection, so that
// several Supervisors can start at once behind a connection pooler.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresTableLocker()
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
