// Package driver dispatches queued turns, runs each in the sandbox of its
// session and records the events that the sandbox's runner reports.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

const (
	// recheck is how often queued turns are dispatched without a Wake, which
	// retries a failed dispatch.
	recheck = 5 * time.Second
	// Sweep is the default interval between sweeps.
	Sweep = 10 * time.Second
	// CancelGrace is the default time that a cancelled turn has to stop by
	// itself before its sandbox is stopped.
	CancelGrace = 30 * time.Second
	// UnsentFor is the default time for which an attachment is kept while no
	// message carries it.
	UnsentFor = time.Hour
)

// Sandboxes manages the sandbox of each session. It is implemented by
// *sandbox.Client.
type Sandboxes interface {
	Create(ctx context.Context, request sandbox.Request) error
	SetEgress(ctx context.Context, name string, egress sandbox.Egress) error
	Sandbox(ctx context.Context, name string) (sandbox.Sandbox, error)
	Sandboxes(ctx context.Context) ([]sandbox.Sandbox, error)
	Resume(ctx context.Context, name string) error
	Suspend(ctx context.Context, name string, durable bool) error
	Revert(ctx context.Context, name string) error
	Delete(ctx context.Context, name, uid string) error
	Draining(ctx context.Context) ([]string, error)
	Runner(name string) *sandbox.Runner
}

// Depot keeps the files that messages carry. It is implemented by
// *depot.Client.
type Depot interface {
	Put(ctx context.Context, key string, body io.Reader) (int64, error)
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
}

type Config struct {
	// Harnesses are the harnesses that agents can use, by name.
	Harnesses map[string]sandbox.Harness
	// Hosts are the hosts that every sandbox may reach, besides the model
	// provider and the MCP servers of its turn.
	Hosts []sandbox.Host
	// Localhost, if not empty, is the host that sandboxes reach in place of
	// the local host of the URL of a model provider or an MCP server.
	Localhost string
	// Halt stops what runs in a sandbox by reverting it to its last
	// snapshot, rather than by putting it to sleep. A sandbox whose snapshot
	// keeps its processes has to be stopped that way.
	Halt bool
	// Idle is how long a session may go without a running turn before its
	// sandbox is put to sleep. Zero disables sleeping.
	Idle time.Duration
	// Retention is how long a session is kept after its last message. After
	// that its sandbox is deleted, and the session ends. Zero keeps it until
	// the session is ended.
	Retention time.Duration
	// MaxStartup limits the time from dispatch until the harness has the
	// message, which includes creating or waking the sandbox, and waiting for
	// a worker with room for it. Zero means no limit.
	MaxStartup time.Duration
	// MaxTurn limits the time a turn runs after it is dispatched.
	MaxTurn time.Duration
	// MaxQueueWait limits the time a turn may be queued before it expires.
	// Zero means no limit.
	MaxQueueWait time.Duration
	// MaxQueuedTurns limits the number of queued turns. Further messages are
	// refused. Zero means no limit.
	MaxQueuedTurns int
	// MaxConcurrentTurns limits the number of running turns. Further turns
	// are queued in the order in which they are accepted.
	MaxConcurrentTurns int
	// Depot keeps the files that messages carry. Without it, messages carry
	// none.
	Depot Depot
	// MaxAttachmentBytes limits the size of a file that a message carries.
	MaxAttachmentBytes int64
	// MaxAttachments limits the number of files that a message carries.
	// Zero means that the installation takes none.
	MaxAttachments int
	// Sweep, CancelGrace and UnsentFor default to the constants of the same
	// name.
	Sweep       time.Duration
	CancelGrace time.Duration
	UnsentFor   time.Duration
}

type Driver struct {
	config Config
	// id identifies this Supervisor in the leases of the turns it follows.
	id        uuid.UUID
	sandboxes Sandboxes
	store     store.Store
	events    *events.Streams
	metrics   *metrics.Metrics
	wake      chan struct{}
	// moved is set when a turn leaves the queue without being dispatched.
	moved  atomic.Bool
	sweeps int

	mu        sync.Mutex
	following map[uuid.UUID]*orders
}

// orders signal the goroutine that follows a turn.
type orders struct {
	// leave stops following the turn.
	leave context.CancelFunc
	// stop is signaled when the turn is cancelled, and answers when one of
	// its approval requests may have been answered.
	stop, answers chan struct{}
}

