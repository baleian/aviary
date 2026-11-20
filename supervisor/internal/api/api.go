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

	"aviary/supervisor/events"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

const maxBodyBytes = 1 << 20

// defaultListed is how many sessions or turns a list holds if the request
// does not say, and maxListed is the most that it may ask for.
const (
	defaultListed = 20
	maxListed     = 100
)

type api struct {
	store  store.Store
	events *events.Streams
	driver *driver.Driver
	// keys seal the secrets that the API is given, before they are stored.
	keys *secret.Keys
	// stores are tried when a project registers one, and read when a
	// project says that a secret is in one.
	stores *secret.Stores
	// callers are who may use the API.
	callers Callers
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
	errSessionOver = &apiError{status: http.StatusConflict, Code: "session_ended", Message: "the session has ended"}
	errTurnEnded   = &apiError{status: http.StatusConflict, Code: "turn_ended", Message: "the turn has ended"}
	errAnswered    = &apiError{status: http.StatusConflict, Code: "approval_answered", Message: "the request for approval has its answer"}
	errNoModel     = &apiError{status: http.StatusConflict, Code: "agent_without_model", Message: "the agent of the session names no model: create an agent that names one"}
	errQueueFull   = &apiError{status: http.StatusTooManyRequests, Code: "queue_full", Message: "too many turns wait to run", retryAfter: 5}
	errInternal    = &apiError{status: http.StatusInternalServerError, Code: "internal", Message: "internal error"}
)

// New returns the handler of the API, for the callers that may use it. An
// event stream stays open for as long as its client does, so the streams end
// when closing is closed.
func New(st store.Store, ev *events.Streams, dr *driver.Driver, keys *secret.Keys, stores *secret.Stores, callers Callers, closing <-chan struct{}) http.Handler {
	a := &api{store: st, events: ev, driver: dr, keys: keys, stores: stores, callers: callers, closing: closing}
	mux := http.NewServeMux()
	// What a caller needs for each request. An administrator may do
	// everything. Another caller may do in a project what its members that
	// are the caller, or one of the groups of the caller, may do.
	mux.Handle("GET /v1/roles", handler(a.listRoles))
	mux.Handle("PUT /v1/roles/{name}", a.administrator(a.setRole))
	mux.Handle("POST /v1/projects", a.administrator(a.createProject))
	mux.Handle("GET /v1/projects", handler(a.listProjects))
	mux.Handle("GET /v1/projects/{pid}", a.inProject(projectView, a.getProject))
	mux.Handle("GET /v1/projects/{pid}/members", a.inProject(projectView, a.listMembers))
	mux.Handle("POST /v1/projects/{pid}/members", a.administrator(a.addMember))
	mux.Handle("PUT /v1/members/{mid}", a.administrator(a.setMember))
	mux.Handle("DELETE /v1/members/{mid}", a.administrator(a.removeMember))
	mux.Handle("POST /v1/projects/{pid}/secret-stores", a.inProject(connectionsManage, a.createSecretStore))
	mux.Handle("GET /v1/secret-stores/{store}", a.ofProject(projectView, "store", a.ofSecretStore, a.getSecretStore))
	mux.Handle("PUT /v1/secret-stores/{store}", a.ofProject(connectionsManage, "store", a.ofSecretStore, a.replaceSecretStore))
	mux.Handle("POST /v1/projects/{pid}/providers", a.inProject(connectionsManage, a.createProvider))
	mux.Handle("GET /v1/providers/{provider}", a.ofProject(projectView, "provider", a.ofProvider, a.getProvider))
	mux.Handle("PUT /v1/providers/{provider}", a.ofProject(connectionsManage, "provider", a.ofProvider, a.replaceProvider))
	mux.Handle("POST /v1/projects/{pid}/agents", a.inProject(agentsManage, a.createAgent))
	mux.Handle("GET /v1/projects/{pid}/agents", a.inProject(projectView, a.listAgents))
	mux.Handle("GET /v1/agents/{aid}", a.ofProject(projectView, "aid", a.ofAgent, a.getAgent))
	mux.Handle("POST /v1/agents/{aid}/sessions", a.ofProject(sessionsRun, "aid", a.ofAgent, a.createSession))
	// The sessions of the agent that the caller may read.
	mux.Handle("GET /v1/agents/{aid}/sessions", handler(a.listSessions))
	mux.Handle("GET /v1/sessions/{sid}", a.withSession(reads, a.getSession))
	mux.Handle("DELETE /v1/sessions/{sid}", a.withSession(ends, a.endSession))
	mux.Handle("POST /v1/sessions/{sid}/messages", a.withSession(sends, a.createMessage))
	mux.Handle("GET /v1/sessions/{sid}/turns", a.withSession(reads, a.listTurns))
	mux.Handle("GET /v1/turns/{tid}", a.withTurn(reads, a.getTurn))
	mux.Handle("POST /v1/turns/{tid}/cancel", a.withTurn(controls, a.cancelTurn))
	mux.Handle("POST /v1/turns/{tid}/approvals/{rid}", a.withTurn(controls, a.answerApproval))
	mux.Handle("GET /v1/turns/{tid}/events", a.withTurn(reads, a.streamEvents))
	// Kubernetes asks whether the Supervisor is up without saying who it is.
	all := http.NewServeMux()
	all.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	all.Handle("GET /v1/me", a.identified(handler(a.me)))
	all.Handle("/", a.identified(a.allowed(mux)))
	return all
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

// listProjects answers the projects of the caller: those of which the
// caller, or one of the groups of the caller, is a member. An administrator
// has them all.
func (a *api) listProjects(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	list := a.store.Projects
	if !who.admin {
		list = func(ctx context.Context) ([]store.Project, error) { return a.store.ProjectsOf(ctx, who.id, who.Groups) }
	}
	projects, err := list(r.Context())
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, struct {
		Projects []store.Project `json:"projects"`
	}{projects})
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
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request struct {
		Harness      string `json:"harness"`
		Instructions string `json:"instructions"`
		Approval     string `json:"approval"`
		Model        *struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if !a.driver.HasHarness(request.Harness) {
		return invalid("harness is not one of the configured harnesses")
	}
	if request.Approval != "" && request.Approval != driver.Ask {
		return invalid(`approval is not "ask"`)
	}
	if request.Model == nil {
		return invalid("model is required")
	}
	// The provider is one of the agent's own project.
	providerID, err := uuid.Parse(request.Model.Provider)
	if err != nil {
		return invalid("model.provider is not a provider of the project")
	}
	provider, err := a.store.Provider(r.Context(), providerID)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && provider.ProjectID != projectID):
		return invalid("model.provider is not a provider of the project")
	case err != nil:
		return err
	case request.Model.Name == "":
		return invalid("model.name is required")
	}
	agent, err := a.store.CreateAgent(r.Context(), projectID, store.Agent{
		Harness: request.Harness, Instructions: request.Instructions, Approval: request.Approval,
		Model: &store.Model{Provider: providerID, Name: request.Model.Name},
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

func (a *api) listAgents(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	agents, err := a.store.Agents(r.Context(), projectID)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, struct {
		Agents []store.Agent `json:"agents"`
	}{agents})
	return nil
}

func (a *api) createSession(w http.ResponseWriter, r *http.Request) error {
	agentID, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	session, err := a.store.CreateSession(r.Context(), agentID, callerOf(r).id)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, session)
	return nil
}

