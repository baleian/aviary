package driver

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aviary/supervisor/internal/depot"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/sandboxtest"
	"aviary/supervisor/store"
)

// archiving returns a change to the config of an installation that gives it
// a depot and has it archive the homes of sandboxes that have slept for a
// moment, and the directory of the depot.
func archiving(t *testing.T) (func(*Config), string) {
	t.Helper()
	dir := t.TempDir()
	objects, err := depot.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(depot.Handler(objects, nil))
	t.Cleanup(server.Close)
	return func(config *Config) {
		config.Depot = depot.At(server.URL)
		config.Idle = 100 * time.Millisecond
		config.ArchiveAfter = 100 * time.Millisecond
		config.ArchiveAgain = 400 * time.Millisecond
		config.ArchiveTime = 10 * time.Second
		config.MaxRestore = time.Minute
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	}, dir
}

// archived returns the keys of the archives in the depot's directory.
func archived(t *testing.T, dir string) []string {
	t.Helper()
	keys := []string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			// The depot deleted it meanwhile.
			return nil
		}
		if err == nil && !entry.IsDir() {
			keys = append(keys, strings.TrimPrefix(path, dir+"/"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// used runs a turn of a new session to its end, leaves files in the home,
// and returns the session with the name and the UID of its sandbox.
func (i *installation) used(driver *Driver, files string) (session store.Session, name, uid string) {
	i.t.Helper()
	session = i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(i.t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(turn.ID)
	i.service.Home(given.Sandbox).Write(files)
	return session, given.Sandbox, i.service.UID(given.Sandbox)
}

// shelved waits until the home of the session is archived, and returns the
// archive.
func (i *installation) shelved(session store.Session) store.Archive {
	i.t.Helper()
	i.eventually("the home of the session is archived", func() bool {
		return i.sessionOf(session.ID).Archive != nil
	})
	return *i.sessionOf(session.ID).Archive
}

func TestTheHomeOfASessionThatSleepsLongIsArchivedAndRestoredForItsNextTurn(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change)
	// Of two Supervisors, one archives the home.
	driver, _ := i.supervisor()
	i.supervisor()
	session, name, uid := i.used(driver, "the files of the agent")

	archive := i.shelved(session)
	key := archiveKey(session.ID, archive.ID)
	if keys := archived(t, dir); len(keys) != 1 || keys[0] != key {
		t.Errorf("the depot has %q, want only %s", keys, key)
	}
	if content, err := os.ReadFile(filepath.Join(dir, key)); err != nil || string(content) != "the files of the agent" {
		t.Errorf("the archive is %q, %v", content, err)
	}
	recorded := i.sessionOf(session.ID)
	if archive.Bytes != 22 || recorded.Sandbox != "" || recorded.SandboxState != store.SandboxArchived {
		t.Errorf("archived session = %+v with the archive %+v; want no sandbox, and an archive of 22 bytes", recorded, archive)
	}
	i.eventually("the sandbox of the archived session is deleted", func() bool { return i.service.UID(name) == "" })

	// The next turn runs in a new sandbox, with the home as it was.
	next := i.send(driver, session, "and then")
	given := i.service.Turn(t)
	if given.ID != next.ID.String() || given.Sandbox != name || i.service.UID(name) == uid {
		t.Errorf("next turn = %+v in the sandbox %s; want it in a new sandbox of the session", given, i.service.UID(name))
	}
	if files, unpacked := i.service.Home(name).Content(); files != "the files of the agent" || unpacked != 1 {
		t.Errorf("the home of the new sandbox has %q, unpacked %d times", files, unpacked)
	}
	recorded = i.sessionOf(session.ID)
	if recorded.Archive != nil || recorded.Sandbox != name || recorded.SandboxState != store.SandboxAwake {
		t.Errorf("restored session = %+v with the archive %+v; want its sandbox awake and no archive", recorded, recorded.Archive)
	}
	if keys := archived(t, dir); len(keys) != 0 {
		t.Errorf("the depot still has %q", keys)
	}
	given.Tell("started", started)
	given.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v", ended)
	}
}

// A turn has the home before an archiving does: the home stays in the
// sandbox, which the turn runs in, and the archive is thrown away.
func TestATurnThatComesWhileItsHomeIsArchivedKeepsTheHomeInItsSandbox(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change)
	driver, _ := i.supervisor()
	session, name, uid := i.used(driver, "the files of the agent")
	release := i.service.Home(name).Hold()
	defer release()
	i.eventually("the sandbox is woken to archive the home", func() bool {
		return i.service.Woken(name) == 1 && len(archived(t, dir)) == 1
	})
	if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxAsleep {
		t.Errorf("the session records its sandbox as %q while its home is archived, want asleep", state)
	}

	next := i.send(driver, session, "and then")
	given := i.service.Turn(t)
	if given.ID != next.ID.String() || i.service.UID(name) != uid {
		t.Errorf("turn = %+v in the sandbox %s, want it in the sandbox %s", given, i.service.UID(name), uid)
	}
	i.eventually("the session records that its sandbox is awake", func() bool {
		return i.sessionOf(session.ID).SandboxState == store.SandboxAwake
	})
	given.Tell("started", started)
	release()
	i.eventually("the archive is thrown away", func() bool { return len(archived(t, dir)) == 0 })
	// The sandbox is neither deleted nor put to sleep under the turn.
	time.Sleep(10 * i.config.Sweep)
	if recorded := i.sessionOf(session.ID); recorded.Archive != nil || recorded.Sandbox != name ||
		i.service.UID(name) != uid || i.service.State(name) != sandbox.Running {
		t.Errorf("session = %+v with the archive %+v, sandbox %s %s; want the home in its running sandbox",
			recorded, recorded.Archive, i.service.UID(name), i.service.State(name))
	}
	given.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
}

// An archive that the runner does not send whole is not the home: the home
// stays in the sandbox, which sleeps on, until a later archiving.
func TestAnArchivingThatFailsLeavesTheHomeInItsSandbox(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change)
	driver, _ := i.supervisor()
	session, name, uid := i.used(driver, "the files of the agent")
	i.service.Home(name).Cut()
	i.eventually("the sandbox is woken to archive the home, and put to sleep again", func() bool {
		return i.service.Woken(name) == 1 && i.service.State(name) == sandbox.Asleep
	})
	recorded := i.sessionOf(session.ID)
	if recorded.Archive != nil || recorded.Sandbox != name || recorded.SandboxState != store.SandboxAsleep || i.service.UID(name) != uid {
		t.Errorf("session = %+v with the archive %+v, want the home in its sandbox, asleep", recorded, recorded.Archive)
	}
	if keys := archived(t, dir); len(keys) != 0 {
		t.Errorf("the depot has %q, want nothing", keys)
	}
	if again := time.Since(*recorded.ClaimedAt); again < i.config.ArchiveAgain {
		// The next archiving does not come at once.
		if woken := i.service.Woken(name); woken != 1 {
			t.Errorf("the sandbox was woken %d times within %s of the archiving that failed", woken, again)
		}
	}

	archive := i.shelved(session)
	if files, err := os.ReadFile(filepath.Join(dir, archiveKey(session.ID, archive.ID))); err != nil || string(files) != "the files of the agent" {
		t.Errorf("the archive is %q, %v", files, err)
	}
}