// New returns a Driver that runs turns in sandboxes.
func New(config Config, sandboxes Sandboxes, st store.Store, ev *events.Streams, m *metrics.Metrics) *Driver {
	if config.Sweep == 0 {
		config.Sweep = Sweep
	}
	if config.CancelGrace == 0 {
		config.CancelGrace = CancelGrace
	}
	if config.UnsentFor == 0 {
		config.UnsentFor = UnsentFor
	}
	return &Driver{
		config: config, id: uuid.New(), sandboxes: sandboxes, store: st, events: ev, metrics: m,
		wake: make(chan struct{}, 1), following: map[uuid.UUID]*orders{},
	}
}

// Ask is the approval mode in which the harness asks a person before it
// acts.
const Ask = "ask"

// HasHarness reports whether the named harness is configured.
func (d *Driver) HasHarness(name string) bool {
	_, found := d.config.Harnesses[name]
	return found
}

// Harnesses returns the names of the configured harnesses, sorted.
func (d *Driver) Harnesses() []string {
	return slices.Sorted(maps.Keys(d.config.Harnesses))
}

// Wake has the driver dispatch queued turns soon. It does not block.
func (d *Driver) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
		// A pending wake-up covers this one.
	}
}

// Accept queues the message as the session's next turn. by is the user who
// sent it, or uuid.Nil if requests are not authenticated, and attachments
// are the files that it carries. It returns ErrTooManyAttachments if they
// are more than MaxAttachments, and the errors of store.CreateTurn.
func (d *Driver) Accept(ctx context.Context, sessionID, by uuid.UUID, text string, attachments []uuid.UUID) (store.Turn, error) {
	if len(attachments) > d.config.MaxAttachments {
		return store.Turn{}, ErrTooManyAttachments
	}
	turn, waiting, err := d.store.CreateTurn(ctx, sessionID, by, text, attachments, d.config.MaxQueuedTurns, d.config.MaxConcurrentTurns)
	switch {
	case err == nil:
		d.metrics.Accepted(waiting)
	case errors.Is(err, store.ErrQueueFull):
		d.metrics.Refused(metrics.QueueFull)
	case errors.Is(err, store.ErrSessionBusy):
		d.metrics.Refused(metrics.SessionBusy)
	}
	return turn, err
}

// EndSession ends the session and deletes its sandbox with its snapshot. It
// returns the errors of store.EndSession. If the sandbox cannot be deleted,
// the session still ends and the error is returned. The deletion is retried
// when the session is ended again, and at every look at the sandboxes.
func (d *Driver) EndSession(ctx context.Context, id uuid.UUID) (store.Session, error) {
	return d.endSession(ctx, id, "")
}

// endSession ends the session for the reason, as EndSession does.
func (d *Driver) endSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
	session, err := d.store.EndSession(ctx, id, reason)
	if err != nil {
		return session, err
	}
	if session.Sandbox != "" {
		if err := d.discard(ctx, session); err != nil {
			return session, err
		}
	}
	session.Sandbox, session.SandboxState = "", ""
	return session, nil
}

// DeleteAgent deletes the agent, which ends its sessions, and deletes their
// sandboxes. It returns the errors of store.DeleteAgent. What cannot be
// deleted is deleted at a later look at the sandboxes.
func (d *Driver) DeleteAgent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	agent, ended, err := d.store.DeleteAgent(ctx, id)
	if err != nil {
		return store.Agent{}, err
	}
	for _, session := range ended {
		if session.Sandbox != "" {
			if err := d.discard(ctx, session); err != nil {
				slog.ErrorContext(ctx, "delete sandbox of ended session", "session", session.ID, "sandbox", session.Sandbox, "error", err)
			}
		}
	}
	return agent, nil
}

// discard deletes the sandbox of a session that has ended, with its
// snapshot, and removes it from the session.
func (d *Driver) discard(ctx context.Context, session store.Session) error {
	if err := d.sandboxes.Delete(ctx, session.Sandbox, ""); err != nil {
		return fmt.Errorf("delete sandbox of session %s: %w", session.ID, err)
	}
	_, err := d.store.SetSandbox(ctx, session.ID, session.Sandbox, "")
	return err
}

// QueueMoved tells the driver that a turn left the queue without being
// dispatched, so that the turns behind it are told their new positions.
func (d *Driver) QueueMoved() {
	d.moved.Store(true)
	d.Wake()
}

