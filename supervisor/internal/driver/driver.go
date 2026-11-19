// Package driver has each turn run in the sandbox of its session, and
// records what the runner in the sandbox tells of the turn.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

const (
	// recheck is how often the driver looks for queued turns without being
	// woken, which retries a dispatch that failed.
	recheck = 5 * time.Second
	// Sweep is how often a Supervisor looks at the turns that run: it says
	// again that it follows its own, takes up those that no Supervisor
	// follows any more, and looks for what the runners are to be told and
	// no Supervisor announced.
	Sweep = 10 * time.Second
	// CancelGrace is how long a turn that was asked to stop is given to end
	// by itself before its sandbox is deleted.
	CancelGrace = 30 * time.Second
)

// What a sandbox is made with, for one who looks at the sandboxes to know
// whose each is.
const (
	metadataSession = "aviary-session"
	metadataAgent   = "aviary-agent"
	metadataProject = "aviary-project"
)

type Config struct {
	// Templates are what the sandboxes are made from: the harnesses that
	// agents can use, by name, as the release had their templates built.
	Templates sandbox.Templates
	// Idle is how long the sandbox of a session may be idle before it is
	// put to sleep: no turn of the session runs, or its turn waits for a
	// person. It is woken for the next turn, and for the answer. Zero is
	// never to put it to sleep.
	Idle time.Duration
	// Retention is for how long the sandbox of a session is kept after a
	// turn of the session was last given to it. A session that is not used
	// for longer has ended: its sandbox is deleted, with what the agent
	// had in it. Zero is to keep it until the session is ended.
	Retention time.Duration
	// MaxStartup is how long a turn may take until its harness has the
	// message: the sandbox of its session may have to be made first. A turn
	// that takes longer fails. Zero is no limit.
	MaxStartup time.Duration
	// MaxTurn is how long a turn may run.
	MaxTurn time.Duration
	// MaxQueueWait is how long a turn may wait to run. A turn that waits
	// longer ends as expired. Zero is no limit.
	MaxQueueWait time.Duration
	// MaxQueuedTurns is the most turns that may wait to run. A message that
	// would make one more is refused. Zero is no limit.
	MaxQueuedTurns int
	// MaxConcurrentTurns is the most turns that run at once. The turns
	// beyond it wait in the order in which they were accepted.
	MaxConcurrentTurns int
	// Sweep is how often the turns that run are looked at, and CancelGrace
	// how long a turn may take to stop. Zero is Sweep and CancelGrace.
	Sweep       time.Duration
	CancelGrace time.Duration
}

type Driver struct {
	config Config
	// id names this Supervisor among those that follow turns.
	id        uuid.UUID
	sandboxes *sandbox.Client
	store     store.Store
	events    *events.Streams
	metrics   *metrics.Metrics
	wake      chan struct{}
	// moved is set when a turn left the queue without being dispatched.
	moved atomic.Bool
	// sweeps counts the sweeps, of which only some look for sandboxes that
	// no session has.
	sweeps int
	// following are the turns that this Supervisor follows.
	mu        sync.Mutex
	following map[uuid.UUID]*orders
}

// orders are what the one who follows a turn is told.
type orders struct {
	// leave ends the following: another Supervisor follows the turn.
	leave context.CancelFunc
	// stop is filled when the turn was asked to stop, and answers when one
	// of its requests for approval may have an answer.
	stop, answers chan struct{}
}

// New returns a driver that has the turns run in the sandboxes of the
// sandbox service.
func New(config Config, sandboxes *sandbox.Client, st store.Store, ev *events.Streams, m *metrics.Metrics) *Driver {
	if config.Sweep == 0 {
		config.Sweep = Sweep
	}
	if config.CancelGrace == 0 {
		config.CancelGrace = CancelGrace
	}
	return &Driver{
		config: config, id: uuid.New(), sandboxes: sandboxes, store: st, events: ev, metrics: m,
		wake: make(chan struct{}, 1), following: map[uuid.UUID]*orders{},
	}
}

