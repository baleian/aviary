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
			t.Fatalf("UserOf(%q) with display name %q = %+v", subject, displayName, user)
		}
		return user
	}

	alice := see("subject-a", "alice@example.com", "Alice")
	if alice.Username != "alice@example.com" {
		t.Errorf("new user = %+v", alice)
	}
	if again := see("subject-a", "alice@example.com", "Alice"); again != alice {
		t.Errorf("same user again = %+v, want %+v", again, alice)
	}
	// The user stays the same while its details change, one at a time.
	if shown := see("subject-a", "alice@example.com", "Alice Kim"); shown.ID != alice.ID || shown.Username != "alice@example.com" {
		t.Errorf("user with a new display name = %+v, was %+v", shown, alice)
	}
	if renamed := see("subject-a", "alice.kim@example.com", "Alice Kim"); renamed.ID != alice.ID || renamed.Username != "alice.kim@example.com" || !renamed.CreatedAt.Equal(alice.CreatedAt) {
		t.Errorf("user with a new username = %+v, was %+v", renamed, alice)
	}
	// A user given no username keeps its own, also when its display name
	// changes.
	if kept := see("subject-a", "", "Alice Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("user given no username = %+v", kept)
	}
	if kept := see("subject-a", "", "A. Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("user given no username and a new display name = %+v", kept)
	}

	// A user without a username, and one whose subject and username differ
	// only in case: both are case-sensitive.
	nameless := see("subject-b", "", "")
	if nameless.ID == alice.ID || nameless.Username != "" {
		t.Errorf("user without a username = %+v", nameless)
	}
	if other := see("Subject-A", "Alice.Kim@example.com", "Another Alice"); other.ID == alice.ID || other.Username != "Alice.Kim@example.com" {
		t.Errorf("user whose subject and username differ only in case = %+v", other)
	}
	if still := see("subject-a", "", "Alice Kim"); still.Username != "alice.kim@example.com" {
		t.Errorf("first user after the case-differing user = %+v", still)
	}

	// A username that the provider gives to another user moves to that user.
	taker := see("subject-c", "alice.kim@example.com", "A new Alice")
	if taker.ID == alice.ID || taker.Username != "alice.kim@example.com" {
		t.Errorf("user given another user's username = %+v", taker)
	}
	if left := see("subject-a", "", "Alice Kim"); left.ID != alice.ID || left.Username != "" {
		t.Errorf("user whose username moved to another user = %+v", left)
	}
	// And back to the first user.
	if back := see("subject-a", "alice.kim@example.com", "Alice Kim"); back.ID != alice.ID || back.Username != "alice.kim@example.com" {
		t.Errorf("user given its username back = %+v", back)
	}
	if without := see("subject-c", "", "A new Alice"); without.ID != taker.ID || without.Username != "" {
		t.Errorf("user that lost the username = %+v", without)
	}
}

// Concurrent first sightings of a user create one user.
func (suite suite) testConcurrentUserOf(t *testing.T) {
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
			t.Fatalf("concurrent UserOf = %+v, want one user", users)
		}
	}
}

// Sessions and turns record the user who created them, or uuid.Nil, and the
// origin of a running turn names the user who sent its message.
func (suite suite) testCreatedBy(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	nobodys, err := st.Session(ctx, st.session())
	if err != nil || nobodys.CreatedBy != uuid.Nil {
		t.Fatalf("unauthenticated session = %+v, %v", nobodys, err)
	}
	created, err := st.CreateSession(ctx, nobodys.AgentID, user.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.Session(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedBy != user.ID || read.CreatedBy != user.ID || read.ID == nobodys.ID {
		t.Errorf("session of user %s: created %+v, read %+v", user.ID, created, read)
	}

	// A turn records its sender, whoever owns the session.
	other, err := st.UserOf(ctx, "subject-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	senders := map[uuid.UUID]uuid.UUID{}
	for session, sender := range map[uuid.UUID]uuid.UUID{created.ID: other.ID, nobodys.ID: user.ID, st.session(): uuid.Nil} {
		sent, _, err := st.CreateTurn(ctx, session, sender, "hello", nil, 0)
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
			t.Fatalf("Turns = %+v, %v, want one turn", listed, err)
		}
		if sent.CreatedBy != sender || one.CreatedBy != sender || listed[0].CreatedBy != sender {
			t.Errorf("turn sent by %s: created %+v, read %+v, listed %+v", sender, sent, one, listed[0])
		}
	}

	dispatches, err := st.NextTurns(ctx, 50)
	if err != nil || len(dispatches) != 3 {
		t.Fatalf("NextTurns = %+v, %v, want 3 turns", dispatches, err)
	}
	for _, dispatch := range dispatches {
		if dispatched, err := st.DispatchTurn(ctx, dispatch.TurnID); err != nil || !dispatched {
			t.Fatalf("DispatchTurn = %t, %v", dispatched, err)
		}
		followed, err := st.FollowTurn(ctx, dispatch.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		sender, sent := senders[dispatch.TurnID]
		subject := map[uuid.UUID]string{user.ID: "subject-a", other.ID: "subject-b"}[sender]
		if origin, err := st.SessionTurn(ctx, followed.SessionID); err != nil || !sent || origin.UserID != sender || origin.Subject != subject {
			t.Errorf("SessionTurn for the turn sent by %s (subject %q) = %+v, %v", sender, subject, origin, err)
		}
	}
}
