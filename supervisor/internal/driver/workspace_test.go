package driver

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/store"
)

// worked runs a turn of a new session to its end, and returns the session
// and the name of its sandbox.
func (i *installation) worked(driver *Driver) (store.Session, string) {
	i.t.Helper()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(i.t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(turn.ID)
	return session, given.Sandbox
}

// asleep puts the session's sandbox to sleep, and records that it sleeps.
func (i *installation) asleep(session store.Session, name string) {
	i.t.Helper()
	if err := i.service.Suspend(ctx, name, false); err != nil {
		i.t.Fatal(err)
	}
	if err := i.store.SetSandboxState(ctx, session.ID, name, store.SandboxAsleep); err != nil {
		i.t.Fatal(err)
	}
}

func TestTheFilesOfASessionAreReachedInItsSandbox(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session, name := i.worked(driver)
	i.service.Home(name).Put("notes.md", "to do")

	workspace, err := driver.Workspace(ctx, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	listed, _, err := workspace.List(ctx, "", "", 10)
	if err != nil || len(listed) != 1 || listed[0].Name != "notes.md" {
		t.Errorf("List = %+v, %v; want the file of the sandbox", listed, err)
	}
	if woken := i.service.Woken(name); woken != 1 {
		t.Errorf("the sandbox, which was awake, was woken %d times", woken)
	}
}

func TestASandboxThatSleepsIsWokenForItsFiles(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session, name := i.worked(driver)
	i.asleep(session, name)
	woken := i.service.Woken(name)

	workspace, err := driver.Workspace(ctx, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	if i.service.Woken(name) != woken+1 || i.service.State(name) != sandbox.Running {
		t.Errorf("sandbox woken %d times more and %s, want it woken once and running", i.service.Woken(name)-woken, i.service.State(name))
	}
	if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxAwake {
		t.Errorf("recorded state of the woken sandbox = %q, want awake", state)
	}

	// Another operation on a sandbox is waited for.
	workspace.Close()
	i.asleep(session, name)
	i.service.Operate(name, 2)
	again, err := driver.Workspace(ctx, session.ID, false)
	if err != nil {
		t.Fatalf("Workspace of a sandbox with another operation in progress: %v", err)
	}
	again.Close()
	// But not for long.
	i.asleep(session, name)
	i.service.Operate(name, 1<<30)
	if _, err := driver.Workspace(ctx, session.ID, false); !errors.Is(err, sandbox.ErrOperating) {
		t.Errorf("Workspace of a sandbox whose other operation does not end: %v, want ErrOperating", err)
	}
}

func TestAnIdleSandboxMakesRoomForTheFilesOfAnother(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	idle, other := i.worked(driver)
	session, name := i.worked(driver)
	i.asleep(session, name)
	i.service.Room(1)

	workspace, err := driver.Workspace(ctx, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Close()
	if i.service.State(other) != sandbox.Asleep || i.sessionOf(idle.ID).SandboxState != store.SandboxAsleep {
		t.Errorf("the idle sandbox is %s, recorded as %q; want it asleep", i.service.State(other), i.sessionOf(idle.ID).SandboxState)
	}

	// A sandbox that is at work makes no room.
	i.asleep(session, name)
	turn := i.send(driver, idle, "work")
	given := i.service.Turn(t)
	given.Tell("started", started)
	if _, err := driver.Workspace(ctx, session.ID, false); !errors.Is(err, sandbox.ErrNoWorker) {
		t.Errorf("Workspace without room: %v, want ErrNoWorker", err)
	}
	given.Tell("finished", finished)
	i.ended(turn.ID)
}

func TestASessionWithoutASandboxGetsOneForAFileThatIsWritten(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})

	if _, err := driver.Workspace(ctx, session.ID, false); !errors.Is(err, ErrNoSandbox) {
		t.Fatalf("Workspace to read of a session without a sandbox: %v, want ErrNoSandbox", err)
	}
	if name := i.sandbox(session); name != "" || len(i.service.Made()) != 0 {
		t.Fatalf("reading made the sandbox %q", name)
	}
	workspace, err := driver.Workspace(ctx, session.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	name := i.sandbox(session)
	if made, found := i.service.Made()[name]; !found || made.Harness != "opencode" || i.service.State(name) != sandbox.Running {
		t.Fatalf("sandbox of the session = %q, made %+v, %s; want one of its agent's harness, running", name, made, i.service.State(name))
	}
	if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxAwake {
		t.Errorf("recorded state of the new sandbox = %q, want awake", state)
	}
	if _, created, err := workspace.Write(ctx, "data/plans.json", strings.NewReader("{}"), 2); err != nil || !created {
		t.Errorf("Write: created %t, %v", created, err)
	}
	workspace.Close()
	if got := i.service.Home(name).Workspace()["data/plans.json"]; got != "{}" {
		t.Errorf("the file in the sandbox is %q", got)
	}

	// A sandbox that is lost has no files, and is replaced like none.
	i.service.Lose(name)
	if _, err := driver.Workspace(ctx, session.ID, false); !errors.Is(err, ErrNoSandbox) {
		t.Errorf("Workspace to read of a session whose sandbox is lost: %v, want ErrNoSandbox", err)
	}
	// Reading changes nothing of the session.
	if still := i.sandbox(session); still != name {
		t.Errorf("the session has the sandbox %q after its files were asked for, want %q as before", still, name)
	}
	workspace, err = driver.Workspace(ctx, session.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Close()
	if home := i.service.Home(i.sandbox(session)); home == nil || len(home.Workspace()) != 0 {
		t.Errorf("the session has no new sandbox, or one with the files of the old")
	}
}

// The first turn of a session may make its sandbox while a file is written
// into its workspace, with what the turn may reach.
func TestAFileThatIsWrittenLeavesTheEgressOfTheSandbox(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	egress := sandbox.Egress{Hosts: []sandbox.Host{{TLS: true, Name: "api.example.com", Port: 443}}}
	// As the Supervisor that follows the turn does, before the session
	// records the sandbox.
	if err := i.service.Create(ctx, sandbox.Request{Session: session.ID, Harness: "opencode", Egress: egress}); err != nil {
		t.Fatal(err)
	}

	workspace, err := driver.Workspace(ctx, session.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Close()
	name := i.sandbox(session)
	if made := i.service.Made()[name]; name == "" || !slices.Equal(made.Egress.Hosts, egress.Hosts) {
		t.Errorf("the sandbox %q may reach %v after a file was written, want %v as before", name, made.Egress.Hosts, egress.Hosts)
	}
}

func TestTheFilesOfASessionThatHasEndedOrCrashedAreNotReached(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session, name := i.worked(driver)
	i.asleep(session, name)
	i.service.Crash(name, "out of memory")
	if _, err := driver.Workspace(ctx, session.ID, false); !errors.Is(err, sandbox.ErrCrashed) {
		t.Errorf("Workspace of a crashed sandbox: %v, want ErrCrashed", err)
	}
	if _, err := driver.EndSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	for _, writes := range []bool{false, true} {
		if _, err := driver.Workspace(ctx, session.ID, writes); !errors.Is(err, store.ErrSessionEnded) {
			t.Errorf("Workspace of a session that has ended: %v, want ErrSessionEnded", err)
		}
	}
}

func TestASandboxWhoseFilesAreInUseIsNotIdle(t *testing.T) {
	i := install(t, func(config *Config) {
		config.Idle = 300 * time.Millisecond
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	driver, _ := i.supervisor()
	session, name := i.worked(driver)

	workspace, err := driver.Workspace(ctx, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	// As when a large file is read.
	time.Sleep(3 * i.config.Idle)
	if state := i.service.State(name); state != sandbox.Running {
		t.Fatalf("sandbox state while its files are in use = %s, want Running", state)
	}
	workspace.Close()
	closed := time.Now()
	i.eventually("the idle sandbox is put to sleep", func() bool {
		return i.service.State(name) == sandbox.Asleep
	})
	if idle := time.Since(closed); idle < i.config.Idle {
		t.Errorf("sandbox put to sleep only %s after its files were last used", idle)
	}
}
