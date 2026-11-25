package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/store"
)

// announcing is a Store that announces every session that is changed through
// it, or whose turns are, so that the Supervisors can send the session to the
// clients that watch it. Two changes are left to the one that follows at
// once: dispatching a turn, which is then followed, and recording how its
// harness finished, after which the turn ends.
type announcing struct {
	store.Store
	events *events.Streams
}

// changed announces the session. If that fails, clients see the change when
// they read the session again.
func (a announcing) changed(ctx context.Context, sessionID uuid.UUID) {
	if err := a.events.AnnounceChange(ctx, sessionID); err != nil {
		slog.ErrorContext(ctx, "announce session change", "session", sessionID, "error", err)
	}
}

// turnChanged announces the session of the turn.
func (a announcing) turnChanged(ctx context.Context, turnID uuid.UUID) {
	turn, err := a.Turn(ctx, turnID)
	if err != nil {
		slog.ErrorContext(ctx, "read changed turn", "turn", turnID, "error", err)
		return
	}
	a.changed(ctx, turn.SessionID)
}

func (a announcing) DeleteAgent(ctx context.Context, id uuid.UUID) (store.Agent, []store.Session, error) {
	agent, ended, err := a.Store.DeleteAgent(ctx, id)
	for _, session := range ended {
		a.changed(ctx, session.ID)
	}
	return agent, ended, err
}

func (a announcing) CreateSession(ctx context.Context, agentID, by uuid.UUID, title string, mcp map[uuid.UUID]bool) (store.Session, error) {
	session, err := a.Store.CreateSession(ctx, agentID, by, title, mcp)
	if err == nil {
		a.changed(ctx, session.ID)
	}
	return session, err
}

func (a announcing) SetSessionMcpServer(ctx context.Context, sessionID, serverID uuid.UUID, enabled bool) (store.Session, error) {
	session, err := a.Store.SetSessionMcpServer(ctx, sessionID, serverID, enabled)
	if err == nil {
		a.changed(ctx, sessionID)
	}
	return session, err
}

func (a announcing) ResetSessionMcpServers(ctx context.Context, sessionID uuid.UUID) (store.Session, error) {
	session, err := a.Store.ResetSessionMcpServers(ctx, sessionID)
	if err == nil {
		a.changed(ctx, sessionID)
	}
	return session, err
}

func (a announcing) SetSessionTitle(ctx context.Context, id uuid.UUID, title string) (store.Session, error) {
	session, err := a.Store.SetSessionTitle(ctx, id, title)
	if err == nil {
		a.changed(ctx, id)
	}
	return session, err
}

func (a announcing) SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error) {
	set, err := a.Store.SetSandbox(ctx, sessionID, from, to)
	if set {
		a.changed(ctx, sessionID)
	}
	return set, err
}

func (a announcing) SetSandboxState(ctx context.Context, sessionID uuid.UUID, sandbox, state string) error {
	err := a.Store.SetSandboxState(ctx, sessionID, sandbox, state)
	if err == nil {
		a.changed(ctx, sessionID)
	}
	return err
}

func (a announcing) ArchiveSession(ctx context.Context, sessionID, claim uuid.UUID, turns int, archive store.Archive) (bool, error) {
	archived, err := a.Store.ArchiveSession(ctx, sessionID, claim, turns, archive)
	if archived {
		a.changed(ctx, sessionID)
	}
	return archived, err
}

func (a announcing) ClaimRestore(ctx context.Context, sessionID, claim uuid.UUID) (store.Archive, bool, error) {
	archive, claimed, err := a.Store.ClaimRestore(ctx, sessionID, claim)
	if claimed {
		a.changed(ctx, sessionID)
	}
	return archive, claimed, err
}

func (a announcing) RestoreSession(ctx context.Context, sessionID, claim uuid.UUID, sandbox string) (bool, error) {
	restored, err := a.Store.RestoreSession(ctx, sessionID, claim, sandbox)
	if restored {
		a.changed(ctx, sessionID)
	}
	return restored, err
}

func (a announcing) ForgetArchive(ctx context.Context, sessionID, archive uuid.UUID) error {
	err := a.Store.ForgetArchive(ctx, sessionID, archive)
	if err == nil {
		a.changed(ctx, sessionID)
	}
	return err
}

func (a announcing) EndSession(ctx context.Context, id uuid.UUID, reason string) (store.Session, error) {
	session, err := a.Store.EndSession(ctx, id, reason)
	if err == nil {
		a.changed(ctx, id)
	}
	return session, err
}

func (a announcing) CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, attachments []uuid.UUID, maxWaiting, maxRunning int) (store.Turn, int, error) {
	turn, waiting, err := a.Store.CreateTurn(ctx, sessionID, by, text, attachments, maxWaiting, maxRunning)
	if err == nil {
		a.changed(ctx, sessionID)
	}
	return turn, waiting, err
}

func (a announcing) CancelTurn(ctx context.Context, id uuid.UUID) (store.Turn, bool, error) {
	turn, unended, err := a.Store.CancelTurn(ctx, id)
	if unended {
		a.changed(ctx, turn.SessionID)
	}
	return turn, unended, err
}

func (a announcing) ExpireTurns(ctx context.Context, wait time.Duration) ([]store.Turn, error) {
	turns, err := a.Store.ExpireTurns(ctx, wait)
	for _, turn := range turns {
		a.changed(ctx, turn.SessionID)
	}
	return turns, err
}

func (a announcing) FollowTurn(ctx context.Context, turnID uuid.UUID) (store.FollowedTurn, error) {
	turn, err := a.Store.FollowTurn(ctx, turnID)
	if err == nil {
		a.changed(ctx, turn.SessionID)
	}
	return turn, err
}

func (a announcing) StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error) {
	startup, first, err := a.Store.StartTurn(ctx, turnID)
	if first {
		a.turnChanged(ctx, turnID)
	}
	return startup, first, err
}

func (a announcing) EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure,
	items func(context.Context) ([]json.RawMessage, error)) (store.Turn, bool, error) {
	turn, ended, err := a.Store.EndTurn(ctx, turnID, succeeded, failure, items)
	if ended {
		a.changed(ctx, turn.SessionID)
	}
	return turn, ended, err
}

func (a announcing) AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error {
	err := a.Store.AddApproval(ctx, turnID, id, request)
	if err == nil {
		a.turnChanged(ctx, turnID)
	}
	return err
}

func (a announcing) ResolveApproval(ctx context.Context, turnID uuid.UUID, id, outcome string) (store.Approval, time.Duration, bool, error) {
	asked, waited, first, err := a.Store.ResolveApproval(ctx, turnID, id, outcome)
	if first {
		a.turnChanged(ctx, turnID)
	}
	return asked, waited, first, err
}

func (a announcing) AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error) {
	answered, err := a.Store.AnswerApproval(ctx, turnID, id, optionID, by)
	if answered {
		a.turnChanged(ctx, turnID)
	}
	return answered, err
}
