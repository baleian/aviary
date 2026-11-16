// Package api serves the Supervisor's public HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/store"
)

const maxBodyBytes = 1 << 20

// defaultTurns is how many turns a list holds if the request does not say,
// and maxTurns is the most that it may ask for.
const (
	defaultTurns = 20
	maxTurns     = 100
)

type api struct {
	store   *store.Store
	events  *events.Streams
	driver  *driver.Driver
	closing <-chan struct{}
}

type apiError struct {
	status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	// retryAfter is how many seconds a client should wait before it sends
	// the request again, if it helps to send it again.
	retryAfter int
}

func (e *apiError) Error() string { return e.Message }

var (
	errNotFound    = &apiError{status: http.StatusNotFound, Code: "not_found", Message: "no such resource"}
	errSessionBusy = &apiError{status: http.StatusConflict, Code: "session_busy", Message: "the session has a turn that has not finished"}
	errTurnEnded   = &apiError{status: http.StatusConflict, Code: "turn_ended", Message: "the turn has ended"}
	errAnswered    = &apiError{status: http.StatusConflict, Code: "approval_answered", Message: "the request for approval has its answer"}
	errQueueFull   = &apiError{status: http.StatusTooManyRequests, Code: "queue_full", Message: "too many turns wait to run", retryAfter: 5}
	errInternal    = &apiError{status: http.StatusInternalServerError, Code: "internal", Message: "internal error"}
)

// New returns the handler of the API. An event stream stays open for as long
// as its client does, so the streams end when closing is closed.
func New(st *store.Store, ev *events.Streams, dr *driver.Driver, closing <-chan struct{}) http.Handler {
	a := &api{store: st, events: ev, driver: dr, closing: closing}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.Handle("POST /v1/projects", handler(a.createProject))
	mux.Handle("GET /v1/projects/{pid}", handler(a.getProject))
	mux.Handle("POST /v1/projects/{pid}/agents", handler(a.createAgent))
	mux.Handle("GET /v1/agents/{aid}", handler(a.getAgent))
	mux.Handle("POST /v1/agents/{aid}/sessions", handler(a.createSession))
	mux.Handle("GET /v1/sessions/{sid}", handler(a.getSession))
	mux.Handle("POST /v1/sessions/{sid}/messages", handler(a.createMessage))
	mux.Handle("GET /v1/sessions/{sid}/turns", handler(a.listTurns))
	mux.Handle("GET /v1/turns/{tid}", handler(a.getTurn))
	mux.Handle("POST /v1/turns/{tid}/cancel", handler(a.cancelTurn))
	mux.Handle("POST /v1/turns/{tid}/approvals/{rid}", handler(a.answerApproval))
	mux.Handle("GET /v1/turns/{tid}/events", handler(a.streamEvents))
	return mux
}

func (a *api) createProject(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Name == "" {
		return invalid("name is required")
	}
	project, err := a.store.CreateProject(r.Context(), request.Name)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, project)
	return nil
}

func (a *api) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	project, err := a.store.Project(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, project)
	return nil
}

