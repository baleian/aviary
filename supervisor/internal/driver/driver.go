// Package driver dispatches queued turns, runs each in the sandbox of its
// session and records the events that the sandbox's runner reports.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/url"
	"slices"
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
	// recheck is how often queued turns are dispatched without a Wake, which
	// retries a failed dispatch.
	recheck = 5 * time.Second
	// Sweep is the default interval between sweeps.
	Sweep = 10 * time.Second
	// CancelGrace is the default time that a cancelled turn has to stop by
	// itself before its sandbox is put to sleep.
	CancelGrace = 30 * time.Second
)

// Labels that identify the agent and project of a sandbox, besides its
// session label.
const (
	labelAgent   = "aviary.io/agent"
	labelProject = "aviary.io/project"
)

// Sandboxes manages the sandbox of each session. It is implemented by
// *sandbox.Client.
type Sandboxes interface {
	Create(ctx context.Context, request sandbox.Request) error
	Sandbox(ctx context.Context, name string) (sandbox.Sandbox, error)
	Sandboxes(ctx context.Context) ([]sandbox.Sandbox, error)
	Suspend(ctx context.Context, name, version string) error
	Resume(ctx context.Context, name string, pod sandbox.Pod) error
	Keep(ctx context.Context, name string, turn uuid.UUID, until time.Time) (sandbox.Sandbox, error)
	Delete(ctx context.Context, name string) error
}