// Run dispatches queued turns and follows them until ctx is done, including
// turns queued while no Supervisor ran. It also takes over turns whose
// Supervisor is gone.
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

func (d *Driver) dispatch(ctx context.Context) {
	expired := d.expire(ctx)
	dispatches, err := d.store.DispatchTurns(ctx, d.config.MaxConcurrentTurns)
	if err != nil {
		slog.ErrorContext(ctx, "dispatch turns", "error", err)
		return
	}
	for _, dispatch := range dispatches {
		d.metrics.Dispatched(dispatch.Harness, dispatch.Waited)
		// If following fails here, a sweep takes the turn over.
		d.follow(ctx, dispatch.TurnID)
	}
	if d.moved.Swap(false) || expired || len(dispatches) > 0 {
		d.announcePositions(ctx)
	}
}

// expire ends the turns that have been queued for longer than MaxQueueWait,
// and reports whether there were any.
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
		slog.ErrorContext(ctx, "announce queue positions", "error", err)
	}
}

// lookEvery is the number of sweeps per look at the sandboxes.
const lookEvery = 6

// orphaned is the age after which a sandbox that its session does not
// reference is deleted. A younger one may not be recorded for its session
// yet.
func (d *Driver) orphaned() time.Duration {
	return 30 * d.config.Sweep
}

// look compares the sandboxes with what the sessions record. It deletes the
// sandboxes that their sessions do not reference and those of sessions that
// have ended or expired, puts idle sessions to sleep, and reverts crashed
// sandboxes. It also corrects the recorded state of a sandbox that something
// else has put to sleep, woken or deleted, and puts to sleep the sandboxes
// whose workers go away. Sandboxes of sessions that the store does not know
// are left alone.
func (d *Driver) look(ctx context.Context) {
	d.evacuate(ctx)
	// The sessions are read first, so that a sandbox that is not listed is
	// gone, not new.
	sessions, err := d.store.SessionsWithSandbox(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list sessions with a sandbox", "error", err)
		return
	}
	sandboxes, err := d.sandboxes.Sandboxes(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list sandboxes", "error", err)
		return
	}
	recorded := make(map[string]store.Session, len(sessions))
	for _, session := range sessions {
		recorded[session.Sandbox] = session
	}
	for _, made := range sandboxes {
		session, found := recorded[made.Name]
		delete(recorded, made.Name)
		if !found {
			// The session got the sandbox after the sessions were read, or
			// does not reference it.
			sessionID, err := uuid.Parse(made.Session)
			if err != nil {
				continue
			}
			session, err = d.store.Session(ctx, sessionID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				continue
			case err != nil:
				slog.ErrorContext(ctx, "read sandbox session", "sandbox", made.Name, "error", err)
				continue
			case session.Sandbox != made.Name:
				d.reap(ctx, sessionID, made)
				continue
			}
		}
		d.tend(ctx, session, made)
	}
	for _, session := range recorded {
		d.tend(ctx, session, sandbox.Sandbox{Name: session.Sandbox, State: sandbox.Lost})
	}
	d.discardAttachments(ctx)
}

// evacuate puts to sleep, durably, the sandboxes whose workers are going
// away, so that they move on with their sessions. A sandbox whose snapshot
// keeps its processes continues its turn on its next worker.
func (d *Driver) evacuate(ctx context.Context) {
	names, err := d.sandboxes.Draining(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list sandboxes of workers that go away", "error", err)
		return
	}
	for _, name := range names {
		if err := d.sandboxes.Suspend(ctx, name, true); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			slog.ErrorContext(ctx, "put to sleep a sandbox whose worker goes away", "sandbox", name, "error", err)
			continue
		}
		slog.InfoContext(ctx, "sandbox put to sleep, since its worker goes away", "sandbox", name)
	}
}

// tend brings the session's sandbox and its recorded state in line. made is
// the sandbox as it was listed, lost if it was not.
func (d *Driver) tend(ctx context.Context, session store.Session, made sandbox.Sandbox) {
	switch {
	case session.EndedAt != nil:
		if err := d.discard(ctx, session); err != nil {
			slog.ErrorContext(ctx, "delete sandbox of ended session", "session", session.ID, "sandbox", made.Name, "error", err)
			return
		}
		slog.InfoContext(ctx, "sandbox of ended session deleted", "session", session.ID, "sandbox", made.Name)
	case d.outlived(ctx, session):
	case made.State == sandbox.Lost:
		d.lose(ctx, session)
	case made.State == sandbox.Crashed:
		d.recover(ctx, session, made)
	default:
		d.correct(ctx, session, made)
		if d.config.Idle > 0 && (made.State == sandbox.Running || made.State == sandbox.Starting) {
			d.lull(ctx, session.ID, made)
		}
	}
}

