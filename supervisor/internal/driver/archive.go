package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

// A session whose sandbox has slept for ArchiveAfter gives up its volume:
// the home is packed into an archive in the depot, and the sandbox is
// deleted. The session's next turn gets a new sandbox, into which the
// archive is unpacked before the turn is given to it.
//
// What the session records decides where the home is. While it has an
// archive, the archive is the home, and a sandbox of the session is not. So
// the archive is recorded only once the depot has it whole, the sandbox is
// deleted only after that, and the archive is deleted only after the session
// records that the home is in its new sandbox.

// leftAtOnce is the number of archives that a look at the sandboxes deletes
// at most.
const leftAtOnce = 100

// home is the home of a session in its sandbox, as a look found them.
type home struct {
	session store.Session
	sandbox sandbox.Sandbox
}

// archiveKey returns the key of the session's archive in the depot. The
// archives of a session are under archives(session).
func archiveKey(session, archive uuid.UUID) string {
	return archives(session) + "/" + archive.String()
}

func archives(session uuid.UUID) string {
	return "archives/" + session.String()
}

// claimed reports whether a Supervisor archives or restores the session's
// home. A claim that is older than ArchiveTime is of a Supervisor that is
// gone.
func (d *Driver) claimed(session store.Session) bool {
	return session.Claimed && session.ClaimedAt != nil && time.Since(*session.ClaimedAt) < d.config.ArchiveTime
}

// moving reports whether the home of the session is being archived or
// restored now, whatever the session recorded when the sessions were read. A
// sandbox that is awake for that is not idle, and what it is seen to do is
// not recorded.
func (d *Driver) moving(ctx context.Context, sessionID uuid.UUID) bool {
	session, err := d.store.Session(ctx, sessionID)
	if err != nil {
		slog.ErrorContext(ctx, "read session", "session", sessionID, "error", err)
		return true
	}
	return session.Archive != nil || d.claimed(session)
}

// due reports whether the session's home is to be archived: its sandbox has
// slept for ArchiveAfter, and archiving it has not failed lately.
func (d *Driver) due(session store.Session, made sandbox.Sandbox) bool {
	if d.config.Depot == nil || d.config.ArchiveAfter == 0 || session.EndedAt != nil || session.Archive != nil {
		return false
	}
	if made.State != sandbox.Asleep || session.SandboxState != store.SandboxAsleep ||
		session.AsleepSince == nil || time.Since(*session.AsleepSince) < d.config.ArchiveAfter {
		return false
	}
	return session.ClaimedAt == nil || time.Since(*session.ClaimedAt) >= d.config.ArchiveAgain
}

// shelve archives the homes, one after another, unless this Supervisor still
// archives those of an earlier look.
func (d *Driver) shelve(ctx context.Context, due []home) {
	if len(due) == 0 || !d.archiving.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer d.archiving.Store(false)
		for _, home := range due {
			if ctx.Err() != nil {
				return
			}
			d.archive(ctx, home.session, home.sandbox)
		}
	}()
}

// sent counts the bytes that were read.
type sent struct {
	io.Reader
	bytes int64
}

func (s *sent) Read(into []byte) (int, error) {
	n, err := s.Reader.Read(into)
	s.bytes += int64(n)
	return n, err
}

