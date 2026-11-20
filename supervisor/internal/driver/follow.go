package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

// run is the state of a turn that this Supervisor follows.
type run struct {
	turn       store.FollowedTurn
	told       *orders
	dispatched time.Time
	// changed is signaled when the deadline moves, and asked when the turn
	// is cancelled.
	changed, asked chan struct{}

	mu      sync.Mutex
	sandbox string
	// runner is set once the runner has the turn. given is set once the turn
	// may have been given to the runner, by this or an earlier Supervisor.
	runner *sandbox.Runner
	given  bool
	// started is set once the harness has the message.
	started bool
	// stopping is when the turn was cancelled, or zero.
	stopping time.Time
	// holdup is why the turn has not reached its runner yet.
	holdup string
	// givenUp is why the turn was given up, if it was.
	givenUp *store.Failure
}

// again is the interval between attempts to reach the runner.
func (d *Driver) again() time.Duration {
	return d.config.Sweep / 10
}

// run follows the turn: it gets the session's sandbox running, gives the
// turn to its runner and records the runner's events until the turn ends.
// If ctx is done or the turn is left before that, the next Supervisor to
// follow the turn continues from what was recorded.
func (d *Driver) run(ctx context.Context, turnID uuid.UUID, told *orders) {
	turn, err := d.store.FollowTurn(ctx, turnID)
	switch {
	case errors.Is(err, store.ErrTurnEnded), errors.Is(err, store.ErrNotFound):
		return
	case err != nil:
		slog.ErrorContext(ctx, "read turn to follow", "turn", turnID, "error", err)
		return
	}
	received, err := d.events.Received(ctx, turnID)
	if err != nil {
		slog.ErrorContext(ctx, "read last received event", "turn", turnID, "error", err)
		return
	}
	slog.InfoContext(ctx, "following turn", "turn", turnID, "received", received)
	defer d.metrics.Following(turn.Harness)()

	// A turn with recorded events was given to its runner by an earlier
	// Supervisor. It is never given again, since a runner that has lost it
	// would run it a second time.
	given := received > 0 || turn.StartedAt != nil
	r := &run{
		turn: turn, told: told, dispatched: time.Now().Add(-turn.Dispatched),
		changed: make(chan struct{}, 1), asked: make(chan struct{}, 1),
		sandbox: turn.Sandbox, given: given, started: turn.StartedAt != nil,
	}
	if turn.CancelRequestedAt != nil {
		r.stopping = *turn.CancelRequestedAt
	}
	// following is cancelled when the turn is given up. Ending the turn uses
	// ctx, so that it still happens then.
	following, leave := context.WithCancel(ctx)
	defer leave()
	go d.watch(following, leave, r)

	if runner := d.reach(ctx, following, r, given); runner != nil {
		d.read(ctx, following, r, runner, received)
	}
	if failure := r.failure(); failure != nil {
		d.giveUp(ctx, r, *failure)
	}
}

// watch passes cancellations and answers on to the runner, and gives the
// turn up when it passes its deadline.
func (d *Driver) watch(following context.Context, leave context.CancelFunc, r *run) {
	for {
		at, _ := r.limit(d.config)
		timer := time.NewTimer(time.Until(at))
		select {
		case <-following.Done():
			timer.Stop()
			return
		case <-timer.C:
			// The deadline may have moved since the timer was set.
			if at, failure := r.limit(d.config); !time.Now().Before(at) {
				r.giveUp(failure)
				leave()
				return
			}
		case <-r.changed:
		case <-r.told.stop:
			// If this fails, the next sweep signals stop again.
			if runner := r.stop(); runner != nil {
				err := runner.Cancel(following, r.turn.ID)
				if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
					slog.WarnContext(following, "cancel turn on runner", "turn", r.turn.ID, "error", err)
				}
			}
		case <-r.told.answers:
			d.answer(following, r)
		}
		timer.Stop()
	}
}

// answer passes the answers that the harness has not received yet on to the
// runner. Repeating an answer is harmless: the runner ignores answers that no
// request waits for.
func (d *Driver) answer(ctx context.Context, r *run) {
	runner := r.reached()
	if runner == nil {
		return
	}
	answers, err := d.store.Answers(ctx, &r.turn.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read approval answers", "turn", r.turn.ID, "error", err)
		return
	}
	if len(answers) == 0 {
		return
	}
	for _, answer := range answers {
		err := runner.Answer(ctx, r.turn.ID, answer.ID, answer.OptionID)
		if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			slog.WarnContext(ctx, "send approval answer to runner", "turn", r.turn.ID, "error", err)
		}
	}
}