// outlived ends the session if its last message is older than Retention
// and it has no turn that has not ended. It reports whether it did.
func (d *Driver) outlived(ctx context.Context, session store.Session) bool {
	if d.config.Retention == 0 {
		return false
	}
	last, err := d.store.Turns(ctx, session.ID, math.MaxInt32, 1)
	if err != nil {
		slog.ErrorContext(ctx, "read last turn of session", "session", session.ID, "error", err)
		return false
	}
	if len(last) == 0 || last[0].EndedAt == nil || !d.stale(last[0]) {
		return false
	}
	// A session that got a turn meanwhile is left to that turn.
	switch _, err := d.endSession(ctx, session.ID, store.EndExpired); {
	case err == nil:
		slog.InfoContext(ctx, "session expired", "session", session.ID, "sandbox", session.Sandbox)
	case !errors.Is(err, store.ErrSessionBusy):
		slog.ErrorContext(ctx, "end expired session", "session", session.ID, "error", err)
	}
	return true
}

// recover reverts a sandbox that crashed while no turn ran in it, so that it
// sleeps as its last snapshot has it and the session's next turn wakes it. A
// session with a turn that has not ended is left to that turn.
func (d *Driver) recover(ctx context.Context, session store.Session, made sandbox.Sandbox) {
	last, err := d.store.Turns(ctx, session.ID, math.MaxInt32, 1)
	if err != nil {
		slog.ErrorContext(ctx, "read last turn of session", "session", session.ID, "error", err)
		return
	}
	if len(last) > 0 && last[0].EndedAt == nil {
		return
	}
	if err := d.sandboxes.Revert(ctx, made.Name); err != nil {
		slog.ErrorContext(ctx, "revert crashed sandbox", "session", session.ID, "sandbox", made.Name, "error", err)
		return
	}
	if err := d.store.SetSandboxState(ctx, session.ID, made.Name, store.SandboxAsleep); err != nil {
		slog.ErrorContext(ctx, "record sandbox state", "session", session.ID, "sandbox", made.Name, "error", err)
		return
	}
	slog.WarnContext(ctx, "crashed sandbox reverted to its last snapshot", "session", session.ID, "sandbox", made.Name, "crash", made.Message)
}

// lose records that the session's sandbox is lost. A session with a turn
// that has not ended is left to that turn, which finds the sandbox gone.
func (d *Driver) lose(ctx context.Context, session store.Session) {
	if session.SandboxState == store.SandboxLost {
		return
	}
	last, err := d.store.Turns(ctx, session.ID, math.MaxInt32, 1)
	if err != nil {
		slog.ErrorContext(ctx, "read last turn of session", "session", session.ID, "error", err)
		return
	}
	if len(last) > 0 && last[0].EndedAt == nil {
		return
	}
	// A turn may have created the sandbox again since the sandboxes were
	// listed.
	seen, err := d.sandboxes.Sandbox(ctx, session.Sandbox)
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
	case err != nil:
		slog.ErrorContext(ctx, "read sandbox", "session", session.ID, "sandbox", session.Sandbox, "error", err)
		return
	case seen.State != sandbox.Lost:
		return
	}
	if err := d.store.SetSandboxState(ctx, session.ID, session.Sandbox, store.SandboxLost); err != nil {
		slog.ErrorContext(ctx, "record sandbox state", "session", session.ID, "sandbox", session.Sandbox, "error", err)
		return
	}
	slog.WarnContext(ctx, "sandbox lost", "session", session.ID, "sandbox", session.Sandbox)
}

// reap deletes a sandbox that its session does not reference: a Supervisor
// stopped between creating it and recording it, or deleting it failed.
func (d *Driver) reap(ctx context.Context, sessionID uuid.UUID, made sandbox.Sandbox) {
	if time.Since(made.Created) < d.orphaned() {
		return
	}
	slog.WarnContext(ctx, "deleting orphaned sandbox", "session", sessionID, "sandbox", made.Name)
	// Only the sandbox that was listed: the session may have got another of
	// the name meanwhile.
	if err := d.sandboxes.Delete(ctx, made.Name, made.UID); err != nil {
		slog.ErrorContext(ctx, "delete orphaned sandbox", "sandbox", made.Name, "error", err)
	}
}