// archive moves the home of the session from its sandbox, which sleeps, into
// the depot, and deletes the sandbox. A turn that comes meanwhile keeps the
// home in the sandbox, and so does any failure.
func (d *Driver) archive(ctx context.Context, session store.Session, asleep sandbox.Sandbox) {
	ctx, cancel := context.WithTimeout(ctx, d.config.ArchiveTime)
	defer cancel()
	log := slog.With("session", session.ID, "sandbox", asleep.Name)
	turns, err := d.turns(ctx, session.ID)
	if err != nil {
		log.ErrorContext(ctx, "count the turns of the session", "error", err)
		return
	}
	claim := uuid.New()
	claimed, err := d.store.ClaimArchive(ctx, session.ID, asleep.Name, turns, claim, time.Now().Add(-d.config.ArchiveAgain))
	if err != nil {
		log.ErrorContext(ctx, "claim session to archive its home", "error", err)
		return
	}
	if !claimed {
		// A turn or another Supervisor came first.
		return
	}
	began := time.Now()
	archive, packed, err := d.pack(ctx, session, asleep.Name)
	archived, unknown := false, false
	if err == nil {
		archived, err = d.store.ArchiveSession(ctx, session.ID, claim, turns, archive)
		unknown = err != nil
	}
	// What is done from here on is done even if this Supervisor stops.
	ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if unknown {
		// The session may record the archive although recording it failed.
		// What it records decides.
		current, unread := d.store.Session(ctx, session.ID)
		if unread != nil {
			log.ErrorContext(ctx, "archive home", "error", err, "unread", unread)
			return
		}
		archived = current.Archive != nil && current.Archive.ID == archive.ID
	}
	if !archived {
		if err != nil {
			log.ErrorContext(ctx, "archive home", "error", err)
		}
		d.unshelve(ctx, session, asleep.Name, turns, archive)
		return
	}
	log.InfoContext(ctx, "home archived", "archive", archive.ID, "bytes", archive.Bytes, "took", time.Since(began).String())
	// Only the sandbox whose home was packed: the session's next turn may
	// have made another of the name. A sandbox that stays is deleted at a
	// later look, as one that its session does not have.
	if err := d.sandboxes.Delete(ctx, asleep.Name, packed.UID); err != nil {
		log.ErrorContext(ctx, "delete sandbox of archived session", "error", err)
	}
}

// turns returns the number of the session's turns.
func (d *Driver) turns(ctx context.Context, sessionID uuid.UUID) (int, error) {
	last, err := d.store.Turns(ctx, sessionID, math.MaxInt32, 1)
	if err != nil || len(last) == 0 {
		return 0, err
	}
	return last[0].Seq, nil
}

// pack wakes the session's sandbox and has its runner pack the home into a
// new archive in the depot. It returns the archive, with its ID even if it is
// not whole, and the sandbox as it ran.
func (d *Driver) pack(ctx context.Context, session store.Session, name string) (store.Archive, sandbox.Sandbox, error) {
	// What earlier archivings of the home left. The session has no archive,
	// so none of it is the home.
	if err := d.config.Depot.Delete(ctx, archives(session.ID)); err != nil {
		return store.Archive{}, sandbox.Sandbox{}, fmt.Errorf("delete earlier archives: %w", err)
	}
	agent, err := d.store.Agent(ctx, session.AgentID)
	if err != nil {
		return store.Archive{}, sandbox.Sandbox{}, fmt.Errorf("read agent: %w", err)
	}
	pod, err := d.pod(agent.Harness, session.ID)
	if err != nil {
		return store.Archive{}, sandbox.Sandbox{}, err
	}
	if err := d.sandboxes.Resume(ctx, name, pod); err != nil {
		return store.Archive{}, sandbox.Sandbox{}, fmt.Errorf("wake sandbox: %w", err)
	}
	running, err := d.awake(ctx, name, d.config.MaxStartup)
	if err != nil {
		return store.Archive{}, running, err
	}
	packed, err := sandbox.RunnerAt(running.Address).Home(ctx)
	if err != nil {
		return store.Archive{}, running, fmt.Errorf("pack home: %w", err)
	}
	defer packed.Close()
	id, err := uuid.NewV7()
	if err != nil {
		return store.Archive{}, running, err
	}
	archive := store.Archive{ID: id}
	home := &sent{Reader: packed}
	archive.Bytes, err = d.config.Depot.Put(ctx, archiveKey(session.ID, id), home)
	switch {
	case err != nil:
		return archive, running, fmt.Errorf("write archive: %w", err)
	case archive.Bytes != home.bytes:
		return archive, running, fmt.Errorf("the depot has %d bytes of an archive of %d", archive.Bytes, home.bytes)
	}
	return archive, running, nil
}

