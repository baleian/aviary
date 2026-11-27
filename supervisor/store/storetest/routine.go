package storetest

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// Routine takes a session of its own through a turn whose message carries
// an attachment, and does on the way what the Supervisors do for every
// message, every request of a sandbox and every sweep. A plugin's test runs it on a database with many ended sessions and
// turns, to check that this work does not read through them.
func Routine(t *testing.T, st store.Store) {
	t.Helper()
	s := supervisor{Store: st, t: t}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	session := s.session()
	must("ExpectAttachments", s.ExpectAttachments(ctx, session))
	file, err := s.CreateAttachment(ctx, store.Attachment{ID: uuid.New(), SessionID: session, Name: "notes.txt", MediaType: "text/plain", Size: 1})
	must("CreateAttachment", err)
	unsent, err := s.CreateAttachment(ctx, store.Attachment{ID: uuid.New(), SessionID: session, Name: "unsent.txt", MediaType: "text/plain", Size: 1})
	must("CreateAttachment", err)
	turn, _, err := s.CreateTurn(ctx, session, uuid.Nil, "hello", []uuid.UUID{file.ID}, 500)
	must("CreateTurn", err)
	_, err = s.Attachment(ctx, file.ID)
	must("Attachment", err)
	_, err = s.TurnAttachments(ctx, []uuid.UUID{turn.ID})
	must("TurnAttachments", err)
	_, err = s.UnsentAttachments(ctx, time.Now().Add(time.Hour), 100)
	must("UnsentAttachments", err)
	_, err = s.DeleteAttachment(ctx, unsent.ID)
	must("DeleteAttachment", err)
	_, err = s.LeftAttachments(ctx, time.Now(), 100)
	must("LeftAttachments", err)
	must("ForgetAttachments", s.ForgetAttachments(ctx, session))
	must("AnnounceQueue", s.AnnounceQueue(ctx, func([]uuid.UUID) error { return nil }))
	_, err = s.ExpireTurns(ctx, time.Hour)
	must("ExpireTurns", err)
	s.dispatch()
	_, err = s.FollowTurn(ctx, turn.ID)
	must("FollowTurn", err)
	_, err = s.SetSandbox(ctx, session, "", "sandbox-1")
	must("SetSandbox", err)
	_, _, err = s.StartTurn(ctx, turn.ID)
	must("StartTurn", err)
	_, err = s.SessionTurn(ctx, session)
	must("SessionTurn", err)
	_, err = s.LastTurn(ctx, session)
	must("LastTurn", err)
	must("UseSandbox", s.UseSandbox(ctx, session, "sandbox-1"))
	_, err = s.IdleSandboxes(ctx, 10, true)
	must("IdleSandboxes", err)

	must("AddApproval", s.AddApproval(ctx, turn.ID, "1", []byte(`{"options": [{"id": "once", "kind": "allow_once"}]}`)))
	_, err = s.WaitingApprovals(ctx, turn.ID)
	must("WaitingApprovals", err)
	_, err = s.WaitedForAnswers(ctx, turn.ID)
	must("WaitedForAnswers", err)
	_, err = s.AnswerApproval(ctx, turn.ID, "1", "once", uuid.Nil)
	must("AnswerApproval", err)
	_, err = s.Answers(ctx, nil)
	must("Answers", err)
	_, err = s.Answers(ctx, &turn.ID)
	must("Answers of the turn", err)
	_, _, _, err = s.ResolveApproval(ctx, turn.ID, "1", "selected")
	must("ResolveApproval", err)

	_, err = s.DispatchedTurns(ctx)
	must("DispatchedTurns", err)
	_, err = s.UnfinishedTurns(ctx)
	must("UnfinishedTurns", err)
	_, err = s.SessionsWithSandbox(ctx)
	must("SessionsWithSandbox", err)
	_, err = s.SandboxCounts(ctx)
	must("SandboxCounts", err)
	_, err = s.Turns(ctx, session, math.MaxInt32, 1)
	must("Turns", err)
	_, err = s.LastTurns(ctx, []uuid.UUID{session})
	must("LastTurns", err)
	_, err = s.SessionMcpServers(ctx, []uuid.UUID{session})
	must("SessionMcpServers", err)

	must("SetFinished", s.SetFinished(ctx, turn.ID, "end_turn", nil))
	_, ended, err := s.EndTurn(ctx, turn.ID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) {
		return []json.RawMessage{json.RawMessage(`{"type": "message", "text": "done"}`)}, nil
	})
	if err != nil || !ended {
		t.Fatalf("EndTurn = %t, %v", ended, err)
	}
	_, err = s.TurnHarness(ctx, turn.ID)
	must("TurnHarness", err)
	_, err = s.TurnItems(ctx, []uuid.UUID{turn.ID})
	must("TurnItems", err)

	must("SetSandboxState", s.SetSandboxState(ctx, session, "sandbox-1", store.SandboxAsleep))
	_, err = s.EndSession(ctx, session, "")
	must("EndSession", err)
}