// lull puts the session's sandbox to sleep if the session's last turn ended
// more than Idle ago. A turn that is given to the sandbox right between the
// check and the suspension is stopped with it, unless the snapshot keeps the
// processes: the follower of the turn then wakes the sandbox again, and the
// turn goes on.
func (d *Driver) lull(ctx context.Context, sessionID uuid.UUID, made sandbox.Sandbox) {
	last, err := d.store.Turns(ctx, sessionID, math.MaxInt32, 1)
	if err != nil {
		slog.ErrorContext(ctx, "read last turn of session", "session", sessionID, "error", err)
		return
	}
	if len(last) > 0 && (last[0].EndedAt == nil || time.Since(*last[0].EndedAt) < d.config.Idle) {
		return
	}
	switch err := d.suspend(ctx, sessionID, made.Name); {
	case err == nil:
		slog.InfoContext(ctx, "idle sandbox put to sleep", "session", sessionID, "sandbox", made.Name)
	case !errors.Is(err, sandbox.ErrNotFound):
		slog.ErrorContext(ctx, "put idle sandbox to sleep", "session", sessionID, "sandbox", made.Name, "error", err)
	}
}

// suspend puts the session's sandbox to sleep, and records that it is
// asleep.
func (d *Driver) suspend(ctx context.Context, sessionID uuid.UUID, name string) error {
	if err := d.sandboxes.Suspend(ctx, name, false); err != nil {
		return err
	}
	return d.store.SetSandboxState(ctx, sessionID, name, store.SandboxAsleep)
}

// halt stops what runs in the session's sandbox, and records that it is
// asleep. A sandbox whose snapshot keeps its processes is reverted to its
// last snapshot: what it did since is lost. Any other is put to sleep, which
// ends its processes and keeps its files.
func (d *Driver) halt(ctx context.Context, sessionID uuid.UUID, name string) error {
	if !d.config.Halt {
		return d.suspend(ctx, sessionID, name)
	}
	if err := d.sandboxes.Revert(ctx, name); err != nil {
		return err
	}
	return d.store.SetSandboxState(ctx, sessionID, name, store.SandboxAsleep)
}

// stateOf returns whether the sandbox, as it was read, is awake or asleep, or
// "" if it is lost.
func stateOf(seen sandbox.Sandbox) string {
	switch seen.State {
	case sandbox.Asleep:
		return store.SandboxAsleep
	case sandbox.Running, sandbox.Starting:
		return store.SandboxAwake
	}
	return ""
}

// correct records the state of the session's sandbox if the session records
// another, as after something else has put the sandbox to sleep or woken it.
func (d *Driver) correct(ctx context.Context, session store.Session, made sandbox.Sandbox) {
	if state := stateOf(made); state == "" || state == session.SandboxState {
		return
	}
	// The session may record what happened after made was read.
	seen, err := d.sandboxes.Sandbox(ctx, made.Name)
	if err != nil {
		if !errors.Is(err, sandbox.ErrNotFound) {
			slog.ErrorContext(ctx, "read sandbox", "session", session.ID, "sandbox", made.Name, "error", err)
		}
		return
	}
	state := stateOf(seen)
	if state == "" || state == session.SandboxState {
		return
	}
	if err := d.store.SetSandboxState(ctx, session.ID, made.Name, state); err != nil {
		slog.ErrorContext(ctx, "record sandbox state", "session", session.ID, "sandbox", made.Name, "error", err)
		return
	}
	slog.InfoContext(ctx, "sandbox state corrected", "session", session.ID, "sandbox", made.Name, "state", state)
}

// lease is how long a turn stays this Supervisor's after it claims the turn.
// Every sweep renews the claim.
func (d *Driver) lease() time.Duration {
	return 3 * d.config.Sweep
}