// reach gets the session's sandbox running and gives the turn to its runner,
// unless the turn was given before. It returns the runner, or nil if the
// turn is not to be followed further.
func (d *Driver) reach(ctx, following context.Context, r *run, given bool) *sandbox.Runner {
	for following.Err() == nil {
		// A turn that is cancelled before its runner has it never runs.
		if !given && r.stopped() {
			d.end(ctx, r.turn.ID, false, store.Failure{})
			return nil
		}
		runner, err := d.attempt(ctx, following, r, given)
		if runner != nil {
			r.reach(runner)
			return runner
		}
		if r.failure() != nil {
			return nil
		}
		if err != nil {
			r.hold(err.Error())
		}
		select {
		case <-time.After(d.again()):
		case <-r.asked:
		case <-following.Done():
		}
	}
	return nil
}

// attempt is a single try of reach. Its error says what the turn waits for.
func (d *Driver) attempt(ctx, following context.Context, r *run, given bool) (*sandbox.Runner, error) {
	turn := r.turn
	id := r.sandboxID()
	if id == "" && given {
		r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the session's sandbox is gone"})
		return nil, nil
	}
	if id == "" {
		var err error
		if id, err = d.provide(ctx, r); err != nil {
			return nil, fmt.Errorf("create sandbox: %w", err)
		}
	}
	seen, err := d.sandboxes.Sandbox(following, id)
	switch {
	case errors.Is(err, sandbox.ErrNotFound), err == nil && seen.State == sandbox.Lost:
		if given {
			r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the session's sandbox is gone"})
			return nil, nil
		}
		// The sandbox was lost after the session's last turn, or was never
		// created.
		gone := "the session's sandbox is gone"
		if err == nil {
			gone = "the session's sandbox is lost: " + seen.Message
		}
		slog.WarnContext(ctx, gone, "session", turn.SessionID, "sandbox", id)
		d.forget(ctx, turn.SessionID, id)
		r.keep("")
		if d.expired(ctx, turn) {
			r.giveUp(store.Failure{
				Reason:  store.FailureSessionExpired,
				Message: fmt.Sprintf("the session was not used for longer than %s, and has ended", d.config.Retention),
			})
			return nil, nil
		}
		// The next attempt creates a new one.
		return nil, errors.New(gone + "; creating a new one")
	case err != nil:
		return nil, fmt.Errorf("get sandbox: %w", err)
	case seen.State == sandbox.Asleep && given:
		r.giveUp(asleep)
		return nil, nil
	case seen.State == sandbox.Asleep:
		pod, err := d.pod(turn)
		if err != nil {
			return nil, err
		}
		if err := d.sandboxes.Resume(following, id, pod); err != nil {
			return nil, fmt.Errorf("wake sandbox: %w", err)
		}
		return nil, errors.New("waking the session's sandbox")
	case seen.State != sandbox.Running:
		why := "the session's sandbox is not running yet"
		if seen.Message != "" {
			why += ": " + seen.Message
		}
		return nil, errors.New(why)
	}
	runner := sandbox.RunnerAt(seen.Address)
	if given {
		return runner, nil
	}
	// Retention counts from the last turn given to the sandbox.
	var until time.Time
	if d.config.Retention > 0 {
		until = time.Now().Add(d.config.Retention)
	}
	kept, err := d.sandboxes.Keep(following, id, turn.ID, until)
	if err != nil {
		return nil, fmt.Errorf("keep sandbox: %w", err)
	}
	// The sandbox was put to sleep for being idle after it was read above,
	// and the turn would stop with its pod. The next attempt wakes it.
	if kept.State == sandbox.Asleep {
		return nil, errors.New("the session's sandbox was put to sleep; waking it")
	}
	// The runner may get the turn even if the request fails.
	r.give()
	err = runner.Start(following, turn.ID, sandbox.Turn{
		Text: turn.Text, Instructions: turn.Instructions, Approval: turn.Approval, Environment: d.environment(turn),
	})
	if errors.Is(err, sandbox.ErrBusy) {
		// The runner still runs another turn, which the store does not
		// consider running. Giving up puts the sandbox to sleep, which stops
		// it.
		r.giveUp(store.Failure{Reason: store.FailureRunner, Message: "the session's runner is running another turn"})
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("start turn: %w", err)
	}
	return runner, nil
}