func (a *api) createAgent(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	var request struct {
		Harness      string          `json:"harness"`
		Instructions string          `json:"instructions"`
		Resources    json.RawMessage `json:"resources"`
		Approval     string          `json:"approval"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if !a.driver.HasHarness(request.Harness) {
		return invalid("harness is not one of the configured harnesses")
	}
	resources, err := driver.AgentResources(request.Resources)
	if err != nil {
		return invalid(err.Error())
	}
	if request.Approval != "" && request.Approval != driver.Ask {
		return invalid(`approval is not "ask"`)
	}
	agent, err := a.store.CreateAgent(r.Context(), projectID, store.Agent{
		Harness: request.Harness, Instructions: request.Instructions, Resources: resources, Approval: request.Approval,
	})
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, agent)
	return nil
}

func (a *api) getAgent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	agent, err := a.store.Agent(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, agent)
	return nil
}

func (a *api) createSession(w http.ResponseWriter, r *http.Request) error {
	agentID, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	session, err := a.store.CreateSession(r.Context(), agentID)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, session)
	return nil
}

func (a *api) getSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	session, err := a.store.Session(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, session)
	return nil
}

// createMessage accepts a message as the session's next turn. The turn runs
// later, so the reply is 202 with the turn to follow.
func (a *api) createMessage(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	var request struct {
		Text string `json:"text"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Text == "" {
		return invalid("text is required")
	}
	turn, err := a.driver.Accept(r.Context(), sessionID, request.Text)
	if err != nil {
		return err
	}
	// The turn is accepted once it is stored. The event only reports that, so
	// failing to publish it does not fail the request. A turn that was
	// dispatched in the meantime has no position to be told.
	err = a.store.AnnounceQueue(r.Context(), func(queued []uuid.UUID) error {
		if position := slices.Index(queued, turn.ID); position >= 0 {
			return a.events.AppendQueued(r.Context(), turn.ID, position+1)
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "publish turn event", "turn", turn.ID, "event", events.TurnQueued, "error", err)
	}
	a.driver.Wake()
	respond(w, http.StatusAccepted, turn)
	return nil
}

func (a *api) getTurn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	turn, err := a.store.Turn(r.Context(), id)
	if err != nil {
		return err
	}
	records, err := a.records(r.Context(), []store.Turn{turn})
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, records[0])
	return nil
}

// listTurns returns the turns of a session, newest first. The query
// parameter limit is how many, and before is the sequence number of a turn:
// the list then holds the turns that came before it. That is how a client
// reads on from the last turn of a list. The first turn of a session has the
// sequence number 1.
func (a *api) listTurns(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	limit, err := queryNumber(r, "limit", defaultTurns, maxTurns)
	if err != nil {
		return err
	}
	before, err := queryNumber(r, "before", math.MaxInt32, math.MaxInt32)
	if err != nil {
		return err
	}
	if _, err := a.store.Session(r.Context(), sessionID); err != nil {
		return err
	}
	turns, err := a.store.Turns(r.Context(), sessionID, before, limit)
	if err != nil {
		return err
	}
	records, err := a.records(r.Context(), turns)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, struct {
		Turns []turnRecord `json:"turns"`
	}{records})
	return nil
}

// turnRecord is a turn with what the agent said and did in it. The items
// are kept when the turn ends: until then a client follows the turn's events.
// A turn that has not ended has the requests of its harness for approval
// that wait for a person, as the approval.requested events told them.
type turnRecord struct {
	store.Turn
	Items     []json.RawMessage `json:"items"`
	Approvals []json.RawMessage `json:"approvals,omitempty"`
}

func (a *api) records(ctx context.Context, turns []store.Turn) ([]turnRecord, error) {
	ids := make([]uuid.UUID, len(turns))
	for i, turn := range turns {
		ids[i] = turn.ID
	}
	items, err := a.store.TurnItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	records := make([]turnRecord, len(turns))
	for i, turn := range turns {
		// A turn without items has an empty list of them.
		records[i] = turnRecord{Turn: turn, Items: append([]json.RawMessage{}, items[turn.ID]...)}
		if turn.EndedAt == nil {
			if records[i].Approvals, err = a.store.WaitingApprovals(ctx, turn.ID); err != nil {
				return nil, err
			}
		}
	}
	return records, nil
}

