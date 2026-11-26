package storetest

import (
	"encoding/json"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

func (suite suite) testConcurrentAnswerApproval(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatch := st.running(st.session())
	if err := st.AddApproval(ctx, dispatch.TurnID, "1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	supervisors := st.supervisors(8)
	var kept atomic.Int32
	atOnce(len(supervisors), func(i int) {
		answered, err := supervisors[i].AnswerApproval(ctx, dispatch.TurnID, "1", strconv.Itoa(i), uuid.Nil)
		if err != nil {
			t.Error(err)
		}
		if answered {
			kept.Add(1)
		}
	})
	if answers, err := st.Answers(ctx, &dispatch.TurnID); err != nil || len(answers) != 1 || kept.Load() != 1 {
		t.Errorf("%d of 8 concurrent answers accepted, Answers = %v, want one: %v", kept.Load(), answers, err)
	}
}

// WaitedForAnswers adds up how long the turn's requests waited for their
// answers.
func (suite suite) testWaitedForAnswers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	turn := st.running(st.session()).TurnID
	waited := func() time.Duration {
		t.Helper()
		waited, err := st.WaitedForAnswers(ctx, turn)
		if err != nil {
			t.Fatal(err)
		}
		return waited
	}
	if waited := waited(); waited != 0 {
		t.Errorf("WaitedForAnswers of a turn that asked nothing = %v, want 0", waited)
	}
	for _, id := range []string{"1", "2"} {
		if err := st.AddApproval(ctx, turn, id, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if answered, err := st.AnswerApproval(ctx, turn, "1", "once", uuid.Nil); err != nil || !answered {
		t.Fatalf("AnswerApproval = %t, %v", answered, err)
	}
	// The answered request waited, and the other one waits on.
	before := waited()
	time.Sleep(20 * time.Millisecond)
	if after := waited(); before < 40*time.Millisecond || after-before < 20*time.Millisecond || after-before > time.Minute {
		t.Errorf("WaitedForAnswers = %v and then %v, want both requests to count, and the one that waits to go on", before, after)
	}
	// A request that the end of its turn resolves waits no more.
	if _, _, _, err := st.ResolveApproval(ctx, turn, "2", "cancelled"); err != nil {
		t.Fatal(err)
	}
	resolved := waited()
	time.Sleep(20 * time.Millisecond)
	if again := waited(); again != resolved {
		t.Errorf("WaitedForAnswers after the last request was resolved = %v and then %v, want no change", resolved, again)
	}
}

func (suite suite) testApprovals(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatch := st.running(st.session())
	turn := dispatch.TurnID
	waiting := func() []json.RawMessage {
		t.Helper()
		requests, err := st.WaitingApprovals(ctx, turn)
		if err != nil {
			t.Fatal(err)
		}
		return requests
	}
	answers := func(of *store.QueuedTurn) []store.Answer {
		t.Helper()
		var answers []store.Answer
		var err error
		if of == nil {
			answers, err = st.Answers(ctx, nil)
		} else {
			answers, err = st.Answers(ctx, &of.TurnID)
		}
		if err != nil {
			t.Fatal(err)
		}
		return answers
	}

	// The input of a tool call may contain U+0000, which a database may be
	// unable to store.
	const first, second = `{"id": "1", "note": "a\u0000b"}`, `{"id": "2"}`
	if err := st.AddApproval(ctx, turn, "1", []byte(first)); err != nil {
		t.Fatal(err)
	}
	if err := st.AddApproval(ctx, turn, "2", []byte(second)); err != nil {
		t.Fatal(err)
	}
	// A runner reports a request again after it lost its Supervisor.
	if err := st.AddApproval(ctx, turn, "1", []byte(`{"id": "another"}`)); err != nil {
		t.Fatal(err)
	}
	if waiting := waiting(); !keepsAll(t, waiting, first, second) {
		t.Errorf("WaitingApprovals = %s, want requests 1 and 2", waiting)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !keeps(t, asked.Request, first) || asked.Answered || asked.Resolved {
		t.Errorf("waiting request = %+v: %v", asked, err)
	}
	if _, err := st.Approval(ctx, turn, "3"); err != store.ErrNotFound {
		t.Errorf("Approval of an unreported request = %v, want ErrNotFound", err)
	}
	if err := st.AddApproval(ctx, uuid.New(), "1", []byte(`{}`)); err == nil {
		t.Error("AddApproval succeeded for a nonexistent turn")
	}

	// The first answer stands, and records the user who gave it.
	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || asked.Answered || asked.AnsweredBy != uuid.Nil {
		t.Errorf("unanswered request = %+v: %v", asked, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "1", "once", user.ID); err != nil || !answered {
		t.Fatalf("AnswerApproval = %t, want true: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "1", "never", uuid.Nil); err != nil || answered {
		t.Errorf("second AnswerApproval = %t, want false: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "3", "once", uuid.Nil); err != nil || answered {
		t.Errorf("AnswerApproval of a nonexistent request = %t, want false: %v", answered, err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Answered || asked.Resolved || asked.AnsweredBy != user.ID {
		t.Errorf("request answered by %s = %+v: %v", user.ID, asked, err)
	}
	// An answered request no longer waits, and its answer is pending delivery.
	if waiting := waiting(); !keepsAll(t, waiting, second) {
		t.Errorf("WaitingApprovals after an answer = %s, want request 2", waiting)
	}
	other := st.running(st.session())
	on := []store.Answer{{TurnID: turn, ID: "1", OptionID: "once"}}
	if all, mine, others := answers(nil), answers(&dispatch), answers(&other); !slices.Equal(all, on) || !slices.Equal(mine, on) || len(others) != 0 {
		t.Errorf("Answers = %v, for the turn %v, for another turn %v", all, mine, others)
	}

	// The harness receives the answer. Resolving again changes nothing.
	resolved, waited, once, err := st.ResolveApproval(ctx, turn, "1", "selected")
	if err != nil || !once || waited < 0 || waited > time.Minute || !keeps(t, resolved.Request, first) {
		t.Fatalf("ResolveApproval = %s, waited %s, first %t: %v", resolved.Request, waited, once, err)
	}
	if _, _, once, err := st.ResolveApproval(ctx, turn, "1", "cancelled"); err != nil || once {
		t.Errorf("request resolved twice: %v", err)
	}
	if _, _, once, err := st.ResolveApproval(ctx, turn, "3", "selected"); err != nil || once {
		t.Errorf("nonexistent request resolved: %v", err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Resolved {
		t.Errorf("resolved request = %+v: %v", asked, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("Answers after ResolveApproval = %v, want none", answers)
	}
	// A resolved request takes no answer.
	if _, _, _, err := st.ResolveApproval(ctx, turn, "2", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "2", "once", uuid.Nil); err != nil || answered {
		t.Errorf("AnswerApproval of a resolved request = %t, want false: %v", answered, err)
	}

	// Nor does a request of an ended turn, and the turn's undelivered answers
	// are no longer listed.
	if err := st.AddApproval(ctx, other.TurnID, "1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.AddApproval(ctx, other.TurnID, "2", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if answered, err := st.AnswerApproval(ctx, other.TurnID, "1", "once", uuid.Nil); err != nil || !answered {
		t.Fatalf("AnswerApproval for the other turn = %t, want true: %v", answered, err)
	}
	st.end(other.TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if answered, err := st.AnswerApproval(ctx, other.TurnID, "2", "once", uuid.Nil); err != nil || answered {
		t.Errorf("AnswerApproval for an ended turn = %t, want false: %v", answered, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("Answers after the turn ended = %v, want none", answers)
	}
}