// expired reports whether the session's previous turn is older than
// Retention. A missing sandbox has then expired, and the session has ended.
func (d *Driver) expired(ctx context.Context, turn store.FollowedTurn) bool {
	if d.config.Retention == 0 {
		return false
	}
	before, err := d.store.Turns(ctx, turn.SessionID, turn.Seq, 1)
	if err != nil || len(before) == 0 {
		return false
	}
	last := before[0].CreatedAt
	if before[0].StartedAt != nil {
		last = *before[0].StartedAt
	}
	return time.Since(last) >= d.config.Retention
}

// pod returns the pod of the session's sandbox, with the harness as
// currently configured.
func (d *Driver) pod(turn store.FollowedTurn) (sandbox.Pod, error) {
	harness, found := d.config.Harnesses[turn.Harness]
	if !found {
		return sandbox.Pod{}, fmt.Errorf("harness %s is not configured", turn.Harness)
	}
	token, err := d.config.Token(turn.SessionID)
	if err != nil {
		return sandbox.Pod{}, fmt.Errorf("create session token: %w", err)
	}
	return sandbox.Pod{Harness: harness, Token: token}, nil
}

// provide creates the session's sandbox and returns its name.
func (d *Driver) provide(ctx context.Context, r *run) (string, error) {
	turn := r.turn
	pod, err := d.pod(turn)
	if err != nil {
		return "", err
	}
	err = d.sandboxes.Create(ctx, sandbox.Request{
		Session: turn.SessionID,
		Labels:  map[string]string{labelAgent: turn.AgentID.String(), labelProject: turn.ProjectID.String()},
		Pod:     pod,
		Kept:    d.config.Retention,
	})
	if err != nil {
		return "", err
	}
	// The name derives from the session, so Supervisors that create the
	// sandbox at the same time create the same one.
	name := sandbox.Name(turn.SessionID)
	if _, err := d.store.SetSandbox(ctx, turn.SessionID, "", name); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "sandbox created", "session", turn.SessionID, "sandbox", name)
	r.keep(name)
	return name, nil
}

// forget deletes the sandbox and removes it from the session.
func (d *Driver) forget(ctx context.Context, sessionID uuid.UUID, id string) {
	if err := d.sandboxes.Delete(ctx, id); err != nil {
		slog.ErrorContext(ctx, "delete sandbox", "session", sessionID, "sandbox", id, "error", err)
	}
	if _, err := d.store.SetSandbox(ctx, sessionID, id, ""); err != nil {
		slog.ErrorContext(ctx, "clear session sandbox", "session", sessionID, "sandbox", id, "error", err)
	}
}

// giveUp ends the turn as failed. If the runner may have the turn, the
// sandbox is put to sleep: this stops the turn with the pod, and keeps the
// session's volume for the next turn, which wakes the sandbox. A sandbox
// that the turn has not reached, such as one still being created or woken,
// is left alone.
func (d *Driver) giveUp(ctx context.Context, r *run, failure store.Failure) {
	slog.WarnContext(ctx, "giving up turn", "turn", r.turn.ID, "reason", failure.Reason, "message", failure.Message)
	if id := r.sandboxID(); id != "" && r.wasGiven() {
		err := d.sandboxes.Suspend(ctx, id, "")
		if errors.Is(err, sandbox.ErrNotFound) {
			// The next turn creates a new sandbox.
			_, err = d.store.SetSandbox(ctx, r.turn.SessionID, id, "")
		}
		// Leave the turn unended; a later sweep follows it again and retries.
		if err != nil {
			slog.ErrorContext(ctx, "put sandbox to sleep", "session", r.turn.SessionID, "sandbox", id, "error", err)
			return
		}
	}
	if d.end(ctx, r.turn.ID, false, failure) && failure.Reason == store.FailureSessionExpired {
		if _, err := d.store.EndSession(ctx, r.turn.SessionID); err != nil {
			slog.ErrorContext(ctx, "end session", "session", r.turn.SessionID, "error", err)
		}
	}
}

