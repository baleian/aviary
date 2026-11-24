package storetest

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// asleep returns a session that has had a turn, and the name of its sandbox,
// which is asleep.
func (s supervisor) asleep() (uuid.UUID, string) {
	s.t.Helper()
	session := s.session()
	sandbox := "sandbox-of-" + session.String()
	turn := s.running(session)
	if set, err := s.SetSandbox(ctx, session, "", sandbox); err != nil || !set {
		s.t.Fatalf("SetSandbox = %t, %v", set, err)
	}
	s.end(turn.TurnID, true, store.Failure{})
	if err := s.SetSandboxState(ctx, session, sandbox, store.SandboxAsleep); err != nil {
		s.t.Fatal(err)
	}
	return session, sandbox
}

// archived returns a session whose home is archived, and the archive.
func (s supervisor) archived() (uuid.UUID, store.Archive) {
	s.t.Helper()
	session, sandbox := s.asleep()
	claim := uuid.New()
	if claimed, err := s.ClaimArchive(ctx, session, sandbox, 1, claim, time.Now()); err != nil || !claimed {
		s.t.Fatalf("ClaimArchive = %t, %v", claimed, err)
	}
	if archived, err := s.ArchiveSession(ctx, session, claim, 1, store.Archive{ID: uuid.New(), Bytes: 1 << 20}); err != nil || !archived {
		s.t.Fatalf("ArchiveSession = %t, %v", archived, err)
	}
	return session, *s.read(session).Archive
}