// listSessions lists the sessions of an agent that the caller may read:
// the caller's own with the permission to run sessions, and those of
// everyone with the one to view them all, or as an administrator.
func (a *api) listSessions(w http.ResponseWriter, r *http.Request) error {
	agentID, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	limit, err := queryNumber(r, "limit", defaultListed, maxListed)
	if err != nil {
		return err
	}
	before, err := queryID(r, "before", uuid.Max)
	if err != nil {
		return err
	}
	agent, err := a.store.Agent(r.Context(), agentID)
	if err != nil {
		return err
	}
	// The caller reads the sessions of everyone, or their own, or none.
	by, readable := uuid.Nil, true
	if who := callerOf(r); !who.admin {
		held, err := a.held(r, agent.ProjectID)
		if err != nil {
			return err
		}
		if !slices.Contains(held, sessionsViewAll) {
			by, readable = who.id, slices.Contains(held, sessionsRun)
		}
	}
	sessions := []store.Session{}
	if readable {
		if sessions, err = a.store.Sessions(r.Context(), agentID, by, before, limit); err != nil {
			return err
		}
	}
	respond(w, http.StatusOK, struct {
		Sessions []store.Session `json:"sessions"`
	}{sessions})
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

// endSession ends the session, and has its sandbox deleted with all that
// the agent had in it. What was said in the session stays.
func (a *api) endSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	session, err := a.driver.EndSession(r.Context(), id)
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
	// An agent from before agents named their models has none, and its
	// turns would have no model to ask.
	session, err := a.store.Session(r.Context(), sessionID)
	if err != nil {
		return err
	}
	if agent, err := a.store.Agent(r.Context(), session.AgentID); err != nil {
		return err
	} else if agent.Model == nil {
		return errNoModel
	}
	turn, err := a.driver.Accept(r.Context(), sessionID, callerOf(r).id, request.Text)
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
	limit, err := queryNumber(r, "limit", defaultListed, maxListed)
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
	answered, err := a.store.AnswerApproval(r.Context(), turnID, answer.ID, answer.OptionID, callerOf(r).id)
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
// reply is 200. A turn that runs is told to stop and ends when its harness
// has stopped, so the reply is 202 with the turn to follow. Cancelling a turn
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
	case errors.Is(err, store.ErrSessionEnded):
		failure = errSessionOver
	case errors.Is(err, store.ErrQueueFull):
		failure = errQueueFull
	case errors.Is(err, store.ErrMemberExists):
		failure = errMemberExists
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		failure = errInternal
	}
	if failure.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(failure.retryAfter))
	}
	if failure.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
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

// queryID reads a query parameter that is an ID. A request without it gets
// the ID absent.
func queryID(r *http.Request, name string, absent uuid.UUID) (uuid.UUID, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return absent, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, invalid(name + " must be an ID")
	}
	return id, nil
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