// A home that cannot be restored stays an archive. The turn fails once it
// has waited for longer than a turn may, the sandbox that was made for it is
// deleted, and a later turn restores the home.
func TestAHomeThatCannotBeRestoredStaysArchived(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change, func(config *Config) {
		config.MaxStartup, config.MaxRestore = 300*time.Millisecond, 300*time.Millisecond
	})
	driver, _ := i.supervisor()
	session, name, _ := i.used(driver, "the files of the agent")
	archive := i.shelved(session)
	// The depot loses sight of the archive.
	file := filepath.Join(dir, archiveKey(session.ID, archive.ID))
	if err := os.Rename(file, file+".elsewhere"); err != nil {
		t.Fatal(err)
	}

	late := i.send(driver, session, "and then")
	ended := i.ended(late.ID)
	if !failed(ended, store.FailureStartup) || !strings.Contains(ended.Failure.Message, "the session's home") {
		t.Errorf("ended turn = %+v, failure = %+v; want it to say that the home was not restored", ended, ended.Failure)
	}
	if waited := ended.EndedAt.Sub(ended.CreatedAt); waited < i.config.MaxStartup+i.config.MaxRestore {
		t.Errorf("the turn was given up after %s, want it to have the time to restore the home too", waited)
	}
	if len(i.service.Given()) != 1 {
		t.Errorf("runners were given %v, want only the first turn", i.service.Given())
	}
	i.eventually("the sandbox that was made for the turn is deleted", func() bool {
		return i.service.UID(name) == "" && i.sessionOf(session.ID).Sandbox == ""
	})
	if recorded := i.sessionOf(session.ID); recorded.Archive == nil || recorded.Archive.ID != archive.ID || recorded.SandboxState != store.SandboxArchived {
		t.Errorf("session = %+v with the archive %+v, want it archived as before", recorded, recorded.Archive)
	}

	if err := os.Rename(file+".elsewhere", file); err != nil {
		t.Fatal(err)
	}
	next := i.send(driver, session, "again")
	given := i.service.TurnWith(t, next.ID.String())
	if files, _ := i.service.Home(given.Sandbox).Content(); files != "the files of the agent" {
		t.Errorf("the home of the new sandbox has %q", files)
	}
}