// sweep renews the leases of the turns that this Supervisor follows. It also
// recovers from Supervisors that are gone and from missed announcements: it
// takes over dispatched turns that nobody follows, and signals cancellations
// and answers again.
func (d *Driver) sweep(ctx context.Context) {
	if d.sweeps++; d.sweeps%lookEvery == 0 {
		d.look(ctx)
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
			slog.ErrorContext(ctx, "renew turn lease", "turn", turnID, "error", err)
		} else if !still {
			// The lease lapsed and another Supervisor took the turn over.
			// Events are recorded once even while both follow it, so leaving
			// is enough.
			slog.WarnContext(ctx, "turn taken over by another supervisor", "turn", turnID)
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
		slog.ErrorContext(ctx, "read approval answers", "error", err)
		return
	}
	for _, answer := range answers {
		d.tell(answer.TurnID, func(told *orders) chan struct{} { return told.answers })
	}
}

// tell signals the channel that what selects, if this Supervisor follows the
// turn.
func (d *Driver) tell(turnID uuid.UUID, what func(*orders) chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if told, mine := d.following[turnID]; mine {
		select {
		case what(told) <- struct{}{}:
		default:
			// A pending signal covers this one.
		}
	}
}

// follow starts following the turn, unless this or another Supervisor
// already does.
func (d *Driver) follow(ctx context.Context, turnID uuid.UUID) {
	d.mu.Lock()
	_, mine := d.following[turnID]
	d.mu.Unlock()
	if mine {
		return
	}
	mine, err := d.events.Follow(ctx, turnID, d.id, d.lease())
	if err != nil {
		slog.ErrorContext(ctx, "claim turn lease", "turn", turnID, "error", err)
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
			// Release the lease even if ctx is done, so that another
			// Supervisor can take the turn over at once.
			leaving, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			if err := d.events.Leave(leaving, turnID, d.id); err != nil {
				slog.ErrorContext(ctx, "release turn lease", "turn", turnID, "error", err)
			}
		}()
		d.run(following, turnID, told)
	}()
}

// end ends the turn and reports whether it has ended, by this call or an
// earlier one.
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
		// The ended turn frees a slot under MaxConcurrentTurns.
		d.Wake()
	}
	return true
}

// Ended publishes the last event of a turn that has ended, and records the
// turn in the metrics.
func (d *Driver) Ended(ctx context.Context, turn store.Turn) {
	slog.InfoContext(ctx, "turn ended", "turn", turn.ID, "state", turn.State)
	// If this fails, clients get the event from the turn's record.
	if err := d.events.AppendLast(ctx, turn); err != nil {
		slog.ErrorContext(ctx, "publish turn event", "turn", turn.ID, "error", err)
	}
	harness, err := d.store.TurnHarness(ctx, turn.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read turn harness", "turn", turn.ID, "error", err)
		return
	}
	d.metrics.Ended(harness, turn)
}

// setup is what the turn's harness is given, and what its sandbox may
// reach.
type setup struct {
	environment map[string]string
	servers     []sandbox.McpServer
	egress      sandbox.Egress
}

// sender names who sends a header to a host: the turn's provider, or an MCP
// server by its ID. The egress gateway puts a secret into the header of every
// request to its host, so a header with a secret has one sender.
type sender struct {
	sandbox.Host
	header string
}

// senders are the headers that are sent to each host, and whether a secret
// goes into them.
type senders map[sender]struct {
	by     string
	secret bool
}

// clash reports whether one of the headers clashes with those of another
// sender to the host: one of them, or the other's, has a secret.
func (s senders) clash(host sandbox.Host, by string, headers []store.Header) bool {
	for _, header := range headers {
		sent, found := s[sender{host, strings.ToLower(header.Name)}]
		if found && sent.by != by && (sent.secret || credentials.Secret(header)) {
			return true
		}
	}
	return false
}

func (s senders) add(host sandbox.Host, by, header string, secret bool) {
	s[sender{host, strings.ToLower(header)}] = struct {
		by     string
		secret bool
	}{by, secret}
}