// Ask is the approval of an agent whose harness asks a person before it
// acts.
const Ask = "ask"

// HasHarness reports whether turns can run on the harness of that name.
func (d *Driver) HasHarness(name string) bool {
	_, found := d.config.Templates.Harnesses[name]
	return found
}

// Wake makes the driver look for queued turns to run. It is called when a
// turn was queued or a running one ended.
func (d *Driver) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
		// The driver has not yet acted on an earlier call, which covers this one.
	}
}

// Accept queues a message as the session's next turn, if the queue has
// room. The turn is of the user who sent the message, by, or of nobody. It
// returns the errors of store.CreateTurn.
func (d *Driver) Accept(ctx context.Context, sessionID, by uuid.UUID, text string) (store.Turn, error) {
	turn, waiting, err := d.store.CreateTurn(ctx, sessionID, by, text, d.config.MaxQueuedTurns, d.config.MaxConcurrentTurns)
	switch {
	case err == nil:
		d.metrics.Accepted(waiting)
	case errors.Is(err, store.ErrQueueFull):
		d.metrics.Refused("queue_full")
	case errors.Is(err, store.ErrSessionBusy):
		d.metrics.Refused("session_busy")
	}
	return turn, err
}

// EndSession ends the session and has its sandbox deleted, with all that
// the agent had in it. It returns the errors of store.EndSession. A session
// whose sandbox could not be deleted is ended all the same, and the error
// says so: ending it again deletes the sandbox.
func (d *Driver) EndSession(ctx context.Context, id uuid.UUID) (store.Session, error) {
	session, err := d.store.EndSession(ctx, id)
	if err != nil || session.Sandbox == "" {
		return session, err
	}
	if err := d.sandboxes.DeleteSandbox(ctx, session.Sandbox); err != nil {
		return session, fmt.Errorf("delete the sandbox of the session %s: %w", id, err)
	}
	if _, err := d.store.SetSandbox(ctx, id, session.Sandbox, ""); err != nil {
		return session, err
	}
	session.Sandbox = ""
	return session, nil
}

// QueueMoved tells the driver that a turn left the queue without running,
// so that it tells the turns behind it their new positions.
func (d *Driver) QueueMoved() {
	d.moved.Store(true)
	d.Wake()
}