// awake waits until the sandbox runs, for limit at most if it is not zero,
// and returns the sandbox as it was last read.
func (d *Driver) awake(ctx context.Context, name string, limit time.Duration) (sandbox.Sandbox, error) {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	for {
		seen, err := d.sandboxes.Sandbox(ctx, name)
		switch {
		case err != nil:
			return seen, fmt.Errorf("get sandbox: %w", err)
		case seen.State == sandbox.Running:
			return seen, nil
		case seen.State != sandbox.Starting:
			return seen, errors.New(strings.TrimSuffix(fmt.Sprintf("the sandbox is %s: %s", seen.State, seen.Message), ": "))
		}
		select {
		case <-time.After(d.again()):
		case <-ctx.Done():
			return seen, errors.New(strings.TrimSuffix("the sandbox is not running yet: "+seen.Message, ": "))
		}
	}
}

// unshelve leaves the home of the session in its sandbox, after an archiving
// that did not record its archive: it deletes the archive, and puts the
// sandbox to sleep again unless a turn uses it.
func (d *Driver) unshelve(ctx context.Context, session store.Session, name string, turns int, archive store.Archive) {
	log := slog.With("session", session.ID, "sandbox", name)
	if archive.ID != uuid.Nil {
		if err := d.config.Depot.Delete(ctx, archiveKey(session.ID, archive.ID)); err != nil {
			log.ErrorContext(ctx, "delete archive that is not the home", "archive", archive.ID, "error", err)
		}
	}
	// The sandbox is read before the turns are counted. A turn that kept the
	// sandbox before it was read is counted, and one that keeps it later
	// changes it, so that it is not put to sleep as it was read.
	seen, err := d.sandboxes.Sandbox(ctx, name)
	if err != nil || seen.State == sandbox.Asleep || seen.State == sandbox.Lost {
		return
	}
	if now, err := d.turns(ctx, session.ID); err != nil || now != turns {
		return
	}
	err = d.sandboxes.Suspend(ctx, name, seen.Version)
	if err != nil && !errors.Is(err, sandbox.ErrChanged) && !errors.Is(err, sandbox.ErrNotFound) {
		log.ErrorContext(ctx, "put sandbox to sleep after archiving failed", "error", err)
	}
}

// abandon deletes the sandbox of a session whose home is an archive, if no
// turn of the session restores the home into it: the turn that made it has
// ended.
func (d *Driver) abandon(ctx context.Context, session store.Session, made sandbox.Sandbox) {
	last, err := d.store.Turns(ctx, session.ID, math.MaxInt32, 1)
	if err != nil || len(last) > 0 && last[0].EndedAt == nil {
		return
	}
	// The claim keeps a turn that comes now from taking the sandbox for its
	// own: it starts over.
	if _, claimed, err := d.store.ClaimRestore(ctx, session.ID, uuid.New()); err != nil || !claimed {
		return
	}
	if made.UID == "" {
		return
	}
	if err := d.sandboxes.Delete(ctx, made.Name, made.UID); err != nil {
		slog.ErrorContext(ctx, "delete sandbox that was not restored into", "session", session.ID, "sandbox", made.Name, "error", err)
	}
}

