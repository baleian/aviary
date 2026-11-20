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

func (suite suite) testAnswerApprovalKeepsOneOfTheAnswersGivenAtOnce(t *testing.T) {
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
		t.Errorf("of eight answers given at once, %d were kept, and those on their way are %v: %v", kept.Load(), answers, err)
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
	answers := func(of *store.Dispatch) []store.Answer {
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
		t.Errorf("the requests that wait are %s", waiting)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !keeps(t, asked.Request, first) || asked.Answered || asked.Resolved {
		t.Errorf("the request that waits is %+v: %v", asked, err)
	}
	if _, err := st.Approval(ctx, turn, "3"); err != store.ErrNotFound {
		t.Errorf("a request that the runner did not report was read with %v", err)
	}
	if err := st.AddApproval(ctx, uuid.New(), "1", []byte(`{}`)); err == nil {
		t.Error("a request of a turn that there is not was kept")
	}

	// The first answer stands, and records the user who gave it.
	user, err := st.UserOf(ctx, "subject-a", "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || asked.Answered || asked.AnsweredBy != uuid.Nil {
		t.Errorf("a request without an answer is %+v: %v", asked, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "1", "once", user.ID); err != nil || !answered {
		t.Fatalf("the request was answered %t: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "1", "never", uuid.Nil); err != nil || answered {
		t.Errorf("the request was answered a second time %t: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "3", "once", uuid.Nil); err != nil || answered {
		t.Errorf("a request that there is not was answered %t: %v", answered, err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Answered || asked.Resolved || asked.AnsweredBy != user.ID {
		t.Errorf("the request that the user %s answered is %+v: %v", user.ID, asked, err)
	}
	// An answered request no longer waits, and its answer is pending delivery.
	if waiting := waiting(); !keepsAll(t, waiting, second) {
		t.Errorf("the requests that wait after an answer are %s", waiting)
	}
	other := st.running(st.session())
	on := []store.Answer{{TurnID: turn, ID: "1", OptionID: "once"}}
	if all, mine, others := answers(nil), answers(&dispatch), answers(&other); !slices.Equal(all, on) || !slices.Equal(mine, on) || len(others) != 0 {
		t.Errorf("the answers on their way are %v, those of the turn %v and those of another %v", all, mine, others)
	}

	// The harness receives the answer. Resolving again changes nothing.
	resolved, waited, once, err := st.ResolveApproval(ctx, turn, "1", "selected")
	if err != nil || !once || waited < 0 || waited > time.Minute || !keeps(t, resolved.Request, first) {
		t.Fatalf("the request %s was resolved after %s, for the first time %t: %v", resolved.Request, waited, once, err)
	}
	if _, _, once, err := st.ResolveApproval(ctx, turn, "1", "cancelled"); err != nil || once {
		t.Errorf("the request was resolved a second time: %v", err)
	}
	if _, _, once, err := st.ResolveApproval(ctx, turn, "3", "selected"); err != nil || once {
		t.Errorf("a request that there is not was resolved: %v", err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Resolved {
		t.Errorf("the resolved request is %+v: %v", asked, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("an answer that the harness has is still on its way: %v", answers)
	}
	// A resolved request takes no answer.
	if _, _, _, err := st.ResolveApproval(ctx, turn, "2", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "2", "once", uuid.Nil); err != nil || answered {
		t.Errorf("a request that was resolved was answered %t: %v", answered, err)
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
		t.Fatalf("the request of the other turn was answered %t: %v", answered, err)
	}
	st.end(other.TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if answered, err := st.AnswerApproval(ctx, other.TurnID, "2", "once", uuid.Nil); err != nil || answered {
		t.Errorf("a request of a turn that has ended was answered %t: %v", answered, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("the answers of a turn that has ended are still on their way: %v", answers)
	}
}