// broken is a depot that deletes nothing while it is down.
type broken struct {
	Depot
	down atomic.Bool
}

func (b *broken) Delete(ctx context.Context, key string) error {
	if b.down.Load() {
		return errors.New("the depot does not answer")
	}
	return b.Depot.Delete(ctx, key)
}

// The archive of a session that ends is deleted, at once or as soon as the
// depot answers again.
func TestTheArchiveOfASessionThatHasEndedIsDeleted(t *testing.T) {
	change, dir := archiving(t)
	var flaky *broken
	i := install(t, change, func(config *Config) {
		flaky = &broken{Depot: config.Depot}
		config.Depot = flaky
	})
	driver, _ := i.supervisor()
	one, _, _ := i.used(driver, "the files of one")
	other, _, _ := i.used(driver, "the files of another")
	i.shelved(one)
	left := i.shelved(other)

	ended, err := driver.EndSession(ctx, one.ID)
	if err != nil || ended.EndedAt == nil || ended.Archive != nil || ended.SandboxState != "" {
		t.Errorf("EndSession = %+v with the archive %+v, %v; want it ended without an archive", ended, ended.Archive, err)
	}
	if keys := archived(t, dir); len(keys) != 1 || keys[0] != archiveKey(other.ID, left.ID) {
		t.Errorf("the depot has %q, want only the archive of the other session", keys)
	}

	flaky.down.Store(true)
	if ended, err := driver.EndSession(ctx, other.ID); err == nil || ended.EndedAt == nil {
		t.Errorf("EndSession while the depot is down = %+v, %v; want the session ended, and an error", ended, err)
	}
	time.Sleep(10 * i.config.Sweep)
	if recorded := i.sessionOf(other.ID); recorded.Archive == nil || len(archived(t, dir)) != 1 {
		t.Errorf("session = %+v with the archive %+v, the depot has %q; want the archive left", recorded, recorded.Archive, archived(t, dir))
	}
	flaky.down.Store(false)
	i.eventually("the archive that was left is deleted", func() bool {
		return len(archived(t, dir)) == 0 && i.sessionOf(other.ID).Archive == nil
	})
}

// A session whose home is archived has no sandbox that expires. It ends
// when it was not used for longer than a sandbox is kept, like any other.
func TestAnArchivedSessionThatIsNotUsedForLongerThanItsSandboxIsKeptHasEnded(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change, func(config *Config) { config.Retention = time.Second })
	driver, _ := i.supervisor()
	session, _, _ := i.used(driver, "the files of the agent")
	i.shelved(session)
	if recorded := i.sessionOf(session.ID); recorded.EndedAt != nil {
		t.Fatalf("the session ended at %v, as soon as its home was archived", recorded.EndedAt)
	}
	i.eventually("the archived session has ended", func() bool { return i.sessionOf(session.ID).EndedAt != nil })
	recorded := i.sessionOf(session.ID)
	turns, err := i.store.Turns(ctx, session.ID, math.MaxInt32, 1)
	if err != nil || len(turns) != 1 {
		t.Fatalf("Turns = %+v, %v", turns, err)
	}
	if unused := recorded.EndedAt.Sub(turns[0].CreatedAt); recorded.EndReason != store.EndExpired || unused < i.config.Retention {
		t.Errorf("session = %+v, ended %s after its last turn; want it expired after %s", recorded, unused, i.config.Retention)
	}
	i.eventually("the archive of the expired session is deleted", func() bool {
		return len(archived(t, dir)) == 0 && i.sessionOf(session.ID).Archive == nil
	})
}

// A sandbox that is awake for the archiving of its home is not at work, and
// yet is not put to sleep like one that is idle: the archive would be cut
// short every time.
func TestASandboxThatIsAwakeForAnArchivingIsNotPutToSleep(t *testing.T) {
	change, dir := archiving(t)
	i := install(t, change)
	// The other Supervisor looks at the sandbox as often.
	driver, _ := i.supervisor()
	i.supervisor()
	session, name, _ := i.used(driver, "the files of the agent")
	release := i.service.Home(name).Hold()
	defer release()
	i.eventually("the sandbox is woken to archive the home", func() bool {
		return i.service.Woken(name) == 1 && len(archived(t, dir)) == 1
	})
	asleep := i.sessionOf(session.ID)
	// Longer than a sandbox may be idle, and several looks.
	time.Sleep(3*i.config.Idle + 3*lookEvery*i.config.Sweep)
	if state, woken := i.service.State(name), i.service.Woken(name); state != sandbox.Running || woken != 1 {
		t.Fatalf("the sandbox is %s, woken %d times; want it running since it was woken", state, woken)
	}
	// Nor does the session take it for awake: it has slept for as long as
	// before, which a later archiving goes by.
	if recorded := i.sessionOf(session.ID); recorded.SandboxState != store.SandboxAsleep ||
		recorded.AsleepSince == nil || !recorded.AsleepSince.Equal(*asleep.AsleepSince) {
		t.Errorf("the session records its sandbox as %q since %v, want asleep since %v", recorded.SandboxState, recorded.AsleepSince, asleep.AsleepSince)
	}
	release()
	if archive := i.shelved(session); archive.Bytes != 22 {
		t.Errorf("archive = %+v, want the 22 bytes of the home", archive)
	}
}

