// Package store keeps the Supervisor's records in PostgreSQL. Its queries are
// in queries.sql, and `make sqlc` writes the Go code for them to
// internal/gen/db.
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aviary/supervisor/internal/gen/db"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrSessionBusy = errors.New("session has an unfinished turn")
	ErrQueueFull   = errors.New("too many turns wait to run")
	ErrTurnTaken   = errors.New("turn was already taken")
	// ErrTurnCancelled is returned for a turn that was cancelled or given up
	// before its runner took it.
	ErrTurnCancelled = errors.New("turn was cancelled")
)

// The states of a turn. A queued turn waits to run, a dispatching one waits
// for its runner, and a running one has been taken by its runner. A turn ends
// as completed, failed or cancelled, or as expired if it waited too long.
const (
	TurnQueued      = "queued"
	TurnDispatching = "dispatching"
	TurnRunning     = "running"
	TurnCompleted   = "completed"
	TurnFailed      = "failed"
	TurnCancelled   = "cancelled"
	TurnExpired     = "expired"
)

// uniqueViolation is the PostgreSQL error code for a broken unique constraint.
const uniqueViolation = "23505"

// dispatchLock is the key of the advisory lock that is held while turns are
// dispatched.
const dispatchLock int64 = 0x617669617279 // "aviary"

// queueLock is the key of the advisory lock that is held while turns that
// wait are told their positions.
const queueLock int64 = dispatchLock + 1

