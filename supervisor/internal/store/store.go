// Package store keeps the Supervisor's records in PostgreSQL.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

// dispatchLock is the key of the advisory lock that is held while turns are
// dispatched.
const dispatchLock int64 = 0x617669617279 // "aviary"

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
	// Usage is the token usage of the turn, if the harness reported it.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage is the token usage of a turn as its harness reported it. It is not a
// measurement: harnesses differ in what they count. One reports the sum of
// the model requests of the turn, another only the last request.
type Usage struct {
	TotalTokens       int64  `json:"total_tokens"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	ThoughtTokens     *int64 `json:"thought_tokens,omitempty"`
	CachedReadTokens  *int64 `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens *int64 `json:"cached_write_tokens,omitempty"`
}

// turnColumns are the columns of a turn, but for its ID, in the order in
// which scanTurn reads them.
const turnColumns = `session_id, seq, state, text, created_at, stop_reason, ended_at,
	total_tokens, input_tokens, output_tokens, thought_tokens, cached_read_tokens, cached_write_tokens`

func scanTurn(row pgx.Row, turn *Turn) error {
	var usage Usage
	var total, input, output *int64
	err := row.Scan(&turn.SessionID, &turn.Seq, &turn.State, &turn.Text, &turn.CreatedAt, &turn.StopReason, &turn.EndedAt,
		&total, &input, &output, &usage.ThoughtTokens, &usage.CachedReadTokens, &usage.CachedWriteTokens)
	if err != nil {
		return err
	}
	if total != nil && input != nil && output != nil {
		usage.TotalTokens, usage.InputTokens, usage.OutputTokens = *total, *input, *output
		turn.Usage = &usage
	}
	return nil
}

// DispatchedTurn is a turn that was dispatched and has not ended.
type DispatchedTurn struct {
	ID uuid.UUID
	// Age is how long ago the turn was dispatched.
	Age time.Duration
}

// TakenTurn is a turn as its runner gets it.
type TakenTurn struct {
	Turn
	// HarnessSessionID is the harness session to continue. It is nil in the
	// first turn of a session, which starts the harness session.
	HarnessSessionID *string
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

// Open connects to the database at url. Its schema must be at least as new
// as this build needs; Migrate brings it there.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := checkSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
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

// CreateTurn queues a message as the session's next turn and returns the
// turn with its position in the queue, which is 1 for the turn that runs
// next. It returns ErrNotFound if the session does not exist and
// ErrSessionBusy if the session has a turn that has not finished.
func (s *Store) CreateTurn(ctx context.Context, sessionID uuid.UUID, text string) (Turn, int, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Turn{}, 0, err
	}
	turn := Turn{ID: id, SessionID: sessionID, State: TurnQueued, Text: text}
	var position int
	// The count does not see the turn that the same statement inserts.
	err = s.pool.QueryRow(ctx,
		`with accepted as (
		     insert into turns (id, session_id, seq, state, text)
		     select $1, id, (select coalesce(max(seq), 0) + 1 from turns where session_id = $2), $3, $4
		     from sessions where id = $2
		     returning seq, created_at
		 )
		 select seq, created_at, (select count(*) from turns where state = $3) + 1 from accepted`,
		turn.ID, turn.SessionID, turn.State, turn.Text,
	).Scan(&turn.Seq, &turn.CreatedAt, &position)
	var failure *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Turn{}, 0, ErrNotFound
	case errors.As(err, &failure) && failure.Code == uniqueViolation:
		return Turn{}, 0, ErrSessionBusy
	}
	return turn, position, err
}

