// Package api serves the Supervisor's public HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/store"
)

const maxBodyBytes = 1 << 20

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
}

func (e *apiError) Error() string { return e.Message }

var (
	errNotFound    = &apiError{http.StatusNotFound, "not_found", "no such resource"}
	errSessionBusy = &apiError{http.StatusConflict, "session_busy", "the session has a turn that has not finished"}
	errInternal    = &apiError{http.StatusInternalServerError, "internal", "internal error"}
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
	mux.Handle("GET /v1/turns/{tid}", handler(a.getTurn))
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
		Harness string `json:"harness"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if !a.driver.HasHarness(request.Harness) {
		return invalid("harness is not one of the configured harnesses")
	}
	agent, err := a.store.CreateAgent(r.Context(), projectID, request.Harness)
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
	turn, position, err := a.store.CreateTurn(r.Context(), sessionID, request.Text)
	if err != nil {
		return err
	}
	// The turn is accepted once it is stored. The event only reports that, so
	// failing to publish it does not fail the request.
	if err := a.events.AppendQueued(r.Context(), turn.ID, position); err != nil {
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
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		failure = errInternal
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

func invalid(message string) error {
	return &apiError{http.StatusBadRequest, "invalid_request", message}
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