// restore gives the session, whose home is an archive, a new sandbox with
// the home unpacked in it. Whatever has the name of the session's sandbox
// before is not its home, and is deleted first.
func (d *Driver) restore(ctx, following context.Context, r *run) error {
	turn := r.turn
	if d.config.Depot == nil {
		return errors.New("the installation has no depot")
	}
	r.restoring()
	claim := uuid.New()
	archive, claimed, err := d.store.ClaimRestore(ctx, turn.SessionID, claim)
	if err != nil {
		return err
	}
	if !claimed {
		session, err := d.store.Session(ctx, turn.SessionID)
		switch {
		case err != nil:
			return err
		case session.Archive != nil:
			return errors.New("the session cannot be claimed")
		}
		// Another Supervisor that followed the turn has restored the home.
		r.restore(session.Sandbox)
		return nil
	}
	name := sandbox.Name(turn.SessionID)
	r.keep("")
	r.hold("restoring the session's home: deleting its former sandbox")
	if err := d.sandboxes.Delete(following, name, ""); err != nil {
		return fmt.Errorf("delete the session's former sandbox: %w", err)
	}
	for {
		_, err := d.sandboxes.Sandbox(following, name)
		if errors.Is(err, sandbox.ErrNotFound) {
			break
		}
		if err != nil {
			return fmt.Errorf("get sandbox: %w", err)
		}
		select {
		case <-time.After(d.again()):
		case <-following.Done():
			return errors.New("the session's former sandbox is still being deleted")
		}
	}
	if _, err := d.provide(ctx, r); err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	r.hold("restoring the session's home: its new sandbox is not running yet")
	// The turn's deadline ends the wait.
	running, err := d.awake(following, name, 0)
	if err != nil {
		return err
	}
	r.hold(fmt.Sprintf("restoring the session's home: unpacking its archive of %d bytes", archive.Bytes))
	began := time.Now()
	home, size, err := d.config.Depot.Get(following, archiveKey(turn.SessionID, archive.ID))
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer home.Close()
	if size != archive.Bytes {
		return fmt.Errorf("the depot has %d bytes of an archive of %d", size, archive.Bytes)
	}
	if err := sandbox.RunnerAt(running.Address).Unpack(following, home, size); err != nil {
		return fmt.Errorf("unpack home: %w", err)
	}
	restored, err := d.store.RestoreSession(ctx, turn.SessionID, claim, name)
	if err != nil {
		return err
	}
	if !restored {
		return errors.New("another Supervisor restores the home")
	}
	r.restore(name)
	slog.InfoContext(ctx, "home restored", "session", turn.SessionID, "sandbox", name,
		"archive", archive.ID, "bytes", size, "took", time.Since(began).String())
	// The home is in the sandbox, so the archive is not needed. If it stays,
	// the next archiving of the home deletes it.
	if err := d.config.Depot.Delete(ctx, archives(turn.SessionID)); err != nil {
		slog.ErrorContext(ctx, "delete archive of restored home", "session", turn.SessionID, "archive", archive.ID, "error", err)
	}
	return nil
}

// erase deletes the archive of a session that has ended, and removes it from
// the session.
func (d *Driver) erase(ctx context.Context, session store.Session) error {
	if d.config.Depot == nil {
		return errors.New("the installation has no depot")
	}
	if err := d.config.Depot.Delete(ctx, archives(session.ID)); err != nil {
		return fmt.Errorf("delete archive of session %s: %w", session.ID, err)
	}
	return d.store.ForgetArchive(ctx, session.ID, session.Archive.ID)
}

// clear deletes the archives that sessions have left when they ended, and
// ends the sessions that were not used for longer than Retention although
// their homes are archived: no sandbox expires for them.
func (d *Driver) clear(ctx context.Context) {
	if d.config.Depot == nil {
		return
	}
	var before time.Time
	if d.config.Retention > 0 {
		// A home is archived ArchiveAfter after its last turn at the
		// earliest.
		before = time.Now().Add(-max(d.config.Retention-d.config.ArchiveAfter, 0))
	}
	left, err := d.store.LeftArchives(ctx, before, leftAtOnce)
	if err != nil {
		slog.ErrorContext(ctx, "list archives to delete", "error", err)
		return
	}
	for _, session := range left {
		if session.EndedAt != nil {
			if err := d.erase(ctx, session); err != nil {
				slog.ErrorContext(ctx, "delete archive of ended session", "session", session.ID, "error", err)
			}
			continue
		}
		last, err := d.store.Turns(ctx, session.ID, math.MaxInt32, 1)
		if err != nil || len(last) == 0 || last[0].EndedAt == nil || !d.stale(last[0]) {
			continue
		}
		// A session that got a turn meanwhile is left to that turn.
		switch _, err := d.endSession(ctx, session.ID, store.EndExpired); {
		case err == nil:
			slog.InfoContext(ctx, "archived session expired", "session", session.ID)
		case !errors.Is(err, store.ErrSessionBusy):
			slog.ErrorContext(ctx, "end expired session", "session", session.ID, "error", err)
		}
	}
}
