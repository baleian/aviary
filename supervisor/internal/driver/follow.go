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

// run is a turn as this Supervisor follows it.
type run struct {
	turn store.FollowedTurn
	told *orders
	// leave ends the following of the turn.
	leave context.CancelFunc
	// dispatched is when the turn was dispatched.
	dispatched time.Time
	// changed is filled when the limit of the turn has changed, and asked
	// when the turn was asked to stop.
	changed, asked chan struct{}

	mu sync.Mutex
	// sandbox is the sandbox of the session, once it has one.
	sandbox string
	// runner is the runner in the sandbox, once it has the turn, and given
	// whether the runner may have it: the turn was given to it, here or by a
	// Supervisor before.
	runner *sandbox.Runner
	given  bool
	// started is whether the harness has the message.
	started bool
	// stopping is when the turn was asked to stop, if it was.
	stopping time.Time
	// holdup is why the turn is not with its runner yet.
	holdup string
	// givenUp is why the turn is given up, once it is.
	givenUp *store.Failure
}

// again is how long the one who follows a turn waits before it asks again
// for what was not there.
func (d *Driver) again() time.Duration {
	return d.config.Sweep / 10
}

// run follows a turn: it has the sandbox of the turn's session run, gives
// the turn to the runner in it, and records what the runner tells until the
// turn has ended. It returns earlier if ctx is done or the orders say to
// leave the turn, and then the next Supervisor that follows the turn goes
// on from what is recorded.
func (d *Driver) run(ctx context.Context, turnID uuid.UUID, told *orders) {
	turn, err := d.store.FollowTurn(ctx, turnID)
	switch {
	case errors.Is(err, store.ErrTurnEnded), errors.Is(err, store.ErrNotFound):
		return
	case err != nil:
		slog.ErrorContext(ctx, "read the turn to follow", "turn", turnID, "error", err)
		return
	}
	received, err := d.events.Received(ctx, turnID)
	if err != nil {
		slog.ErrorContext(ctx, "read what the runner has told", "turn", turnID, "error", err)
		return
	}
	slog.InfoContext(ctx, "following turn", "turn", turnID, "received", received)
	defer d.metrics.Following(turn.Harness)()

	// A turn of which something is recorded was given to its runner, by a
	// Supervisor that is gone. It is not given again: a runner that does
	// not have it any more would run it a second time.
	given := received > 0 || turn.StartedAt != nil
	r := &run{
		turn: turn, told: told, dispatched: time.Now().Add(-turn.Dispatched),
		changed: make(chan struct{}, 1), asked: make(chan struct{}, 1),
		sandbox: turn.Sandbox, given: given, started: turn.StartedAt != nil,
	}
	if turn.CancelRequestedAt != nil {
		r.stopping = *turn.CancelRequestedAt
	}
	// What is done for the turn while it is followed ends with the
	// following. What settles the turn does not.
	following, leave := context.WithCancel(ctx)
	defer leave()
	r.leave = leave
	go d.watch(following, leave, r)

	if runner := d.reach(ctx, following, r, given); runner != nil {
		d.read(ctx, following, r, runner, received)
	}
	if failure := r.failure(); failure != nil {
		d.giveUp(ctx, r, *failure)
	}
}

// watch tells the runner of the turn what the turn is told, and gives the
// turn up when it has taken longer than it may.
func (d *Driver) watch(following context.Context, leave context.CancelFunc, r *run) {
	for {
		at, _ := r.limit(d.config)
		timer := time.NewTimer(time.Until(at))
		select {
		case <-following.Done():
			timer.Stop()
			return
		case <-timer.C:
			// The limit moves when the turn starts, and when it is asked to
			// stop.
			if at, failure := r.limit(d.config); !time.Now().Before(at) {
				r.giveUp(failure)
				leave()
				return
			}
		case <-r.changed:
		case <-r.told.stop:
			// What was not told is told again at the next sweep.
			if runner := r.stop(); runner != nil {
				err := runner.Cancel(following, r.turn.ID)
				if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
					slog.WarnContext(following, "tell the runner to stop the turn", "turn", r.turn.ID, "error", err)
				}
			}
		case <-r.told.answers:
			d.answer(following, r)
		}
		timer.Stop()
	}
}