// Run has the queued turns run and follows them, until ctx is done. It also
// sees the turns that were queued while no Supervisor was running, and
// those whose Supervisor is gone.
func (d *Driver) Run(ctx context.Context) {
	go d.events.WatchCancels(ctx, func(turnID uuid.UUID) {
		d.tell(turnID, func(told *orders) chan struct{} { return told.stop })
	})
	go d.events.WatchAnswers(ctx, func(turnID uuid.UUID) {
		d.tell(turnID, func(told *orders) chan struct{} { return told.answers })
	})
	ticker := time.NewTicker(recheck)
	defer ticker.Stop()
	sweeping := time.NewTicker(d.config.Sweep)
	defer sweeping.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-d.wake:
		case <-ticker.C:
		case <-sweeping.C:
			d.sweep(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// dispatch has the queued turns run that fit under the limit, oldest first.
func (d *Driver) dispatch(ctx context.Context) {
	expired := d.expire(ctx)
	dispatches, err := d.store.DispatchTurns(ctx, d.config.MaxConcurrentTurns)
	if err != nil {
		slog.ErrorContext(ctx, "dispatch turns", "error", err)
		return
	}
	for _, dispatch := range dispatches {
		d.metrics.Dispatched(dispatch.Harness, dispatch.Waited)
		// A turn that is not followed now is taken up by a sweep.
		d.follow(ctx, dispatch.TurnID)
	}
	if d.moved.Swap(false) || expired || len(dispatches) > 0 {
		d.announcePositions(ctx)
	}
}

// expire ends the turns that have waited to run for too long. It reports
// whether there were any.
func (d *Driver) expire(ctx context.Context) bool {
	if d.config.MaxQueueWait == 0 {
		return false
	}
	turns, err := d.store.ExpireTurns(ctx, d.config.MaxQueueWait)
	if err != nil {
		slog.ErrorContext(ctx, "expire turns", "error", err)
		return false
	}
	for _, turn := range turns {
		d.Ended(ctx, turn)
	}
	return len(turns) > 0
}

// announcePositions tells the turns that still wait their new positions in
// the queue, after the turns ahead of them were dispatched.
func (d *Driver) announcePositions(ctx context.Context) {
	err := d.store.AnnounceQueue(ctx, func(queued []uuid.UUID) error {
		for i, turnID := range queued {
			if err := d.events.AppendQueued(ctx, turnID, i+1); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "tell the queued turns their positions", "error", err)
	}
}

// reapEvery is how many sweeps pass between two looks for sandboxes that no
// session has.
const reapEvery = 6

// orphaned is how old a sandbox is before it is taken to be one that no
// session will have: until then it may be on its way to its session.
func (d *Driver) orphaned() time.Duration {
	return 30 * d.config.Sweep
}

// reap deletes the sandboxes of sessions that those sessions do not have: a
// Supervisor made one and was gone before it had told its session, or a
// session was ended and its sandbox could not be deleted then. A sandbox of
// a session that the records do not know is left alone.
func (d *Driver) reap(ctx context.Context) {
	sandboxes, err := d.sandboxes.Sandboxes(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list the sandboxes", "error", err)
		return
	}
	for _, made := range sandboxes {
		sessionID, err := uuid.Parse(made.Metadata[metadataSession])
		if err != nil || time.Since(made.CreatedAt) < d.orphaned() {
			continue
		}
		session, err := d.store.Session(ctx, sessionID)
		if err != nil || session.Sandbox == made.ID {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.ErrorContext(ctx, "read the session of a sandbox", "sandbox", made.ID, "error", err)
			}
			continue
		}
		slog.WarnContext(ctx, "delete a sandbox that its session does not have", "session", sessionID, "sandbox", made.ID)
		if err := d.sandboxes.DeleteSandbox(ctx, made.ID); err != nil {
			slog.ErrorContext(ctx, "delete a sandbox that its session does not have", "sandbox", made.ID, "error", err)
		}
	}
}

// lease is for how long a turn is this Supervisor's once it has said that
// it follows the turn. It says so again with every sweep.
func (d *Driver) lease() time.Duration {
	return 3 * d.config.Sweep
}

// sweep looks at the turns that run. Nothing of it is needed while every
// Supervisor stays and every announcement arrives.
func (d *Driver) sweep(ctx context.Context) {
	if d.sweeps++; d.sweeps%reapEvery == 0 {
		d.reap(ctx)
	}
	d.mu.Lock()
	mine := make(map[uuid.UUID]*orders, len(d.following))
	for turnID, told := range d.following {
		mine[turnID] = told
	}
	d.mu.Unlock()
	for turnID, told := range mine {
		still, err := d.events.Follow(ctx, turnID, d.id, d.lease())
		if err != nil {
			slog.ErrorContext(ctx, "say that the turn is followed", "turn", turnID, "error", err)
		} else if !still {
			// This Supervisor was taken to be gone. Two that follow a turn
			// record it once, and one is enough.
			slog.WarnContext(ctx, "another supervisor follows the turn", "turn", turnID)
			told.leave()
		}
	}
	turns, err := d.store.DispatchedTurns(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read dispatched turns", "error", err)
		return
	}
	for _, turn := range turns {
		d.follow(ctx, turn.ID)
		if turn.Cancelling > 0 {
			d.tell(turn.ID, func(told *orders) chan struct{} { return told.stop })
		}
	}
	answers, err := d.store.Answers(ctx, nil)
	if err != nil {
		slog.ErrorContext(ctx, "read the answers to requests for approval", "error", err)
		return
	}
	for _, answer := range answers {
		d.tell(answer.TurnID, func(told *orders) chan struct{} { return told.answers })
	}
}

// tell passes on what a turn is told, if this Supervisor follows the turn.
func (d *Driver) tell(turnID uuid.UUID, what func(*orders) chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if told, mine := d.following[turnID]; mine {
		select {
		case what(told) <- struct{}{}:
		default:
			// The one who follows the turn has not yet acted on an earlier call.
		}
	}
}

// follow has this Supervisor follow the turn, unless it does already or
// another Supervisor does.
func (d *Driver) follow(ctx context.Context, turnID uuid.UUID) {
	d.mu.Lock()
	_, mine := d.following[turnID]
	d.mu.Unlock()
	if mine {
		return
	}
	mine, err := d.events.Follow(ctx, turnID, d.id, d.lease())
	if err != nil {
		slog.ErrorContext(ctx, "say that the turn is followed", "turn", turnID, "error", err)
		return
	}
	if !mine {
		return
	}
	following, leave := context.WithCancel(ctx)
	told := &orders{leave: leave, stop: make(chan struct{}, 1), answers: make(chan struct{}, 1)}
	d.mu.Lock()
	d.following[turnID] = told
	d.mu.Unlock()
	go func() {
		defer func() {
			leave()
			d.mu.Lock()
			delete(d.following, turnID)
			d.mu.Unlock()
			// Whatever is left of the turn is the next Supervisor's that
			// asks, also when this one was told to stop.
			leaving, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			if err := d.events.Leave(leaving, turnID, d.id); err != nil {
				slog.ErrorContext(ctx, "say that the turn is followed no longer", "turn", turnID, "error", err)
			}
		}()
		d.run(following, turnID, told)
	}()
}

// end ends a turn and reports whether the turn is now ended.
func (d *Driver) end(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure) bool {
	turn, changed, err := d.store.EndTurn(ctx, turnID, succeeded, failure, func(ctx context.Context) ([]json.RawMessage, error) {
		return d.events.Items(ctx, turnID)
	})
	if err != nil {
		slog.ErrorContext(ctx, "end turn", "turn", turnID, "error", err)
		return false
	}
	if changed {
		d.Ended(ctx, turn)
		// The turn no longer counts against the limit.
		d.Wake()
	}
	return true
}

// Ended tells of a turn that has ended: its clients, with the last event of
// the turn, and the metrics.
func (d *Driver) Ended(ctx context.Context, turn store.Turn) {
	slog.InfoContext(ctx, "turn ended", "turn", turn.ID, "state", turn.State)
	// A client that misses this event gets it from the turn's record.
	if err := d.events.AppendLast(ctx, turn); err != nil {
		slog.ErrorContext(ctx, "publish turn event", "turn", turn.ID, "error", err)
	}
	harness, err := d.store.TurnHarness(ctx, turn.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read the harness of the turn", "turn", turn.ID, "error", err)
		return
	}
	d.metrics.Ended(harness, turn)
}

// environment is what the harness of the turn's session is started with.
func environment(turn store.FollowedTurn) map[string]string {
	told := map[string]string{}
	if turn.ProviderURL != "" {
		told["AVIARY_LLM_BASE_URL"] = strings.TrimSuffix(turn.ProviderURL, "/")
		told["AVIARY_LLM_MODEL"] = turn.Model
		// A sandbox holds no key of a model endpoint. The harness sends
		// this in its place.
		told["AVIARY_LLM_API_KEY"] = "none"
	}
	// The image of the harness reads this, and starts its harness so that
	// it asks.
	if turn.Approval != "" {
		told["AVIARY_APPROVAL"] = turn.Approval
	}
	return told
}

// lost reports whether a sandbox in that state will not run again.
func lost(state string) bool {
	switch state {
	case "Stopping", "Terminated", "Failed":
		return true
	}
	return false
}
