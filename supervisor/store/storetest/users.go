package storetest

import (
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

func (suite suite) testUsers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	// see is what the identity provider tells of a user with a request.
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
	// What the provider tells of a user changes, one thing at a time, and
	// the user is the same.
	if shown := see("subject-a", "alice@example.com", "Alice Kim"); shown.ID != alice.ID || shown.Username != "alice@example.com" {
		t.Errorf("a user that is shown as something else is %+v, and was %+v", shown, alice)
	}
	if renamed := see("subject-a", "alice.kim@example.com", "Alice Kim"); renamed.ID != alice.ID || renamed.Username != "alice.kim@example.com" || !renamed.CreatedAt.Equal(alice.CreatedAt) {
		t.Errorf("a user that the provider renamed is %+v, and was %+v", renamed, alice)
	}
	// A user that is given no username keeps the one that it has, also when
	// something else of the user changes.
	if kept := see("subject-a", "", "Alice Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("a user that is given no username is %+v", kept)
	}
	if kept := see("subject-a", "", "A. Kim"); kept.ID != alice.ID || kept.Username != "alice.kim@example.com" {
		t.Errorf("a user that is given no username and is shown as something else is %+v", kept)
	}

	// A user without a username, and one with a name that differs only in
	// its case: usernames are compared as they are written.
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

	// The provider gives the username of one user to another: the other
	// has it from then on, and is not the user that had it.
	taker := see("subject-c", "alice.kim@example.com", "A new Alice")
	if taker.ID == alice.ID || taker.Username != "alice.kim@example.com" {
		t.Errorf("a user that was given the username of another is %+v", taker)
	}
	if left := see("subject-a", "", "Alice Kim"); left.ID != alice.ID || left.Username != "" {
		t.Errorf("the user whose username was given to another is %+v", left)
	}
	// And gives it back.
	if back := see("subject-a", "alice.kim@example.com", "Alice Kim"); back.ID != alice.ID || back.Username != "alice.kim@example.com" {
		t.Errorf("the user that was given its username back is %+v", back)
	}
	if without := see("subject-c", "", "A new Alice"); without.ID != taker.ID || without.Username != "" {
		t.Errorf("the user that had the username meanwhile is %+v", without)
	}
}

// Supervisors see a user for the first time at once: there is one user.
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