// read records the runner's events of the turn after the event numbered
// after, until the turn ends.
func (d *Driver) read(ctx, following context.Context, r *run, runner *sandbox.Runner, after uint64) {
	turn := r.turn
	for following.Err() == nil {
		var ended *ending
		err := runner.Events(following, turn.ID, after, func(event sandbox.Event) error {
			how, err := d.record(following, r, event)
			if err != nil {
				return err
			}
			if after, ended = event.Seq, how; ended != nil {
				return errEnded
			}
			return nil
		})
		switch {
		case ended != nil:
			d.end(ctx, turn.ID, ended.succeeded, ended.failure)
			return
		case following.Err() != nil:
			return
		case errors.Is(err, sandbox.ErrNotFound):
			d.end(ctx, turn.ID, false, store.Failure{
				Reason: store.FailureRunner, Message: "the session's runner no longer has the turn",
			})
			return
		case err != nil:
			slog.WarnContext(ctx, "read turn events", "turn", turn.ID, "after", after, "error", err)
		}
		// The stream was cut off, or the runner cannot be reached.
		seen, gone := d.sandboxes.Sandbox(following, r.sandboxID())
		switch {
		case errors.Is(gone, sandbox.ErrNotFound), gone == nil && seen.State == sandbox.Lost:
			r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the session's sandbox is gone"})
			return
		case gone == nil && seen.State == sandbox.Asleep:
			r.giveUp(asleep)
			return
		case gone == nil && seen.State == sandbox.Running && seen.Address != runner.Address():
			// The pod was recreated. Its runner does not have the turn, which
			// the next read reports.
			runner = sandbox.RunnerAt(seen.Address)
			r.reach(runner)
		}
		select {
		case <-time.After(d.again()):
		case <-following.Done():
		}
		// Another Supervisor may have recorded more of the turn.
		if recorded, err := d.events.Received(following, turn.ID); err == nil && recorded > after {
			after = recorded
		}
	}
}

// errEnded stops reading the events of a turn that has ended.
var errEnded = errors.New("the turn has ended")

// asleep is the failure of a turn whose sandbox was put to sleep while the
// runner had the turn.
var asleep = store.Failure{Reason: store.FailureRunner, Message: "the session's sandbox was put to sleep while the turn was running"}

// ending is how a turn ended, as reported by its runner.
type ending struct {
	succeeded bool
	failure   store.Failure
}

// passed are the runner events that are passed on to clients. Since the
// sandbox is not trusted, no other event of the runner is.
var passed = map[string]bool{
	"message.delta":      true,
	"thought.delta":      true,
	"tool.call":          true,
	"tool.update":        true,
	"plan.update":        true,
	"approval.requested": true,
	"approval.resolved":  true,
}

// record records an event of the turn's runner. The event may have been
// recorded already, by this or another Supervisor. record returns how the
// turn ended, if the event ends it.
func (d *Driver) record(ctx context.Context, r *run, event sandbox.Event) (*ending, error) {
	turn := r.turn
	wrong := &ending{failure: store.Failure{
		Reason: store.FailureRunner, Message: fmt.Sprintf("the runner sent an invalid %q event", event.Name),
	}}
	data := event.Data
	switch event.Name {
	case "started":
		startup, first, err := d.store.StartTurn(ctx, turn.ID)
		if err != nil {
			return nil, err
		}
		if first {
			d.metrics.Started(turn.Harness, startup)
		}
		r.start()
		return nil, d.events.Receive(ctx, turn.ID, event.Seq, events.TurnStarted, struct{}{})
	case "finished":
		var told struct {
			StopReason string       `json:"stop_reason"`
			Usage      *store.Usage `json:"usage"`
		}
		if json.Unmarshal(data, &told) != nil || told.StopReason == "" {
			return wrong, nil
		}
		if err := d.store.SetFinished(ctx, turn.ID, told.StopReason, told.Usage); err != nil {
			return nil, err
		}
		return &ending{succeeded: true}, d.events.Receive(ctx, turn.ID, event.Seq, "", nil)
	case "failed":
		var failure store.Failure
		if json.Unmarshal(data, &failure) != nil {
			return wrong, nil
		}
		switch failure.Reason {
		case store.FailureHarness, store.FailureResume, store.FailureRunner:
		default:
			failure.Reason = store.FailureRunner
		}
		return &ending{failure: failure}, d.events.Receive(ctx, turn.ID, event.Seq, "", nil)
	case "approval.requested":
		// Stored for the person who answers it, and also passed on as an
		// event.
		var asked struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &asked) != nil || asked.ID == "" {
			return wrong, nil
		}
		if err := d.store.AddApproval(ctx, turn.ID, asked.ID, data); err != nil {
			return nil, err
		}
	case "approval.resolved":
		var resolved struct {
			ID       string `json:"id"`
			Outcome  string `json:"outcome"`
			OptionID string `json:"option_id"`
		}
		if json.Unmarshal(data, &resolved) != nil || resolved.ID == "" {
			return wrong, nil
		}
		asked, waited, first, err := d.store.ResolveApproval(ctx, turn.ID, resolved.ID, resolved.Outcome)
		if err != nil {
			return nil, err
		}
		outcome := resolved.Outcome
		if outcome == "selected" {
			// Add who chose the option, which the runner does not know.
			kept, err := d.store.Approval(ctx, turn.ID, resolved.ID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			if kept.AnsweredBy != uuid.Nil {
				if data, err = with(data, "answered_by", kept.AnsweredBy); err != nil {
					return nil, err
				}
			}
			outcome = asked.OptionKind(resolved.OptionID)
		}
		if first {
			d.metrics.Approved(turn.Harness, outcome, waited)
		}
	}
	if !passed[event.Name] {
		return nil, d.events.Receive(ctx, turn.ID, event.Seq, "", nil)
	}
	return nil, d.events.Receive(ctx, turn.ID, event.Seq, event.Name, data)
}