// kept is a sandbox service that deletes no sandbox that is named with its
// UID, as the cluster does not while it does not answer.
type kept struct {
	*sandboxtest.Service
	down atomic.Bool
}

func (k *kept) Delete(ctx context.Context, name, uid string) error {
	if uid != "" && k.down.Load() {
		return errors.New("the cluster does not answer")
	}
	return k.Service.Delete(ctx, name, uid)
}

// A sandbox that stays after its home was archived is not the home any more.
// The next turn does not run in it, whatever it has, and it is deleted.
func TestASandboxThatStaysAfterItsHomeWasArchivedIsNotUsed(t *testing.T) {
	change, _ := archiving(t)
	i := install(t, change)
	cluster := &kept{Service: i.service}
	cluster.down.Store(true)
	running, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	driver := New(i.config, cluster, i.store, i.events, metrics.New(nil, nil))
	go driver.Run(running)
	session, name, uid := i.used(driver, "the files of the agent")
	i.shelved(session)
	if i.service.UID(name) != uid {
		t.Fatalf("the sandbox was deleted although the cluster did not answer")
	}
	// What happens in the sandbox now is not the session's.
	i.service.Home(name).Write("what the sandbox has since")

	next := i.send(driver, session, "and then")
	given := i.service.TurnWith(t, next.ID.String())
	if i.service.UID(name) == uid {
		t.Errorf("the turn runs in the sandbox that stayed")
	}
	if files, unpacked := i.service.Home(given.Sandbox).Content(); files != "the files of the agent" || unpacked != 1 {
		t.Errorf("the home of the turn's sandbox has %q, unpacked %d times", files, unpacked)
	}
	// The sandbox of the turn is not deleted in place of the one that
	// stayed.
	cluster.down.Store(false)
	time.Sleep(3 * lookEvery * i.config.Sweep)
	if i.service.UID(name) == "" || i.service.UID(name) == uid || i.service.State(name) != sandbox.Running {
		t.Errorf("the sandbox of the turn is %s %s, want it running", i.service.UID(name), i.service.State(name))
	}
}

// late is a sandbox service that deletes a sandbox that is named with its
// UID only once it is let to.
type late struct {
	*sandboxtest.Service
	asked, let chan struct{}
}

func (l *late) Delete(ctx context.Context, name, uid string) error {
	if uid != "" {
		select {
		case l.asked <- struct{}{}:
		default:
		}
		<-l.let
	}
	return l.Service.Delete(ctx, name, uid)
}

// The sandbox whose home was archived is deleted, and no other: a turn that
// comes before it is deleted gets a new sandbox of the same name, which
// stays.
func TestOnlyTheSandboxWhoseHomeWasArchivedIsDeleted(t *testing.T) {
	change, _ := archiving(t)
	i := install(t, change)
	cluster := &late{Service: i.service, asked: make(chan struct{}, 1), let: make(chan struct{})}
	running, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	driver := New(i.config, cluster, i.store, i.events, metrics.New(nil, nil))
	go driver.Run(running)
	session, name, uid := i.used(driver, "the files of the agent")
	i.shelved(session)
	select {
	case <-cluster.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the sandbox of the archived session was not deleted")
	}

	next := i.send(driver, session, "and then")
	given := i.service.TurnWith(t, next.ID.String())
	restored := i.service.UID(name)
	if restored == "" || restored == uid {
		t.Fatalf("the turn runs in the sandbox %q, want a new one", restored)
	}
	close(cluster.let)
	time.Sleep(3 * lookEvery * i.config.Sweep)
	if now := i.service.UID(name); now != restored || i.service.State(name) != sandbox.Running {
		t.Errorf("the sandbox of the turn is now %q, %s; want %s, running", now, i.service.State(name), restored)
	}
	given.Tell("started", started)
	given.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
}