// answer gives the runner the answers that its harness has not got yet. A
// runner drops an answer that it has given its harness.
func (d *Driver) answer(ctx context.Context, r *run) {
	runner := r.reached()
	if runner == nil {
		return
	}
	answers, err := d.store.Answers(ctx, &r.turn.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read the answers to requests for approval", "turn", r.turn.ID, "error", err)
		return
	}
	if len(answers) == 0 {
		return
	}
	for _, answer := range answers {
		err := runner.Answer(ctx, r.turn.ID, answer.ID, answer.OptionID)
		if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			slog.WarnContext(ctx, "give the runner an answer", "turn", r.turn.ID, "error", err)
		}
	}
}

// reach has the sandbox of the turn's session run and gives the runner in
// it the turn, unless it was given. It returns the runner, or nothing if
// the turn is not to be followed further.
func (d *Driver) reach(ctx, following context.Context, r *run, given bool) *sandbox.Runner {
	for following.Err() == nil {
		// A turn that is asked to stop before a runner has it is not run.
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

// attempt is one try of reach. An error tells what the turn waits for.
func (d *Driver) attempt(ctx, following context.Context, r *run, given bool) (*sandbox.Runner, error) {
	turn := r.turn
	id := r.sandboxID()
	if id == "" && given {
		r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the sandbox of the session is gone"})
		return nil, nil
	}
	if id == "" {
		var err error
		if id, err = d.provide(ctx, r); err != nil {
			return nil, fmt.Errorf("make the sandbox of the session: %w", err)
		}
	}
	seen, err := d.sandboxes.Sandbox(following, id)
	switch {
	case errors.Is(err, sandbox.ErrNotFound), err == nil && seen.State == sandbox.Lost:
		if given {
			r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the sandbox of the session is gone"})
			return nil, nil
		}
		// The sandbox was lost after the last turn of the session, or
		// could not be made.
		gone := "the sandbox of the session is gone"
		if err == nil {
			gone = "the sandbox of the session is lost: " + seen.Message
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
		// The session gets a new one.
		return nil, errors.New(gone + ", and a new one is made")
	case err != nil:
		return nil, fmt.Errorf("look at the sandbox of the session: %w", err)
	case seen.State == sandbox.Asleep && given:
		r.giveUp(asleep)
		return nil, nil
	case seen.State == sandbox.Asleep:
		// The session was not at work, and its sandbox was put to sleep.
		harness, err := d.harness(turn)
		if err != nil {
			return nil, err
		}
		if err := d.sandboxes.Resume(following, id, harness); err != nil {
			return nil, fmt.Errorf("wake the sandbox of the session: %w", err)
		}
		return nil, errors.New("the sandbox of the session is woken")
	case seen.State != sandbox.Running:
		why := "the sandbox of the session does not run yet"
		if seen.Message != "" {
			why += ": " + seen.Message
		}
		return nil, errors.New(why)
	}
	runner := sandbox.RunnerAt(seen.Address)
	if given {
		return runner, nil
	}
	// The sandbox is kept for as long as the installation says now, which
	// may be until it is deleted.
	var until time.Time
	if d.config.Retention > 0 {
		until = time.Now().Add(d.config.Retention)
	}
	if err := d.sandboxes.Keep(following, id, until); err != nil {
		return nil, fmt.Errorf("have the sandbox of the session kept: %w", err)
	}
	// The runner may have the turn even when no answer comes.
	r.give()
	err = runner.Start(following, turn.ID, sandbox.Turn{
		Text: turn.Text, Instructions: turn.Instructions, Approval: turn.Approval, Environment: environment(turn),
	})
	if errors.Is(err, sandbox.ErrBusy) {
		// A turn that this installation has ended runs on in the sandbox.
		r.giveUp(store.Failure{Reason: store.FailureRunner, Message: "the runner of the session runs another turn"})
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("give the turn to its runner: %w", err)
	}
	return runner, nil
}

// expired reports whether the session of the turn was not used for longer
// than its sandbox is kept: the turn before this one is that old. A sandbox
// that is gone then was deleted for that.
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

// harness is the harness of the turn as the installation has it.
func (d *Driver) harness(turn store.FollowedTurn) (sandbox.Harness, error) {
	harness, found := d.config.Harnesses[turn.Harness]
	if !found {
		return sandbox.Harness{}, fmt.Errorf("the installation has no harness %s", turn.Harness)
	}
	return harness, nil
}

// provide makes the sandbox of the turn's session, and returns its name.
func (d *Driver) provide(ctx context.Context, r *run) (string, error) {
	turn := r.turn
	harness, err := d.harness(turn)
	if err != nil {
		return "", err
	}
	err = d.sandboxes.Create(ctx, sandbox.Request{
		Session: turn.SessionID,
		Labels:  map[string]string{labelAgent: turn.AgentID.String(), labelProject: turn.ProjectID.String()},
		Harness: harness,
		Kept:    d.config.Retention,
	})
	if err != nil {
		return "", err
	}
	// The sandbox of a session has its name. Another Supervisor that made it
	// first made the same.
	name := sandbox.Name(turn.SessionID)
	if _, err := d.store.SetSandbox(ctx, turn.SessionID, "", name); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "made the sandbox of the session", "session", turn.SessionID, "sandbox", name)
	r.keep(name)
	return name, nil
}

// forget has the sandbox deleted, and the session have none.
func (d *Driver) forget(ctx context.Context, sessionID uuid.UUID, id string) {
	if err := d.sandboxes.Delete(ctx, id); err != nil {
		slog.ErrorContext(ctx, "delete the sandbox of the session", "session", sessionID, "sandbox", id, "error", err)
	}
	if _, err := d.store.SetSandbox(ctx, sessionID, id, ""); err != nil {
		slog.ErrorContext(ctx, "forget the sandbox of the session", "session", sessionID, "sandbox", id, "error", err)
	}
}

// giveUp ends a turn that cannot go on as failed. A sandbox whose runner may
// have the turn is put to sleep: what runs in it stops with its pod, and the
// volume of the session stays for the next turn, which wakes it. A sandbox
// that the turn did not reach, such as one that was still being made or
// woken, is left as it is.
func (d *Driver) giveUp(ctx context.Context, r *run, failure store.Failure) {
	slog.WarnContext(ctx, "turn is given up", "turn", r.turn.ID, "reason", failure.Reason, "message", failure.Message)
	if id := r.sandboxID(); id != "" && r.wasGiven() {
		err := d.sandboxes.Suspend(ctx, id)
		if errors.Is(err, sandbox.ErrNotFound) {
			// The sandbox is gone: the next turn of the session gets a new
			// one.
			_, err = d.store.SetSandbox(ctx, r.turn.SessionID, id, "")
		}
		// A turn whose sandbox cannot be put to sleep is given up again, by
		// the next sweep.
		if err != nil {
			slog.ErrorContext(ctx, "put the sandbox of the session to sleep", "session", r.turn.SessionID, "sandbox", id, "error", err)
			return
		}
	}
	if d.end(ctx, r.turn.ID, false, failure) && failure.Reason == store.FailureSessionExpired {
		if _, err := d.store.EndSession(ctx, r.turn.SessionID); err != nil {
			slog.ErrorContext(ctx, "end the session", "session", r.turn.SessionID, "error", err)
		}
	}
}

// read records what the runner tells of the turn, from the event after the
// one with the number, until the turn has ended.
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
				Reason: store.FailureRunner, Message: "the runner of the session does not have the turn any more",
			})
			return
		case err != nil:
			slog.WarnContext(ctx, "read the events of the turn", "turn", turn.ID, "after", after, "error", err)
		}
		// The reading was cut off, or the runner cannot be reached.
		seen, gone := d.sandboxes.Sandbox(following, r.sandboxID())
		switch {
		case errors.Is(gone, sandbox.ErrNotFound), gone == nil && seen.State == sandbox.Lost:
			r.giveUp(store.Failure{Reason: store.FailureSandboxLost, Message: "the sandbox of the session is gone"})
			return
		case gone == nil && seen.State == sandbox.Asleep:
			r.giveUp(asleep)
			return
		case gone == nil && seen.State == sandbox.Running && seen.Address != runner.Address():
			// A pod that is made anew has a runner of its own, which says that
			// it does not have the turn.
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

// errEnded ends the reading of a turn that has ended.
var errEnded = errors.New("the turn has ended")

// asleep is the failure of a turn whose sandbox was put to sleep while its
// runner had the turn: the runner stopped with the pod.
var asleep = store.Failure{Reason: store.FailureRunner, Message: "the sandbox of the session was put to sleep while the turn ran"}

// ending is how a turn ended, as its runner tells it.
type ending struct {
	succeeded bool
	failure   store.Failure
}

// passed are the events of a runner that the clients of a turn read as the
// runner tells them. A runner may tell others: what comes out of a sandbox
// is what the agent in it wants to come.
var passed = map[string]bool{
	"message.delta":      true,
	"thought.delta":      true,
	"tool.call":          true,
	"tool.update":        true,
	"plan.update":        true,
	"approval.requested": true,
	"approval.resolved":  true,
}

// record records one event of the turn's runner, which may be recorded
// already, by this Supervisor or by another. It returns how the turn ended,
// if the event tells that.
func (d *Driver) record(ctx context.Context, r *run, event sandbox.Event) (*ending, error) {
	turn := r.turn
	wrong := &ending{failure: store.Failure{
		Reason: store.FailureRunner, Message: fmt.Sprintf("the runner told %q without what it takes", event.Name),
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
		// The request is kept for the person who answers it. It is also an
		// event of the turn, like the ones below.
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
			// The harness got the answer of a person. The runner does not
			// know whose it was.
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

// with returns the JSON object with one more field.
func with(object []byte, name string, value any) ([]byte, error) {
	fields := map[string]any{}
	if err := json.Unmarshal(object, &fields); err != nil {
		return nil, err
	}
	fields[name] = value
	return json.Marshal(fields)
}

// limit is when the turn is given up if it has not ended, and why it is
// then.
func (r *run) limit(config Config) (time.Time, store.Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// The time that the turn waits for a person is of its time too: its
	// sandbox runs while it waits.
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
	// A turn that was asked to stop ends as cancelled, whatever it fails
	// for.
	if stopped := r.stopping.Add(config.CancelGrace); !r.stopping.IsZero() && stopped.Before(at) {
		at = stopped
		failure = store.Failure{Reason: store.FailureRunner, Message: "the turn did not stop when it was asked to"}
	}
	return at, failure
}

func (r *run) sandboxID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sandbox
}

// keep sets the sandbox of the session.
func (r *run) keep(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sandbox = id
}

// hold notes why the turn is not with its runner yet.
func (r *run) hold(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holdup = why
}

// reach notes that the runner has the turn, and has it told what the turn
// was told before.
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

// give notes that the turn is given to the runner, and wasGiven whether it
// was.
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

// start notes that the harness has the message.
func (r *run) start() {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	poke(r.changed)
}

// stop notes that the turn was asked to stop, and returns the runner that
// is to be told, if one has the turn.
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

// giveUp notes why the turn is given up. The first reason stands.
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

// poke fills a channel that tells of something that may be told twice.
func poke(told chan struct{}) {
	select {
	case told <- struct{}{}:
	default:
	}
}