type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
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
	// Instructions are what the agent is told before every turn, if anything.
	Instructions string `json:"instructions,omitempty"`
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
	// StartedAt is when the harness was given the message, once it has been.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// StopReason is why the harness ended the turn, once it has.
	StopReason *string    `json:"stop_reason,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	// Usage is the token usage of the turn, if the harness reported it.
	Usage *Usage `json:"usage,omitempty"`
	// CancelRequestedAt is when the turn was asked to stop, if it was. A
	// turn that had been dispatched by then ends once its pod has stopped.
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	// Failure is why the turn failed, if it did.
	Failure *Failure `json:"failure,omitempty"`
}

// Failure is why a turn failed.
type Failure struct {
	// Reason is a word for the kind of failure, one of the Failure
	// constants.
	Reason string `json:"reason"`
	// Message is what is known of this failure, for a person to read.
	Message string `json:"message,omitempty"`
}

// The kinds of failure of a turn.
const (
	// FailureHarness: the harness could not be started or answered with an
	// error. That is also how a harness reports that the model endpoint
	// refused it.
	FailureHarness = "harness_error"
	// FailureStartup: the pod of the turn did not start in time, for example
	// because an image could not be pulled.
	FailureStartup = "startup_timed_out"
	// FailureResume: the harness could not continue its session, because
	// what it had kept of the conversation is gone or cannot be read. The
	// next turn of the session starts a new harness session.
	FailureResume = "resume_failed"
	// FailureTimedOut: the turn ran for longer than a turn may.
	FailureTimedOut = "timed_out"
	// FailureOutOfMemory: the pod used more memory than it may.
	FailureOutOfMemory = "out_of_memory"
	// FailurePodLost: the pod was stopped from outside, or is gone.
	FailurePodLost = "pod_lost"
	// FailureSupervisorLost: the runner lost its connection to the
	// Supervisor.
	FailureSupervisorLost = "supervisor_lost"
	// FailureRunner: the runner failed in another way.
	FailureRunner = "runner_error"
)

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

// DispatchedTurn is a turn that was dispatched and has not ended.
type DispatchedTurn struct {
	ID uuid.UUID
	// Taken tells whether the runner of the turn has connected and taken
	// the turn.
	Taken bool
	// Age is how long ago the turn was dispatched.
	Age time.Duration
	// Cancelling is how long ago the turn was asked to stop, or zero if it
	// was not.
	Cancelling time.Duration
}

// TakenTurn is a turn as its runner gets it.
type TakenTurn struct {
	Turn
	// HarnessSessionID is the harness session to continue. It is nil in the
	// first turn of a session, which starts the harness session.
	HarnessSessionID *string
	// Instructions are the instructions of the session's agent.
	Instructions string
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
	return &Store{pool: pool, queries: db.New(pool)}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Project{}, err
	}
	row, err := s.queries.CreateProject(ctx, db.CreateProjectParams{ID: id, Name: name})
	return Project(row), err
}

func (s *Store) Project(ctx context.Context, id uuid.UUID) (Project, error) {
	row, err := s.queries.GetProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return Project(row), err
}

// CreateAgent returns ErrNotFound if the project does not exist.
func (s *Store) CreateAgent(ctx context.Context, projectID uuid.UUID, harness, instructions string) (Agent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Agent{}, err
	}
	row, err := s.queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: id, ProjectID: projectID, Harness: harness, Instructions: instructions,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return Agent(row), err
}

func (s *Store) Agent(ctx context.Context, id uuid.UUID) (Agent, error) {
	row, err := s.queries.GetAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return Agent(row), err
}

// CreateSession returns ErrNotFound if the agent does not exist.
func (s *Store) CreateSession(ctx context.Context, agentID uuid.UUID) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	row, err := s.queries.CreateSession(ctx, db.CreateSessionParams{ID: id, AgentID: agentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return sessionFrom(row), err
}

func (s *Store) Session(ctx context.Context, id uuid.UUID) (Session, error) {
	row, err := s.queries.GetSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return sessionFrom(row), err
}

// CreateTurn queues a message as the session's next turn. It returns
// ErrNotFound if the session does not exist, ErrSessionBusy if the session
// has a turn that has not finished, and ErrQueueFull if more than maxWaiting
// turns would then wait to run. Zero is no such limit. A queued turn waits
// only if the turns that run leave it no room among maxRunning.
func (s *Store) CreateTurn(ctx context.Context, sessionID uuid.UUID, text string, maxWaiting, maxRunning int) (Turn, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Turn{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	if maxWaiting > 0 {
		// Messages are accepted one at a time, so that the count holds also
		// when they reach several Supervisors at once.
		if err := queries.Lock(ctx, queueLock); err != nil {
			return Turn{}, err
		}
	}
	row, err := queries.CreateTurn(ctx, db.CreateTurnParams{ID: id, SessionID: sessionID, Text: text})
	var failure *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Turn{}, ErrNotFound
	case errors.As(err, &failure) && failure.Code == uniqueViolation:
		return Turn{}, ErrSessionBusy
	case err != nil:
		return Turn{}, err
	}
	if maxWaiting > 0 {
		waiting, err := queries.CountWaitingTurns(ctx, int64(maxRunning))
		if err != nil {
			return Turn{}, err
		}
		if waiting > int64(maxWaiting) {
			return Turn{}, ErrQueueFull
		}
	}
	return turnFrom(row), tx.Commit(ctx)
}

// AnnounceQueue calls announce with the turns that wait to run, in the order
// in which they will run. Only one such call runs at a time, also across
// Supervisors, so that of two calls the later one has the newer queue: the
// positions that the turns are told last are the latest.
func (s *Store) AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error {
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

// CancellingTurns returns the turns that were asked to stop and have not
// ended.
func (s *Store) CancellingTurns(ctx context.Context) ([]uuid.UUID, error) {
	return s.queries.CancellingTurns(ctx)
}

func (s *Store) Turn(ctx context.Context, id uuid.UUID) (Turn, error) {
	row, err := s.queries.GetTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	return turnFrom(row), err
}

// Turns returns the turns of a session that came before the one with the
// sequence number before, newest first and at most limit of them.
func (s *Store) Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]Turn, error) {
	rows, err := s.queries.ListTurns(ctx, db.ListTurnsParams{SessionID: sessionID, Before: int32(before), Count: int32(limit)})
	if err != nil {
		return nil, err
	}
	turns := make([]Turn, len(rows))
	for i, row := range rows {
		turns[i] = turnFrom(row)
	}
	return turns, nil
}

// TurnItems returns what the agent said and did in each of the turns, in
// order, by the ID of the turn. A turn has items once it has ended, if the
// agent said or did anything in it.
func (s *Store) TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error) {
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

// DispatchTurns makes queued turns wait for their runners, oldest first, for
// as long as fewer than limit turns are running or about to. It returns what
// it takes to run each of them.
func (s *Store) DispatchTurns(ctx context.Context, limit int) ([]Dispatch, error) {
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
	dispatches := make([]Dispatch, len(rows))
	for i, row := range rows {
		dispatches[i] = Dispatch{
			TurnID:    row.TurnID,
			SessionID: row.SessionID,
			AgentID:   row.AgentID,
			ProjectID: row.ProjectID,
			Harness:   row.Harness,
			Token:     rand.Text(),
		}
		err := queries.DispatchTurn(ctx, db.DispatchTurnParams{ID: row.TurnID, TokenHash: hashToken(dispatches[i].Token)})
		if err != nil {
			return nil, err
		}
	}
	return dispatches, tx.Commit(ctx)
}

// CancelTurn cancels a turn that has not ended. A turn that waits to run
// ends as cancelled at once. A turn that was dispatched is marked: it ends
// when its pod has stopped. CancelTurn returns the turn as it then is, and
// whether the turn had not ended before.
func (s *Store) CancelTurn(ctx context.Context, id uuid.UUID) (Turn, bool, error) {
	row, err := s.queries.CancelTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		turn, err := s.Turn(ctx, id)
		return turn, false, err
	}
	return turnFrom(row), err == nil, err
}

// ExpireTurns ends the turns that have waited to run for longer than wait
// as expired, and returns them.
func (s *Store) ExpireTurns(ctx context.Context, wait time.Duration) ([]Turn, error) {
	rows, err := s.queries.ExpireTurns(ctx, wait.Seconds())
	if err != nil {
		return nil, err
	}
	turns := make([]Turn, len(rows))
	for i, row := range rows {
		turns[i] = turnFrom(row)
	}
	return turns, nil
}

// DispatchedTurns returns the turns that were dispatched and have not ended.
func (s *Store) DispatchedTurns(ctx context.Context) ([]DispatchedTurn, error) {
	rows, err := s.queries.DispatchedTurns(ctx)
	if err != nil {
		return nil, err
	}
	turns := make([]DispatchedTurn, len(rows))
	for i, row := range rows {
		turns[i] = DispatchedTurn{
			ID:         row.ID,
			Taken:      row.Taken,
			Age:        time.Duration(row.Age * float64(time.Second)),
			Cancelling: time.Duration(row.Cancelling * float64(time.Second)),
		}
	}
	return turns, nil
}

// TakeTurn gives the turn that the token opens to its runner. A turn is given
// only once, because a runner that took it may already have acted on it. It
// returns ErrNotFound if the token opens no turn and ErrTurnTaken if the turn
// was given before.
func (s *Store) TakeTurn(ctx context.Context, token string) (TakenTurn, error) {
	hash := hashToken(token)
	row, err := s.queries.TakeTurn(ctx, hash)
	if err == nil {
		return TakenTurn{
			Turn: Turn{
				ID:        row.ID,
				SessionID: row.SessionID,
				Seq:       int(row.Seq),
				State:     TurnRunning,
				Text:      row.Text,
				CreatedAt: row.CreatedAt,
			},
			HarnessSessionID: row.HarnessSessionID,
			Instructions:     row.Instructions,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return TakenTurn{}, err
	}
	dispatching, err := s.queries.IsTurnDispatching(ctx, hash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return TakenTurn{}, ErrNotFound
	case err != nil:
		return TakenTurn{}, err
	case dispatching:
		// The turn was not taken, so it was cancelled or given up before
		// its runner came for it.
		return TakenTurn{}, ErrTurnCancelled
	}
	return TakenTurn{}, ErrTurnTaken
}

// RunningTurn returns the turn that the token belongs to, while that turn
// runs. It returns ErrNotFound for any other token.
func (s *Store) RunningTurn(ctx context.Context, token string) (uuid.UUID, error) {
	id, err := s.queries.RunningTurn(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// StartTurn records that the harness of the turn has been given the message,
// and the harness session that the turn runs in, for the next turn of the
// session to continue.
func (s *Store) StartTurn(ctx context.Context, turnID uuid.UUID, harnessSessionID string) error {
	return s.queries.StartTurn(ctx, db.StartTurnParams{TurnID: turnID, HarnessSessionID: harnessSessionID})
}

// SetFinished records how the harness ended the turn: why it stopped and,
// if the harness reported it, the token usage of the turn.
func (s *Store) SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *Usage) error {
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

// GiveUpTurn records why a turn whose runner has not taken it is given up.
// From then on the runner is not given the turn, and the turn fails for
// that reason once its pod is gone. It reports whether the turn was still
// waiting for its runner.
func (s *Store) GiveUpTurn(ctx context.Context, turnID uuid.UUID, failure Failure) (bool, error) {
	changed, err := s.queries.GiveUpTurn(ctx, db.GiveUpTurnParams{ID: turnID, Reason: failure.Reason, Message: failure.Message})
	return changed == 1, err
}

// EndTurn records that the pod of the turn has ended. The turn is completed
// if the pod succeeded and the runner reported why the harness stopped, and
// failed otherwise, for the reason that failure gives. A turn that was asked
// to stop is cancelled, unless the harness had ended it by itself. From then
// on the session accepts its next message. The second result is false if the
// turn had ended before, or does not exist.
//
// For a turn that it ends, EndTurn calls items and keeps what it returns as
// what the agent said and did in the turn. If that fails, the turn has not
// ended.
func (s *Store) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure Failure,
	items func(context.Context) ([]json.RawMessage, error)) (Turn, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, false, err
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
		return Turn{}, false, nil
	}
	if err != nil {
		return Turn{}, false, err
	}
	turn := turnFrom(row)
	// Of several Supervisors that end the turn at once, only the one that
	// gets here keeps the items.
	said, err := items(ctx)
	if err != nil {
		return Turn{}, false, fmt.Errorf("read what the turn said: %w", err)
	}
	if len(said) > 0 {
		rows := make([]db.AddTurnItemsParams, len(said))
		for i, item := range said {
			rows[i] = db.AddTurnItemsParams{TurnID: turnID, Seq: int32(i + 1), Item: storable(item)}
		}
		if _, err := queries.AddTurnItems(ctx, rows); err != nil {
			return Turn{}, false, err
		}
	}
	// A harness session that cannot be continued is not tried again: the
	// next turn of the session starts a new one. The session keeps its
	// files, and the agent starts without its memory of the conversation.
	if turn.Failure != nil && turn.Failure.Reason == FailureResume {
		if err := queries.ForgetHarnessSession(ctx, turn.SessionID); err != nil {
			return Turn{}, false, err
		}
	}
	return turn, true, tx.Commit(ctx)
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

// sessionFrom returns a row of the sessions table as a Session.
func sessionFrom(row db.Session) Session {
	return Session{ID: row.ID, AgentID: row.AgentID, CreatedAt: row.CreatedAt}
}

// turnFrom returns a row of the turns table as a Turn.
func turnFrom(row db.Turn) Turn {
	turn := Turn{
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
	if row.FailureReason != nil {
		turn.Failure = &Failure{Reason: *row.FailureReason}
		if row.FailureMessage != nil {
			turn.Failure.Message = *row.FailureMessage
		}
	}
	if row.TotalTokens != nil && row.InputTokens != nil && row.OutputTokens != nil {
		turn.Usage = &Usage{
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

// hashToken returns what is stored of a turn token. The token is random, so
// a plain hash is enough to keep it from being read back.
func hashToken(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}