// read returns the session.
func (s supervisor) read(id uuid.UUID) store.Session {
	s.t.Helper()
	session, err := s.Session(ctx, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return session
}

// sameArchive reports whether the archives are the same one.
func sameArchive(one, other store.Archive) bool {
	return one.ID == other.ID && one.Bytes == other.Bytes && one.At.Equal(other.At)
}

// recent reports whether at is a time of this test.
func recent(at *time.Time) bool {
	return at != nil && time.Since(*at) < time.Minute && time.Until(*at) < time.Minute
}

// A session records when its sandbox was put to sleep, and keeps that time
// for as long as the sandbox sleeps.
func (suite suite) testAsleepSince(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	id := st.session()
	set := func(from, to string) {
		t.Helper()
		if set, err := st.SetSandbox(ctx, id, from, to); err != nil || !set {
			t.Fatalf("SetSandbox(%q, %q) = %t, %v", from, to, set, err)
		}
	}
	record := func(state string) *time.Time {
		t.Helper()
		if err := st.SetSandboxState(ctx, id, "sandbox-1", state); err != nil {
			t.Fatal(err)
		}
		return st.another().read(id).AsleepSince
	}
	set("", "sandbox-1")
	if since := st.read(id).AsleepSince; since != nil {
		t.Errorf("a sandbox that is awake is asleep since %v", since)
	}
	first := record(store.SandboxAsleep)
	if !recent(first) {
		t.Fatalf("a sandbox that was put to sleep is asleep since %v, want now", first)
	}
	if again := record(store.SandboxAsleep); again == nil || !again.Equal(*first) {
		t.Errorf("a sandbox that is recorded as asleep again is asleep since %v, want %v as before", again, first)
	}
	sessions, err := st.SessionsWithSandbox(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].AsleepSince == nil || !sessions[0].AsleepSince.Equal(*first) {
		t.Errorf("SessionsWithSandbox = %+v, %v; want the session, asleep since %v", sessions, err, first)
	}
	for _, state := range []string{store.SandboxAwake, store.SandboxLost} {
		if since := record(state); since != nil {
			t.Errorf("a sandbox that is %s is asleep since %v", state, since)
		}
		if later := record(store.SandboxAsleep); !recent(later) || later.Before(*first) {
			t.Errorf("a sandbox that was put to sleep again is asleep since %v, want now", later)
		}
	}
	set("sandbox-1", "")
	if since := st.read(id).AsleepSince; since != nil {
		t.Errorf("a session without a sandbox is asleep since %v", since)
	}
}

// A Supervisor claims a session before it archives the home: one whose
// sandbox sleeps, that no turn uses or has used since the Supervisor looked,
// and that no other Supervisor has claimed lately.
func (suite suite) testClaimArchive(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session, sandbox := st.asleep()
	anHourAgo, inAnHour := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	claim := func(session uuid.UUID, sandbox string, turns int, since time.Time) bool {
		t.Helper()
		claimed, err := st.ClaimArchive(ctx, session, sandbox, turns, uuid.New(), since)
		if err != nil {
			t.Fatal(err)
		}
		return claimed
	}
	if claim(session, "another-sandbox", 1, anHourAgo) || claim(session, "", 1, anHourAgo) {
		t.Error("a session was claimed by the name of a sandbox that it does not have")
	}
	if claim(session, sandbox, 0, anHourAgo) || claim(session, sandbox, 2, anHourAgo) {
		t.Error("a session was claimed although it does not have that many turns")
	}
	if claim(uuid.New(), sandbox, 1, anHourAgo) {
		t.Error("a session that does not exist was claimed")
	}
	if read := st.read(session); read.Claimed || read.ClaimedAt != nil {
		t.Errorf("a session that was not claimed is claimed at %v", read.ClaimedAt)
	}

	if !claim(session, sandbox, 1, anHourAgo) {
		t.Fatal("a session whose sandbox sleeps was not claimed")
	}
	read := st.another().read(session)
	if !read.Claimed || !recent(read.ClaimedAt) || read.SandboxState != store.SandboxAsleep || read.Sandbox != sandbox {
		t.Errorf("claimed session = %+v, claimed at %v; want it claimed now, with its sandbox asleep", read, read.ClaimedAt)
	}
	sessions, err := st.SessionsWithSandbox(ctx)
	if err != nil || len(sessions) != 1 || !sessions[0].Claimed || !recent(sessions[0].ClaimedAt) {
		t.Errorf("SessionsWithSandbox = %+v, %v; want the session, claimed", sessions, err)
	}
	if claim(session, sandbox, 1, anHourAgo) {
		t.Error("a session that was claimed lately was claimed again")
	}
	if !claim(session, sandbox, 1, inAnHour) {
		t.Error("a session whose claim is old was not claimed again")
	}

	// Not while the sandbox is awake, a turn runs, or after the session has
	// ended.
	awake, awakeSandbox := st.asleep()
	busy, busySandbox := st.asleep()
	ended, endedSandbox := st.asleep()
	contended, name := st.asleep()
	if err := st.SetSandboxState(ctx, awake, awakeSandbox, store.SandboxAwake); err != nil {
		t.Fatal(err)
	}
	if claim(awake, awakeSandbox, 1, anHourAgo) {
		t.Error("a session whose sandbox is awake was claimed")
	}
	st.queue(busy)
	if claim(busy, busySandbox, 1, anHourAgo) || claim(busy, busySandbox, 2, anHourAgo) {
		t.Error("a session with an unfinished turn was claimed")
	}
	if _, err := st.EndSession(ctx, ended, ""); err != nil {
		t.Fatal(err)
	}
	if claim(ended, endedSandbox, 1, anHourAgo) {
		t.Error("a session that has ended was claimed")
	}

	// Of several Supervisors, one claims the session.
	supervisors := st.supervisors(8)
	var claimed atomic.Int32
	atOnce(len(supervisors), func(i int) {
		switch won, err := supervisors[i].ClaimArchive(ctx, contended, name, 1, uuid.New(), anHourAgo); {
		case err != nil:
			t.Error(err)
		case won:
			claimed.Add(1)
		}
	})
	if claimed.Load() != 1 {
		t.Errorf("%d of 8 concurrent ClaimArchive calls succeeded, want 1", claimed.Load())
	}
}

// Archiving records the archive as the home of the session, which then has
// no sandbox, unless the claim was lost or a turn came meanwhile.
func (suite suite) testArchiveSession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	anHourAgo, inAnHour := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	archive := store.Archive{ID: uuid.New(), Bytes: 123456789012}
	claimed := func() (session uuid.UUID, sandbox string, claim uuid.UUID) {
		t.Helper()
		session, sandbox = st.asleep()
		claim = uuid.New()
		if claimed, err := st.ClaimArchive(ctx, session, sandbox, 1, claim, anHourAgo); err != nil || !claimed {
			t.Fatalf("ClaimArchive = %t, %v", claimed, err)
		}
		return session, sandbox, claim
	}
	refused := func(what string, session, claim uuid.UUID, turns int, sandbox string) {
		t.Helper()
		if archived, err := st.ArchiveSession(ctx, session, claim, turns, archive); err != nil || archived {
			t.Errorf("ArchiveSession %s = %t, %v; want false", what, archived, err)
		}
		if read := st.read(session); read.Archive != nil || read.Sandbox != sandbox {
			t.Errorf("session %s has the archive %+v and the sandbox %q; want none and %q", what, read.Archive, read.Sandbox, sandbox)
		}
	}

	session, sandbox, claim := claimed()
	refused("under another claim", session, uuid.New(), 1, sandbox)
	refused("without a claim", session, uuid.Nil, 1, sandbox)
	if archived, err := st.ArchiveSession(ctx, uuid.New(), claim, 1, archive); err != nil || archived {
		t.Errorf("ArchiveSession of a session that does not exist = %t, %v; want false", archived, err)
	}
	if archived, err := st.ArchiveSession(ctx, session, claim, 1, archive); err != nil || !archived {
		t.Fatalf("ArchiveSession = %t, %v", archived, err)
	}
	read := st.another().read(session)
	if read.Archive == nil || read.Archive.ID != archive.ID || read.Archive.Bytes != archive.Bytes || !recent(&read.Archive.At) ||
		read.Sandbox != "" || read.SandboxState != store.SandboxArchived || read.AsleepSince != nil || read.Claimed {
		t.Errorf("archived session = %+v with the archive %+v; want the archive %+v of now, no sandbox and no claim", read, read.Archive, archive)
	}
	// Its former sandbox is not the session's any more.
	if err := st.SetSandboxState(ctx, session, sandbox, store.SandboxAwake); err != nil {
		t.Fatal(err)
	}
	if read := st.read(session); read.SandboxState != store.SandboxArchived {
		t.Errorf("the state of a sandbox that the archived session does not have was recorded: %q", read.SandboxState)
	}
	if sessions, err := st.SessionsWithSandbox(ctx); err != nil || len(sessions) != 0 {
		t.Errorf("SessionsWithSandbox = %+v, %v; want none", sessions, err)
	}
	if archived, err := st.ArchiveSession(ctx, session, claim, 1, store.Archive{ID: uuid.New(), Bytes: 1}); err != nil || archived {
		t.Errorf("ArchiveSession again = %t, %v; want false", archived, err)
	}
	if again := st.read(session); again.Archive == nil || !sameArchive(*again.Archive, *read.Archive) {
		t.Errorf("the archive is now %+v, want %+v as before", again.Archive, read.Archive)
	}
	if claimed, err := st.ClaimArchive(ctx, session, sandbox, 1, uuid.New(), inAnHour); err != nil || claimed {
		t.Errorf("ClaimArchive of an archived session = %t, %v; want false", claimed, err)
	}

	// A turn that came since the claim has used the home, also if it has
	// ended.
	session, sandbox, claim = claimed()
	turn := st.queue(session)
	refused("of a session with a queued turn", session, claim, 1, sandbox)
	refused("of a session with a queued turn that the Supervisor knows", session, claim, 2, sandbox)
	st.dispatch()
	if _, err := st.FollowTurn(ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	st.end(turn.ID, true, store.Failure{})
	refused("of a session that has had a turn since", session, claim, 1, sandbox)

	// A claim that another Supervisor has taken over.
	session, sandbox, claim = claimed()
	later := uuid.New()
	if claimed, err := st.ClaimArchive(ctx, session, sandbox, 1, later, inAnHour); err != nil || !claimed {
		t.Fatalf("ClaimArchive = %t, %v", claimed, err)
	}
	refused("under a claim that was taken over", session, claim, 1, sandbox)
	if archived, err := st.ArchiveSession(ctx, session, later, 1, archive); err != nil || !archived {
		t.Errorf("ArchiveSession under the later claim = %t, %v", archived, err)
	}

	session, sandbox, claim = claimed()
	if _, err := st.EndSession(ctx, session, ""); err != nil {
		t.Fatal(err)
	}
	refused("of a session that has ended", session, claim, 1, sandbox)
}

// A turn of an archived session gets a new sandbox, which is restoring until
// the Supervisor that claimed the session records the home as restored.
func (suite suite) testRestoreSession(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session, archive := st.archived()
	turn := st.running(session)
	if followed, err := st.FollowTurn(ctx, turn.TurnID); err != nil || followed.Sandbox != "" || followed.SandboxState != store.SandboxArchived {
		t.Errorf("FollowTurn = sandbox %q, %q, %v; want none, archived", followed.Sandbox, followed.SandboxState, err)
	}
	set := func(from, to string) {
		t.Helper()
		if set, err := st.SetSandbox(ctx, session, from, to); err != nil || !set {
			t.Fatalf("SetSandbox(%q, %q) = %t, %v", from, to, set, err)
		}
	}
	state := func(what, sandbox, state string, claimed bool) {
		t.Helper()
		read := st.another().read(session)
		if read.Sandbox != sandbox || read.SandboxState != state || read.Claimed != claimed ||
			read.Archive == nil || !sameArchive(*read.Archive, archive) {
			t.Errorf("%s, the session has the sandbox %q, %q, claimed %t, with the archive %+v; want %q, %q, claimed %t, with %+v",
				what, read.Sandbox, read.SandboxState, read.Claimed, read.Archive, sandbox, state, claimed, archive)
		}
	}

	one := uuid.New()
	got, claimed, err := st.ClaimRestore(ctx, session, one)
	if err != nil || !claimed || !sameArchive(got, archive) {
		t.Fatalf("ClaimRestore = %+v, %t, %v; want %+v", got, claimed, err, archive)
	}
	state("claimed", "", store.SandboxArchived, true)
	set("", "sandbox-2")
	state("with a new sandbox", "sandbox-2", store.SandboxRestoring, true)
	if followed, err := st.FollowTurn(ctx, turn.TurnID); err != nil || followed.Sandbox != "sandbox-2" || followed.SandboxState != store.SandboxRestoring {
		t.Errorf("FollowTurn = sandbox %q, %q, %v; want sandbox-2, restoring", followed.Sandbox, followed.SandboxState, err)
	}
	// What a look at the sandboxes finds does not end the restoring.
	if err := st.SetSandboxState(ctx, session, "sandbox-2", store.SandboxAwake); err != nil {
		t.Fatal(err)
	}
	state("after its sandbox was seen running", "sandbox-2", store.SandboxRestoring, true)
	sessions, err := st.SessionsWithSandbox(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].SandboxState != store.SandboxRestoring || sessions[0].Archive == nil || sessions[0].Archive.ID != archive.ID {
		t.Errorf("SessionsWithSandbox = %+v, %v; want the session, restoring from its archive", sessions, err)
	}
	// A sandbox that is given up leaves the session archived.
	set("sandbox-2", "")
	state("without the sandbox", "", store.SandboxArchived, true)
	set("", "sandbox-2")

	// Another Supervisor takes the restoring over. What the first one did
	// does not count.
	other := uuid.New()
	if got, claimed, err := st.another().ClaimRestore(ctx, session, other); err != nil || !claimed || !sameArchive(got, archive) {
		t.Fatalf("ClaimRestore by another Supervisor = %+v, %t, %v; want %+v", got, claimed, err, archive)
	}
	state("claimed by another Supervisor", "", store.SandboxArchived, true)
	set("", "sandbox-2")
	for what, restore := range map[string]func() (bool, error){
		"under the claim that was taken over": func() (bool, error) { return st.RestoreSession(ctx, session, one, "sandbox-2") },
		"without a claim":                     func() (bool, error) { return st.RestoreSession(ctx, session, uuid.Nil, "sandbox-2") },
		"into a sandbox that it does not have": func() (bool, error) {
			return st.RestoreSession(ctx, session, other, "sandbox-1")
		},
		"into no sandbox":                  func() (bool, error) { return st.RestoreSession(ctx, session, other, "") },
		"of a session that does not exist": func() (bool, error) { return st.RestoreSession(ctx, uuid.New(), other, "sandbox-2") },
	} {
		if restored, err := restore(); err != nil || restored {
			t.Errorf("RestoreSession %s = %t, %v; want false", what, restored, err)
		}
		state("after a restoring "+what, "sandbox-2", store.SandboxRestoring, true)
	}
	if restored, err := st.RestoreSession(ctx, session, other, "sandbox-2"); err != nil || !restored {
		t.Fatalf("RestoreSession = %t, %v", restored, err)
	}
	read := st.another().read(session)
	if read.Archive != nil || read.Sandbox != "sandbox-2" || read.SandboxState != store.SandboxAwake || read.Claimed {
		t.Errorf("restored session = %+v with the archive %+v; want sandbox-2 awake, no archive and no claim", read, read.Archive)
	}
	if restored, err := st.RestoreSession(ctx, session, other, "sandbox-2"); err != nil || restored {
		t.Errorf("RestoreSession again = %t, %v; want false", restored, err)
	}
	if _, claimed, err := st.ClaimRestore(ctx, session, uuid.New()); err != nil || claimed {
		t.Errorf("ClaimRestore of a session whose home is in its sandbox = %t, %v; want false", claimed, err)
	}
	// Its sandbox is like any other again.
	if err := st.SetSandboxState(ctx, session, "sandbox-2", store.SandboxAsleep); err != nil {
		t.Fatal(err)
	}
	if read := st.read(session); read.SandboxState != store.SandboxAsleep || !recent(read.AsleepSince) {
		t.Errorf("restored session = %+v, asleep since %v; want its sandbox asleep", read, read.AsleepSince)
	}

	if _, claimed, err := st.ClaimRestore(ctx, uuid.New(), uuid.New()); err != nil || claimed {
		t.Errorf("ClaimRestore of a session that does not exist = %t, %v; want false", claimed, err)
	}
	ended, _ := st.archived()
	if _, err := st.EndSession(ctx, ended, ""); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := st.ClaimRestore(ctx, ended, uuid.New()); err != nil || claimed {
		t.Errorf("ClaimRestore of a session that has ended = %t, %v; want false", claimed, err)
	}
}

// The archives that are left to delete are those of the sessions that have
// ended, and of the sessions that have been archived for too long. A session
// forgets its archive once it is deleted.
func (suite suite) testLeftArchives(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	never, always := time.Time{}, time.Now().Add(time.Hour)
	left := func(before time.Time, limit int) []uuid.UUID {
		t.Helper()
		sessions, err := st.another().LeftArchives(ctx, before, limit)
		if err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{}
		for _, session := range sessions {
			if session.Archive == nil {
				t.Errorf("LeftArchives returned the session %s without its archive", session.ID)
			}
			ids = append(ids, session.ID)
		}
		return ids
	}
	listed := func(got []uuid.UUID, want ...uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got := left(always, 10); len(got) != 0 {
		t.Errorf("LeftArchives of a database without archives = %v, want none", got)
	}

	old, oldArchive := st.archived()
	newer, _ := st.archived()
	ended, endedArchive := st.archived()
	asleep, _ := st.asleep()
	plain := st.session()
	for _, id := range []uuid.UUID{ended, asleep, plain} {
		session, err := st.EndSession(ctx, id, store.EndExpired)
		if err != nil {
			t.Fatal(err)
		}
		if id == ended && (session.Archive == nil || !sameArchive(*session.Archive, endedArchive)) {
			t.Errorf("EndSession = the archive %+v, want %+v", session.Archive, endedArchive)
		}
	}
	if got := left(never, 10); !listed(got, ended) {
		t.Errorf("LeftArchives = %v, want the session that has ended, %s", got, ended)
	}
	if got := left(oldArchive.At, 10); !listed(got, ended) {
		t.Errorf("LeftArchives before the first archive = %v, want the session that has ended, %s", got, ended)
	}
	if got := left(oldArchive.At.Add(time.Microsecond), 10); !listed(got, ended, old) {
		t.Errorf("LeftArchives after the first archive = %v, want %s and %s", got, ended, old)
	}
	if got := left(always, 10); !listed(got, ended, old, newer) {
		t.Errorf("LeftArchives of any age = %v, want %s, %s and %s", got, ended, old, newer)
	}
	if got := left(always, 1); !listed(got, ended, old) {
		t.Errorf("LeftArchives of any age, one of each = %v, want %s and %s", got, ended, old)
	}

	// A session forgets only the archive that it has, and only once it has
	// ended.
	for what, forget := range map[string]func() error{
		"another archive":                 func() error { return st.ForgetArchive(ctx, ended, uuid.New()) },
		"of a session that has not ended": func() error { return st.ForgetArchive(ctx, old, oldArchive.ID) },
		"of a session that does not exist": func() error {
			return st.ForgetArchive(ctx, uuid.New(), endedArchive.ID)
		},
	} {
		if err := forget(); err != nil {
			t.Errorf("ForgetArchive %s = %v", what, err)
		}
	}
	if got := left(always, 10); !listed(got, ended, old, newer) {
		t.Errorf("LeftArchives = %v, want %s, %s and %s as before", got, ended, old, newer)
	}
	if err := st.ForgetArchive(ctx, ended, endedArchive.ID); err != nil {
		t.Fatal(err)
	}
	if read := st.read(ended); read.Archive != nil || read.SandboxState != "" {
		t.Errorf("session that forgot its archive = %+v with the archive %+v; want neither an archive nor a state", read, read.Archive)
	}
	if got := left(always, 10); !listed(got, old, newer) {
		t.Errorf("LeftArchives = %v, want %s and %s", got, old, newer)
	}
}

// The sessions that an agent is deleted with come with their archives, and
// the archived sessions are counted with the sandboxes.
func (suite suite) testArchivesOfDeletedAgentsAndCounts(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	archived, archive := st.archived()
	restoring, _ := st.archived()
	if _, claimed, err := st.ClaimRestore(ctx, restoring, uuid.New()); err != nil || !claimed {
		t.Fatalf("ClaimRestore = %t, %v", claimed, err)
	}
	if set, err := st.SetSandbox(ctx, restoring, "", "sandbox-2"); err != nil || !set {
		t.Fatalf("SetSandbox = %t, %v", set, err)
	}
	st.asleep()
	gone, _ := st.archived()
	if _, err := st.EndSession(ctx, gone, ""); err != nil {
		t.Fatal(err)
	}
	counts, err := st.another().SandboxCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for state, sandboxes := range map[string]int{
		store.SandboxArchived: 1, store.SandboxRestoring: 1, store.SandboxAsleep: 1, store.SandboxAwake: 0, store.SandboxLost: 0,
	} {
		if counts[state] != sandboxes {
			t.Errorf("SandboxCounts = %v, want %d %s", counts, sandboxes, state)
		}
	}

	_, ended, err := st.DeleteAgent(ctx, st.read(archived).AgentID)
	if err != nil || len(ended) != 1 || ended[0].ID != archived || ended[0].Archive == nil || !sameArchive(*ended[0].Archive, archive) {
		t.Errorf("DeleteAgent ended %+v, %v; want the archived session with its archive %+v", ended, err, archive)
	}
}
