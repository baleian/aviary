package storetest

import (
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

func (suite suite) testUsers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	// see calls UserOf as an authenticated request does, and checks the user.
	see := func(subject, username, displayName string) store.User {
		t.Helper()
		user, err := st.UserOf(ctx, subject, username, displayName)
		if err != nil {
			t.Fatal(err)
		}
		if user.ID == uuid.Nil || user.Subject != subject || user.DisplayName != displayName || user.CreatedAt.IsZero() {
			t.Fatalf("the user of %q, shown as %q, is %+v", subject, displayName, user)
		}
		return user
	}

	alice := see("subject-a", "alice@example.com", "Alice")
	if alice.Username != "alice@example.com" {
		t.Errorf("a user that was seen first is %+v", alice)
	}
	if again := see("subject-a", "alice@example.com", "Alice"); again != alice {
		t.Errorf("a user that was seen again is %+v, not %+v", again, alice)
	}
	// The user stays the same while its details change, one at a time.
	if shown := see("subject-a", "alice@example.com", "Alice Kim"); shown.ID != alice.ID || shown.Username != "alice@example.com" {
		t.Errorf("a user that is shown as something else is %+v, and was %+v", shown, alice)
	}
	if renamed := see("subject-a", "alice.kim@example.com", "Alice Kim"); renamed.ID != alice.ID || renamed.Username != "alice.kim@example.com" || !renamed.CreatedAt.Equal(alice.CreatedAt) {
		t.Errorf("a user that the provider renamed is %+v, and was %+v", renamed, alice)
	}
	// A user given no username keeps its own, also when its display name
	// changes.
	if kept := see("subject-a", "", "Alice Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("a user that is given no username is %+v", kept)
	}
	if kept := see("subject-a", "", "A. Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("a user that is given no username and is shown as something else is %+v", kept)
	}

	// A user without a username, and one whose subject and username differ
	// only in case: both are case-sensitive.
	nameless := see("subject-b", "", "")
	if nameless.ID == alice.ID || nameless.Username != "" {
		t.Errorf("a user without a username is %+v", nameless)
	}
	if other := see("Subject-A", "Alice.Kim@example.com", "Another Alice"); other.ID == alice.ID || other.Username != "Alice.Kim@example.com" {
		t.Errorf("a user whose subject and username differ in their case is %+v", other)
	}
	if still := see("subject-a", "", "Alice Kim"); still.Username != "alice.kim@example.com" {
		t.Errorf("after another user was seen, the user is %+v", still)
	}

	// A username that the provider gives to another user moves to that user.
	taker := see("subject-c", "alice.kim@example.com", "A new Alice")
	if taker.ID == alice.ID || taker.Username != "alice.kim@example.com" {
		t.Errorf("a user that was given the username of another is %+v", taker)
	}
	if left := see("subject-a", "", "Alice Kim"); left.ID != alice.ID || left.Username != "" {
		t.Errorf("the user whose username was given to another is %+v", left)
	}
	// And back to the first user.
	if back := see("subject-a", "alice.kim@example.com", "Alice Kim"); back.ID != alice.ID || back.Username != "alice.kim@example.com" {
		t.Errorf("the user that was given its username back is %+v", back)
	}
	if without := see("subject-c", "", "A new Alice"); without.ID != taker.ID || without.Username != "" {
		t.Errorf("the user that had the username meanwhile is %+v", without)
	}
}

// Concurrent first sightings of a user create one user.
func (suite suite) testSupervisorsSeeAUserFirstTogether(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	supervisors := st.supervisors(8)
	users := make([]store.User, len(supervisors))
	atOnce(len(supervisors), func(i int) {
		user, err := supervisors[i].UserOf(ctx, "subject-a", "alice@example.com", "Alice")
		if err != nil {
			t.Error(err)
		}
		users[i] = user
	})
	for _, user := range users {
		if user.ID == uuid.Nil || user.ID != users[0].ID || user.Username != "alice@example.com" || user.DisplayName != "Alice" {
			t.Fatalf("the Supervisors saw the users %+v", users)
		}
	}
}

// Sessions and turns record the user who created them, or uuid.Nil, and the
// origin of a running turn names the user who sent its message.
func (suite suite) testSessionOfAUser(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	nobodys, err := st.Session(ctx, st.session())
	if err != nil || nobodys.CreatedBy != uuid.Nil {
		t.Fatalf("a session that nobody created is %+v, with the error %v", nobodys, err)
	}
	created, err := st.CreateSession(ctx, nobodys.AgentID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.Session(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedBy != user.ID || read.CreatedBy != user.ID || read.ID == nobodys.ID {
		t.Errorf("the session of the user %s was created as %+v and is read as %+v", user.ID, created, read)
	}

	// A turn records its sender, whoever owns the session.
	other, err := st.UserOf(ctx, "subject-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	senders := map[uuid.UUID]uuid.UUID{}
	for session, sender := range map[uuid.UUID]uuid.UUID{created.ID: other.ID, nobodys.ID: user.ID, st.session(): uuid.Nil} {
		sent, _, err := st.CreateTurn(ctx, session, sender, "hello", 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		senders[sent.ID] = sender
		one, err := st.Turn(ctx, sent.ID)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := st.Turns(ctx, session, 2, 10)
		if err != nil || len(listed) != 1 {
			t.Fatalf("the turns of the session are %+v, with the error %v", listed, err)
		}
		if sent.CreatedBy != sender || one.CreatedBy != sender || listed[0].CreatedBy != sender {
			t.Errorf("the turn that %s sent was created as %+v, is read as %+v and is listed as %+v", sender, sent, one, listed[0])
		}
	}

	dispatches, err := st.DispatchTurns(ctx, 50)
	if err != nil || len(dispatches) != 3 {
		t.Fatalf("the turns were dispatched as %+v, with the error %v", dispatches, err)
	}
	for _, dispatch := range dispatches {
		followed, err := st.FollowTurn(ctx, dispatch.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		sender, sent := senders[dispatch.TurnID]
		subject := map[uuid.UUID]string{user.ID: "subject-a", other.ID: "subject-b"}[sender]
		if origin, err := st.SessionTurn(ctx, followed.SessionID); err != nil || !sent || origin.UserID != sender || origin.Subject != subject {
			t.Errorf("a request of the running turn that %s, the subject %q, sent comes from %+v, with the error %v", sender, subject, origin, err)
		}
	}
}