type Config struct {
	// Harnesses are the harnesses that agents can use, by name.
	Harnesses map[string]sandbox.Harness
	// Models is the Egress Proxy's endpoint for model requests. Its path is
	// ignored: each turn uses the path of its provider's URL.
	Models *url.URL
	// Token returns the token that identifies a session to the Egress Proxy.
	Token func(session uuid.UUID) (string, error)
	// Idle is how long a session may go without a running turn before its
	// sandbox is put to sleep. Zero disables sleeping.
	Idle time.Duration
	// Retention is how long a sandbox is kept after a turn was last given to
	// it. After that it is deleted with its volume, and the session ends.
	// Zero keeps it until the session is ended.
	Retention time.Duration
	// MaxStartup limits the time from dispatch until the harness has the
	// message, which includes creating or waking the sandbox. Zero means no
	// limit.
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
	// Sweep and CancelGrace default to the constants of the same name.
	Sweep       time.Duration
	CancelGrace time.Duration
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
// sent it, or uuid.Nil if requests are not authenticated. It returns the
// errors of store.CreateTurn.
func (d *Driver) Accept(ctx context.Context, sessionID, by uuid.UUID, text string) (store.Turn, error) {
	turn, waiting, err := d.store.CreateTurn(ctx, sessionID, by, text, d.config.MaxQueuedTurns, d.config.MaxConcurrentTurns)
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

// EndSession ends the session and deletes its sandbox with its volume. It
// returns the errors of store.EndSession. If the sandbox cannot be deleted,
// the session still ends and the error is returned. The deletion is retried
// when the session is ended again, and at every look at the sandboxes.
func (d *Driver) EndSession(ctx context.Context, id uuid.UUID) (store.Session, error) {
	return d.endSession(ctx, id, "")
}

// endSession ends the session for the reason, as EndSession does.
func (d *Driver) endSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
	session, err := d.store.EndSession(ctx, id, reason)
	if err != nil || session.Sandbox == "" {
		return session, err
	}
	if err := d.discard(ctx, session); err != nil {
		return session, err
	}
	session.Sandbox, session.SandboxState = "", ""
	return session, nil
}

// DeleteAgent deletes the agent, which ends its sessions, and deletes their
// sandboxes with their volumes. It returns the errors of store.DeleteAgent. A
// sandbox that cannot be deleted is deleted at a later look at the sandboxes.
func (d *Driver) DeleteAgent(ctx context.Context, id uuid.UUID) (store.Agent, error) {
	agent, ended, err := d.store.DeleteAgent(ctx, id)
	if err != nil {
		return store.Agent{}, err
	}
	for _, session := range ended {
		if session.Sandbox == "" {
			continue
		}
		if err := d.discard(ctx, session); err != nil {
			slog.ErrorContext(ctx, "delete sandbox of ended session", "session", session.ID, "sandbox", session.Sandbox, "error", err)
		}
	}
	return agent, nil
}

// discard deletes the sandbox of a session that has ended, with its volume,
// and removes it from the session.
func (d *Driver) discard(ctx context.Context, session store.Session) error {
	if err := d.sandboxes.Delete(ctx, session.Sandbox); err != nil {
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
// have ended, and puts idle sessions to sleep. It also corrects the recorded
// state of a sandbox that something else has put to sleep, woken or deleted.
// Sandboxes of sessions that the store does not know are left alone.
func (d *Driver) look(ctx context.Context) {
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
	case made.State == sandbox.Lost:
		d.lose(ctx, session)
	default:
		d.correct(ctx, session, made)
		if d.config.Idle > 0 && (made.State == sandbox.Running || made.State == sandbox.Starting) {
			d.lull(ctx, session.ID, made)
		}
	}
}

// lose handles a sandbox that is gone. If the sandbox has expired, its
// session ends; otherwise the session records that the sandbox is lost. A
// session with a turn that has not ended is left to that turn, which finds
// the sandbox gone.
func (d *Driver) lose(ctx context.Context, session store.Session) {
	lost := session.SandboxState == store.SandboxLost
	if lost && d.config.Retention == 0 {
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
	expired := len(last) > 0 && d.stale(last[0])
	if lost && !expired {
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
	if expired {
		// A session that got a turn meanwhile is left to that turn.
		switch _, err := d.endSession(ctx, session.ID, store.EndExpired); {
		case err == nil:
			slog.InfoContext(ctx, "session expired", "session", session.ID, "sandbox", session.Sandbox)
		case !errors.Is(err, store.ErrSessionBusy):
			slog.ErrorContext(ctx, "end expired session", "session", session.ID, "error", err)
		}
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
	if err := d.sandboxes.Delete(ctx, made.Name); err != nil {
		slog.ErrorContext(ctx, "delete orphaned sandbox", "sandbox", made.Name, "error", err)
	}
}

// lull puts the session's sandbox to sleep if the session's last turn ended
// more than Idle ago. Only the version that was read is suspended: a turn
// changes the version with Keep before it is given to the sandbox, so it
// either prevents the suspension or finds the sandbox asleep and wakes it.
func (d *Driver) lull(ctx context.Context, sessionID uuid.UUID, made sandbox.Sandbox) {
	last, err := d.store.Turns(ctx, sessionID, math.MaxInt32, 1)
	if err != nil {
		slog.ErrorContext(ctx, "read last turn of session", "session", sessionID, "error", err)
		return
	}
	if len(last) > 0 && (last[0].EndedAt == nil || time.Since(*last[0].EndedAt) < d.config.Idle) {
		return
	}
	switch err := d.suspend(ctx, sessionID, made.Name, made.Version); {
	case err == nil:
		slog.InfoContext(ctx, "idle sandbox put to sleep", "session", sessionID, "sandbox", made.Name)
	case !errors.Is(err, sandbox.ErrChanged) && !errors.Is(err, sandbox.ErrNotFound):
		slog.ErrorContext(ctx, "put idle sandbox to sleep", "session", sessionID, "sandbox", made.Name, "error", err)
	}
}

// suspend puts the session's sandbox to sleep, if version is empty or still
// current, and records that it is asleep.
func (d *Driver) suspend(ctx context.Context, sessionID uuid.UUID, name, version string) error {
	if err := d.sandboxes.Suspend(ctx, name, version); err != nil {
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

// environment returns the environment variables of the turn's harness.
func (d *Driver) environment(turn store.FollowedTurn) map[string]string {
	// The Egress Proxy serves the provider's endpoint under the path of the
	// provider's URL, which the API validated.
	models := *d.config.Models
	if endpoint, err := url.Parse(turn.ProviderURL); err == nil {
		models.Path = strings.TrimSuffix(endpoint.Path, "/")
	}
	told := map[string]string{
		"AVIARY_LLM_BASE_URL": models.String(),
		"AVIARY_LLM_MODEL":    turn.Model,
	}
	if turn.Approval != "" {
		told["AVIARY_APPROVAL"] = turn.Approval
	}
	return told
}

// mcpServers returns the MCP servers that the turn's session has on. The
// harness reaches each of them at the model listener of the Egress Proxy,
// under the path of the server's URL.
func (d *Driver) mcpServers(ctx context.Context, turn store.FollowedTurn) ([]sandbox.McpServer, error) {
	on, err := d.store.SessionMcpServers(ctx, []uuid.UUID{turn.SessionID})
	if err != nil {
		return nil, fmt.Errorf("get the MCP servers of the session: %w", err)
	}
	var servers []sandbox.McpServer
	for _, id := range on[turn.SessionID] {
		server, err := d.store.McpServer(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get MCP server: %w", err)
		}
		// The API validated the URL.
		address, err := url.Parse(server.URL)
		if err != nil {
			continue
		}
		at := *d.config.Models
		at.Path, at.RawPath = address.Path, address.RawPath
		servers = append(servers, sandbox.McpServer{ID: server.ID, Name: server.Slug, Transport: server.Transport, URL: at.String()})
	}
	return servers, nil
}