// setUp returns what the turn's harness is given: the model of the turn's
// agent at its provider's URL, with placeholders in place of the provider's
// secrets, and the MCP servers that the session has on, with placeholders
// in place of theirs. The sandbox may reach the installation's hosts, the
// provider and the servers, and the egress gateway puts the secrets into
// their requests. A server whose headers clash with those of the provider or
// of another server at its host is left out.
func (d *Driver) setUp(ctx context.Context, turn store.FollowedTurn) (setup, error) {
	agent, err := d.store.Agent(ctx, turn.AgentID)
	if err != nil {
		return setup{}, fmt.Errorf("get the turn's agent: %w", err)
	}
	provider, err := d.store.Provider(ctx, agent.Model.Provider)
	if err != nil {
		return setup{}, fmt.Errorf("get the model provider of the turn's agent: %w", err)
	}
	address, err := d.reached(provider.URL)
	if err != nil {
		return setup{}, fmt.Errorf("the model provider's URL: %w", err)
	}
	host, err := sandbox.HostOf(address)
	if err != nil {
		return setup{}, err
	}
	set := setup{egress: sandbox.Egress{Hosts: slices.Clone(d.config.Hosts)}}
	sent := senders{}
	headers := map[string]string{}
	var secrets []sandbox.Secret
	if provider.APIKey != nil || provider.APIKeyAt != nil {
		secrets = append(secrets, sandbox.Secret{Host: host, Header: "authorization", Prefix: "Bearer ", URI: credentials.ProviderKey(provider.ID)})
		sent.add(host, "", "authorization", true)
	}
	for _, header := range provider.Headers {
		sent.add(host, "", header.Name, credentials.Secret(header))
		if !credentials.Secret(header) {
			headers[header.Name] = header.Value
			continue
		}
		headers[header.Name] = credentials.Placeholder
		secrets = append(secrets, sandbox.Secret{Host: host, Header: header.Name, URI: credentials.ProviderHeader(provider.ID, header.Name)})
	}
	if len(secrets) == 0 {
		set.egress.Hosts = append(set.egress.Hosts, host)
	}
	set.egress.Secrets = secrets
	encoded, err := json.Marshal(headers)
	if err != nil {
		return setup{}, err
	}
	set.environment = map[string]string{
		"AVIARY_LLM_BASE_URL": strings.TrimSuffix(address, "/"),
		"AVIARY_LLM_MODEL":    turn.Model,
		// The egress gateway puts the provider's key in its place.
		"AVIARY_LLM_API_KEY": credentials.Placeholder,
		"AVIARY_LLM_HEADERS": string(encoded),
	}
	if turn.Approval != "" {
		set.environment["AVIARY_APPROVAL"] = turn.Approval
	}
	on, err := d.store.SessionMcpServers(ctx, []uuid.UUID{turn.SessionID})
	if err != nil {
		return setup{}, fmt.Errorf("get the MCP servers of the session: %w", err)
	}
	for _, id := range on[turn.SessionID] {
		server, err := d.store.McpServer(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return setup{}, fmt.Errorf("get MCP server: %w", err)
		}
		address, err := d.reached(server.URL)
		if err != nil {
			continue
		}
		host, err := sandbox.HostOf(address)
		if err != nil {
			slog.WarnContext(ctx, "MCP server left out of a turn", "server", server.ID, "error", err)
			continue
		}
		if sent.clash(host, server.ID.String(), server.Headers) {
			slog.WarnContext(ctx, "MCP server left out of a turn: a header of its host has a secret of another sender",
				"server", server.ID, "host", host.Name)
			continue
		}
		given := sandbox.McpServer{ID: server.ID, Name: server.Slug, Transport: server.Transport, URL: address}
		secret := false
		for _, header := range server.Headers {
			sent.add(host, server.ID.String(), header.Name, credentials.Secret(header))
			if !credentials.Secret(header) {
				given.Headers = append(given.Headers, sandbox.McpHeader{Name: header.Name, Value: header.Value})
				continue
			}
			secret = true
			given.Headers = append(given.Headers, sandbox.McpHeader{Name: header.Name, Value: credentials.Placeholder})
			set.egress.Secrets = append(set.egress.Secrets, sandbox.Secret{Host: host, Header: header.Name, URI: credentials.McpServerHeader(server.ID, header.Name)})
		}
		if !secret {
			set.egress.Hosts = append(set.egress.Hosts, host)
		}
		set.servers = append(set.servers, given)
	}
	return set, nil
}

// reached returns the URL as a sandbox reaches it: with the host that stands
// in for the local host, if the URL names the local host.
func (d *Driver) reached(address string) (string, error) {
	at, err := url.Parse(address)
	if err != nil {
		return "", err
	}
	if d.config.Localhost == "" || !local(at.Hostname()) {
		return address, nil
	}
	host := d.config.Localhost
	if port := at.Port(); port != "" {
		host += ":" + port
	}
	at.Host = host
	return at.String(), nil
}

// local reports whether the host is the local host: by that name, or as a
// loopback address.
func local(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}