// with returns the JSON object with the field added.
func with(object []byte, name string, value any) ([]byte, error) {
	fields := map[string]any{}
	if err := json.Unmarshal(object, &fields); err != nil {
		return nil, err
	}
	fields[name] = value
	return json.Marshal(fields)
}

// limit returns the turn's deadline, and the failure to report if the turn
// has not ended by then.
func (r *run) limit(config Config) (time.Time, store.Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Time spent waiting for approval counts too, since the sandbox runs
	// meanwhile.
	at := r.dispatched.Add(config.MaxTurn)
	failure := store.Failure{
		Reason: store.FailureTimedOut, Message: fmt.Sprintf("the turn ran for longer than %s", config.MaxTurn),
	}
	if startup := r.dispatched.Add(config.MaxStartup); !r.started && config.MaxStartup > 0 && startup.Before(at) {
		at = startup
		failure = store.Failure{Reason: store.FailureStartup, Message: r.holdup}
		if failure.Message == "" {
			failure.Message = "the harness did not start the turn"
		}
	}
	// The store records a cancelled turn as cancelled, whatever the failure.
	if stopped := r.stopping.Add(config.CancelGrace); !r.stopping.IsZero() && stopped.Before(at) {
		at = stopped
		failure = store.Failure{Reason: store.FailureRunner, Message: "the turn did not stop after it was cancelled"}
	}
	return at, failure
}

func (r *run) sandboxID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sandbox
}

// keep records the name of the session's sandbox.
func (r *run) keep(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sandbox = id
}

// hold records why the turn has not reached its runner yet.
func (r *run) hold(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holdup = why
}

// reach records that the runner has the turn, and signals any cancellation
// and answers that came before.
func (r *run) reach(runner *sandbox.Runner) {
	r.mu.Lock()
	r.runner = runner
	stopping := !r.stopping.IsZero()
	r.mu.Unlock()
	if stopping {
		poke(r.told.stop)
	}
	poke(r.told.answers)
}

func (r *run) reached() *sandbox.Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runner
}

// give records that the runner may have the turn.
func (r *run) give() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.given = true
}

func (r *run) wasGiven() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.given
}

// start records that the harness has the message, which moves the deadline.
func (r *run) start() {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	poke(r.changed)
}

// stop records that the turn was cancelled, and returns the runner to tell,
// if one has the turn.
func (r *run) stop() *sandbox.Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping.IsZero() {
		r.stopping = time.Now()
	}
	poke(r.asked)
	return r.runner
}

func (r *run) stopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.stopping.IsZero()
}

// giveUp records why the turn is given up. The first reason wins.
func (r *run) giveUp(failure store.Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.givenUp == nil {
		r.givenUp = &failure
	}
}

func (r *run) failure() *store.Failure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.givenUp
}

// poke signals told without blocking. A pending signal covers this one.
func poke(told chan struct{}) {
	select {
	case told <- struct{}{}:
	default:
	}
}
