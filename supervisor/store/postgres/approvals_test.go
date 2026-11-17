package postgres_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/store"
)

func TestApprovals(t *testing.T) {
	t.Parallel()
	st := open(t)
	dispatch, _ := st.running(st.session())
	turn := dispatch.TurnID
	waiting := func() []string {
		t.Helper()
		requests, err := st.WaitingApprovals(ctx, turn)
		if err != nil {
			t.Fatal(err)
		}
		waiting := []string{}
		for _, request := range requests {
			waiting = append(waiting, string(request))
		}
		return waiting
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

	// The character that PostgreSQL cannot keep may be in what a tool is
	// called with.
	if err := st.AddApproval(ctx, turn, "1", []byte(`{"id": "1", "note": "a\u0000b"}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.AddApproval(ctx, turn, "2", []byte(`{"id": "2"}`)); err != nil {
		t.Fatal(err)
	}
	// A runner that lost its Supervisor reports a request again.
	if err := st.AddApproval(ctx, turn, "1", []byte(`{"id": "another"}`)); err != nil {
		t.Fatal(err)
	}
	if waiting := waiting(); !slices.Equal(waiting, []string{`{"id": "1", "note": "a` + "�" + `b"}`, `{"id": "2"}`}) {
		t.Errorf("the requests that wait are %q", waiting)
	}
	if _, err := st.Approval(ctx, turn, "3"); err != store.ErrNotFound {
		t.Errorf("a request that the runner did not report was read with %v", err)
	}

	// The first answer stands.
	if answered, err := st.AnswerApproval(ctx, turn, "1", "once"); err != nil || !answered {
		t.Fatalf("the request was answered %t: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "1", "never"); err != nil || answered {
		t.Errorf("the request was answered a second time %t: %v", answered, err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "3", "once"); err != nil || answered {
		t.Errorf("a request that there is not was answered %t: %v", answered, err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Answered || asked.Resolved {
		t.Errorf("the answered request is %+v: %v", asked, err)
	}
	// It waits for a person no longer, and its answer is on its way.
	if waiting := waiting(); !slices.Equal(waiting, []string{`{"id": "2"}`}) {
		t.Errorf("the requests that wait after an answer are %q", waiting)
	}
	other, _ := st.running(st.session())
	on := []store.Answer{{TurnID: turn, ID: "1", OptionID: "once"}}
	if all, mine, others := answers(nil), answers(&dispatch), answers(&other); !slices.Equal(all, on) || !slices.Equal(mine, on) || len(others) != 0 {
		t.Errorf("the answers on their way are %v, those of the turn %v and those of another %v", all, mine, others)
	}

	// The harness has its answer. A report that comes again changes nothing.
	resolved, waited, first, err := st.ResolveApproval(ctx, turn, "1", "selected")
	if err != nil || !first || waited < 0 || waited > time.Minute || !strings.Contains(string(resolved.Request), `"note"`) {
		t.Fatalf("the request %s was resolved after %s, for the first time %t: %v", resolved.Request, waited, first, err)
	}
	if _, _, first, err := st.ResolveApproval(ctx, turn, "1", "cancelled"); err != nil || first {
		t.Errorf("the request was resolved a second time: %v", err)
	}
	if _, _, first, err := st.ResolveApproval(ctx, turn, "3", "selected"); err != nil || first {
		t.Errorf("a request that there is not was resolved: %v", err)
	}
	if asked, err := st.Approval(ctx, turn, "1"); err != nil || !asked.Resolved {
		t.Errorf("the resolved request is %+v: %v", asked, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("an answer that the harness has is still on its way: %v", answers)
	}
	// A request that the harness waits for no longer takes no answer.
	if _, _, _, err := st.ResolveApproval(ctx, turn, "2", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if answered, err := st.AnswerApproval(ctx, turn, "2", "once"); err != nil || answered {
		t.Errorf("a request that was resolved was answered %t: %v", answered, err)
	}

	// Nor does one of a turn that has ended, and what was answered to it is
	// passed on no more.
	if err := st.AddApproval(ctx, other.TurnID, "1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.AddApproval(ctx, other.TurnID, "2", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if answered, err := st.AnswerApproval(ctx, other.TurnID, "1", "once"); err != nil || !answered {
		t.Fatalf("the request of the other turn was answered %t: %v", answered, err)
	}
	st.end(other.TurnID, false, store.Failure{Reason: store.FailurePodLost})
	if answered, err := st.AnswerApproval(ctx, other.TurnID, "2", "once"); err != nil || answered {
		t.Errorf("a request of a turn that has ended was answered %t: %v", answered, err)
	}
	if answers := answers(nil); len(answers) != 0 {
		t.Errorf("the answers of a turn that has ended are still on their way: %v", answers)
	}
}
