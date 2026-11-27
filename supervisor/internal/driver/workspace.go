package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

// The workspace of a session is in its sandbox, which has to be awake for
// its files to be read or written. A sandbox that sleeps is woken for them
// as it is for a turn, and is not idle while they are read or written.

// ErrNoSandbox means that the session has no sandbox, and so no files.
var ErrNoSandbox = errors.New("the session has no sandbox")

// MaxFileBytes returns the largest size of a file that is written into the
// workspace of a session, in bytes.
func (d *Driver) MaxFileBytes() int64 {
	return d.config.MaxFileBytes
}

// Workspace is the workspace of a session: the runner of its sandbox, which
// is awake, and is not idle until Close.
type Workspace struct {
	*sandbox.Runner
	close func()
}

// Close ends the use of the workspace.
func (w *Workspace) Close() {
	w.close()
}

// Workspace returns the workspace of the session, for a request that reads
// its files or, with writes, writes one. The caller closes it when the
// request is over. A session without a sandbox gets one only for a request
// that writes. Workspace returns ErrNoSandbox for one that does not,
// store.ErrSessionEnded if the session has ended, sandbox.ErrNoWorker if no
// worker has room for the sandbox, and sandbox.ErrCrashed if it has crashed.
func (d *Driver) Workspace(ctx context.Context, sessionID uuid.UUID, writes bool) (*Workspace, error) {
	session, err := d.store.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.EndedAt != nil {
		return nil, store.ErrSessionEnded
	}
	id := session.Sandbox
	if id != "" {
		seen, err := d.sandboxes.Sandbox(ctx, id)
		switch {
		case errors.Is(err, sandbox.ErrNotFound), err == nil && seen.State == sandbox.Lost:
			// The sandbox is gone, with its files.
			if !writes {
				return nil, ErrNoSandbox
			}
			d.forget(ctx, sessionID, id)
			id = ""
		case err != nil:
			return nil, fmt.Errorf("get sandbox: %w", err)
		}
	}
	if id == "" {
		if !writes {
			return nil, ErrNoSandbox
		}
		agent, err := d.store.Agent(ctx, session.AgentID)
		if err != nil {
			return nil, err
		}
		if id, err = d.create(ctx, sessionID, agent.Harness, sandbox.Egress{}); err != nil {
			return nil, fmt.Errorf("create sandbox: %w", err)
		}
		session.SandboxState = store.SandboxAsleep
	}
	if err := d.ready(ctx, id); err != nil {
		return nil, err
	}
	if session.SandboxState != store.SandboxAwake {
		if err := d.store.SetSandboxState(ctx, sessionID, id, store.SandboxAwake); err != nil {
			return nil, err
		}
	}
	return &Workspace{Runner: d.sandboxes.Runner(id), close: d.hold(ctx, sessionID, id)}, nil
}

// ready wakes the sandbox up for its files. Idle sandboxes are put to sleep
// to make room for it, as for a turn, and another operation on it is waited
// for, for as long as a sweep takes.
func (d *Driver) ready(ctx context.Context, id string) error {
	patience := time.After(d.config.Sweep)
	for {
		err := d.sandboxes.Resume(ctx, id)
		switch {
		case errors.Is(err, sandbox.ErrNoWorker) && d.makeRoom(ctx):
		case errors.Is(err, sandbox.ErrOperating):
			select {
			case <-time.After(d.again()):
			case <-patience:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			return err
		}
	}
}

// hold records that the session's sandbox is used: now, again and again
// while it is held, and when the returned function lets go of it. The
// sandbox is not put to sleep as idle meanwhile.
func (d *Driver) hold(ctx context.Context, sessionID uuid.UUID, id string) func() {
	// The use is recorded also if the request is over.
	ctx = context.WithoutCancel(ctx)
	use := func() {
		if err := d.store.UseSandbox(ctx, sessionID, id); err != nil {
			slog.ErrorContext(ctx, "record the use of a sandbox", "session", sessionID, "sandbox", id, "error", err)
		}
	}
	use()
	if d.config.Idle == 0 {
		return use
	}
	done := make(chan struct{})
	go func() {
		again := time.NewTicker(max(d.config.Idle/3, time.Millisecond))
		defer again.Stop()
		for {
			select {
			case <-again.C:
				use()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			use()
		})
	}
}