// QueuedTurns returns the IDs of the queued turns in the order in which they
// will run.
func (s *Store) QueuedTurns(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`select id from turns where state = $1 order by created_at, id`, TurnQueued,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (s *Store) Turn(ctx context.Context, id uuid.UUID) (Turn, error) {
	turn := Turn{ID: id}
	err := scanTurn(s.pool.QueryRow(ctx, `select `+turnColumns+` from turns where id = $1`, id), &turn)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	return turn, err
}

// DispatchTurns makes queued turns wait for their runners, oldest first, for
// as long as fewer than limit turns are running or about to. It returns what
// it takes to run each of them.
func (s *Store) DispatchTurns(ctx context.Context, limit int) ([]Dispatch, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// One Supervisor at a time counts the running turns and picks the next,
	// so that several Supervisors together stay within the limit.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, dispatchLock); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		`select turns.id, sessions.id, agents.id, agents.project_id, agents.harness
		 from turns
		 join sessions on sessions.id = turns.session_id
		 join agents on agents.id = sessions.agent_id
		 where turns.state = $1
		 order by turns.created_at, turns.id
		 limit greatest($2 - (select count(*) from turns where state in ($3, $4)), 0)`,
		TurnQueued, limit, TurnDispatching, TurnRunning,
	)
	if err != nil {
		return nil, err
	}
	dispatches, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Dispatch, error) {
		var dispatch Dispatch
		err := row.Scan(&dispatch.TurnID, &dispatch.SessionID, &dispatch.AgentID, &dispatch.ProjectID, &dispatch.Harness)
		return dispatch, err
	})
	if err != nil {
		return nil, err
	}
	for i := range dispatches {
		dispatches[i].Token = rand.Text()
		_, err := tx.Exec(ctx,
			`update turns set state = $2, token_hash = $3, dispatched_at = now() where id = $1`,
			dispatches[i].TurnID, TurnDispatching, hashToken(dispatches[i].Token),
		)
		if err != nil {
			return nil, err
		}
	}
	return dispatches, tx.Commit(ctx)
}

// DispatchedTurns returns the turns that were dispatched and have not ended.
func (s *Store) DispatchedTurns(ctx context.Context) ([]DispatchedTurn, error) {
	rows, err := s.pool.Query(ctx,
		`select id, extract(epoch from now() - coalesce(dispatched_at, created_at))::float8
		 from turns where state in ($1, $2)`,
		TurnDispatching, TurnRunning,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (DispatchedTurn, error) {
		var turn DispatchedTurn
		var seconds float64
		err := row.Scan(&turn.ID, &seconds)
		turn.Age = time.Duration(seconds * float64(time.Second))
		return turn, err
	})
}

// TakeTurn gives the turn that the token opens to its runner. A turn is given
// only once, because a runner that took it may already have acted on it. It
// returns ErrNotFound if the token opens no turn and ErrTurnTaken if the turn
// was given before.
func (s *Store) TakeTurn(ctx context.Context, token string) (TakenTurn, error) {
	hash := hashToken(token)
	turn := TakenTurn{Turn: Turn{State: TurnRunning}}
	err := s.pool.QueryRow(ctx,
		`update turns set state = $2
		 from sessions
		 where turns.token_hash = $1 and turns.state = $3 and sessions.id = turns.session_id
		 returning turns.id, turns.session_id, turns.seq, turns.text, turns.created_at,
		           sessions.harness_session_id`,
		hash, turn.State, TurnDispatching,
	).Scan(&turn.ID, &turn.SessionID, &turn.Seq, &turn.Text, &turn.CreatedAt, &turn.HarnessSessionID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return turn, err
	}
	var known bool
	err = s.pool.QueryRow(ctx,
		`select exists (select from turns where token_hash = $1)`, hash,
	).Scan(&known)
	if err != nil {
		return TakenTurn{}, err
	}
	if known {
		return TakenTurn{}, ErrTurnTaken
	}
	return TakenTurn{}, ErrNotFound
}

// RunningTurn returns the turn that the token belongs to, while that turn
// runs. It returns ErrNotFound for any other token.
func (s *Store) RunningTurn(ctx context.Context, token string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`select id from turns where token_hash = $1 and state = $2`,
		hashToken(token), TurnRunning,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// SetHarnessSessionrecords the harness session that the turn runs in, for
// the next turn of the session to continue.
func (s *Store) SetHarnessSession(ctx context.Context, turnID uuid.UUID, harnessSessionID string) error {
	_, err := s.pool.Exec(ctx,
		`update sessions set harness_session_id = $2
		 from turns
		 where turns.id = $1 and sessions.id = turns.session_id`,
		turnID, harnessSessionID,
	)
	return err
}

// SetFinished records how the harness ended the turn: why it stopped and,
// if the harness reported it, the token usage of the turn.
func (s *Store) SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *Usage) error {
	if usage == nil {
		_, err := s.pool.Exec(ctx, `update turns set stop_reason = $2 where id = $1`, turnID, stopReason)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`update turns set stop_reason = $2, total_tokens = $3, input_tokens = $4, output_tokens = $5,
		        thought_tokens = $6, cached_read_tokens = $7, cached_write_tokens = $8
		 where id = $1`,
		turnID, stopReason, usage.TotalTokens, usage.InputTokens, usage.OutputTokens,
		usage.ThoughtTokens, usage.CachedReadTokens, usage.CachedWriteTokens,
	)
	return err
}

// EndTurn records that the pod of the turn has ended. The turn is completed
// if the pod succeeded and the runner reported why the harness stopped, and
// failed otherwise. From then on the session accepts its next message. The
// second result is false if the turn had ended before, or does not exist.
func (s *Store) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool) (Turn, bool, error) {
	turn := Turn{ID: turnID}
	err := scanTurn(s.pool.QueryRow(ctx,
		`update turns set ended_at = now(),
		        state = case when $2 and stop_reason is not null then $3 else $4 end
		 where id = $1 and ended_at is null
		 returning `+turnColumns,
		turnID, succeeded, TurnCompleted, TurnFailed,
	), &turn)
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