// answerApproval takes the answer of a person to a request of a turn's
// harness for approval: one of the options that the harness gave with the
// request. The answer is passed on to the runner of the turn, and the
// approval.resolved event tells when the harness has it. The first answer to
// a request stands.
func (a *api) answerApproval(w http.ResponseWriter, r *http.Request) error {
	turnID, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	var chosen struct {
		OptionID string `json:"option_id"`
	}
	if err := decode(w, r, &chosen); err != nil {
		return err
	}
	answer := struct {
		ID       string `json:"id"`
		OptionID string `json:"option_id"`
	}{r.PathValue("rid"), chosen.OptionID}
	asked, err := a.store.Approval(r.Context(), turnID, answer.ID)
	if err != nil {
		return err
	}
	var request struct {
		Options []struct {
			ID string `json:"id"`
		} `json:"options"`
	}
	if err := json.Unmarshal(asked.Request, &request); err != nil {
		return err
	}
	offered := slices.ContainsFunc(request.Options, func(option struct {
		ID string `json:"id"`
	}) bool {
		return option.ID == answer.OptionID
	})
	if !offered {
		return invalid("option_id must be the id of one of the options of the request")
	}
	answered, err := a.store.AnswerApproval(r.Context(), turnID, answer.ID, answer.OptionID)
	if err != nil {
		return err
	}
	if !answered {
		if turn, err := a.store.Turn(r.Context(), turnID); err != nil {
			return err
		} else if turn.EndedAt != nil {
			return errTurnEnded
		}
		return errAnswered
	}
	// The answer is recorded. If it cannot be announced, the Supervisor that
	// the runner is connected to finds it a little later.
	if err := a.events.AnnounceAnswer(r.Context(), turnID); err != nil {
		slog.ErrorContext(r.Context(), "announce the answer to a request for approval", "turn", turnID, "error", err)
	}
	respond(w, http.StatusAccepted, answer)
	return nil
}

// cancelTurn cancels a turn. A turn that waits to run ends at once, and the
// reply is 200. A turn that runs is told to stop and ends when its pod has
// stopped, so the reply is 202 with the turn to follow. Cancelling a turn
// that was cancelled before changes nothing and succeeds.
func (a *api) cancelTurn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	turn, unended, err := a.store.CancelTurn(r.Context(), id)
	switch {
	case err != nil:
		return err
	case !unended && turn.State != store.TurnCancelled:
		return errTurnEnded
	case !unended:
	case turn.State == store.TurnCancelled:
		a.driver.Ended(r.Context(), turn)
		a.driver.QueueMoved()
	default:
		// The request is recorded. If it cannot be announced, the Supervisor
		// that the runner is connected to finds it a little later.
		if err := a.events.AnnounceCancel(r.Context(), turn.ID); err != nil {
			slog.ErrorContext(r.Context(), "announce the cancelled turn", "turn", turn.ID, "error", err)
		}
		respond(w, http.StatusAccepted, turn)
		return nil
	}
	respond(w, http.StatusOK, turn)
	return nil
}

// handler turns the error a handler returns into the error response.
type handler func(w http.ResponseWriter, r *http.Request) error

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := h(w, r)
	if err == nil {
		return
	}
	var failure *apiError
	switch {
	case errors.As(err, &failure):
	case errors.Is(err, store.ErrNotFound):
		failure = errNotFound
	case errors.Is(err, store.ErrSessionBusy):
		failure = errSessionBusy
	case errors.Is(err, store.ErrQueueFull):
		failure = errQueueFull
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		failure = errInternal
	}
	if failure.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(failure.retryAfter))
	}
	respond(w, failure.status, failure)
}

// pathID reads an ID from the request path. An ID that is not a UUID cannot
// name anything, so it is reported as not found.
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, errNotFound
	}
	return id, nil
}

// queryNumber reads a query parameter that is a number from 1 to most. A
// request without it gets the number absent.
func queryNumber(r *http.Request, name string, absent, most int) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return absent, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > most {
		return 0, invalid(name + " must be a number from 1 to " + strconv.Itoa(most))
	}
	return number, nil
}

func invalid(message string) error {
	return &apiError{status: http.StatusBadRequest, Code: "invalid_request", Message: message}
}

func decode(w http.ResponseWriter, r *http.Request, into any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return invalid("request body is not the JSON this endpoint expects")
	}
	return nil
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, so a failed write can only be logged.
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}
