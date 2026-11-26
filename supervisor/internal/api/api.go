// Package api serves the Supervisor's public HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/objects"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

const maxBodyBytes = 1 << 20

// Default and maximum page sizes of the list endpoints.
const (
	defaultListed = 20
	maxListed     = 100
)

type api struct {
	store  store.Store
	events *events.Streams
	driver *driver.Driver
	// keys encrypt secrets before they are stored.
	keys *secret.Keys
	// stores checks the secret stores that projects register, and the keys
	// that providers reference in them.
	stores  *secret.Stores
	callers Callers
	// resolver reads the secrets of providers and MCP servers, which the API
	// sends them with when it asks them for their models and tools.
	// outbound sends those requests, and follows no redirect: the answer of
	// the endpoint is the result.
	resolver *credentials.Resolver
	outbound *http.Client
	changes  *changes
	closing  <-chan struct{}
}

type apiError struct {
	status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	// retryAfter, if positive, is sent as the Retry-After header, in
	// seconds.
	retryAfter int
}

func (e *apiError) Error() string { return e.Message }

var (
	errNotFound    = &apiError{status: http.StatusNotFound, Code: "not_found", Message: "no such resource"}
	errSlugTaken   = &apiError{status: http.StatusConflict, Code: "slug_taken", Message: "the slug is already taken"}
	errSessionBusy = &apiError{status: http.StatusConflict, Code: "session_busy", Message: "the session has an unfinished turn"}
	errSessionOver = &apiError{status: http.StatusConflict, Code: "session_ended", Message: "the session has ended"}
	errAgentGone   = &apiError{status: http.StatusConflict, Code: "agent_deleted", Message: "the agent is deleted"}
	errTurnEnded   = &apiError{status: http.StatusConflict, Code: "turn_ended", Message: "the turn has ended"}
	errAnswered    = &apiError{status: http.StatusConflict, Code: "approval_answered", Message: "the approval request already has an answer"}
	errQueueFull   = &apiError{status: http.StatusTooManyRequests, Code: "queue_full", Message: "too many turns are queued; retry later", retryAfter: 5}
	errInternal    = &apiError{status: http.StatusInternalServerError, Code: "internal", Message: "internal error"}
	errNoBody      = &apiError{status: http.StatusBadRequest, Code: "invalid_request", Message: "request body is required"}
)

// slugForm matches a slug: a lowercase DNS label (RFC 1035).
var slugForm = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// New returns the API handler. Event streams stay open until their clients
// disconnect or ctx is done. reach asks model providers and MCP servers for
// their models and tools, and follows no redirect.
func New(ctx context.Context, st store.Store, ev *events.Streams, dr *driver.Driver, keys *secret.Keys, stores *secret.Stores, callers Callers, reach *http.Client) http.Handler {
	a := &api{
		store: st, events: ev, driver: dr, keys: keys, stores: stores, callers: callers,
		resolver: &credentials.Resolver{Store: st, Keys: keys, Stores: stores}, outbound: reach,
		changes: newChanges(), closing: ctx.Done(),
	}
	go a.sendChanges(ctx)
	mux := http.NewServeMux()
	// Admins may do everything. Other callers have, in a project, the
	// permissions of the members that are them or one of their groups. A
	// route names the permissions that it needs there.
	mux.Handle("GET /v1/roles", handler(a.listRoles))
	mux.Handle("PUT /v1/roles/{name}", a.administrator(a.setRole))
	mux.Handle("GET /v1/harnesses", handler(a.listHarnesses))
	mux.Handle("GET /v1/limits", handler(a.getLimits))
	mux.Handle("POST /v1/projects", a.administrator(a.createProject))
	mux.Handle("GET /v1/projects", handler(a.listProjects))
	mux.Handle("GET /v1/projects/{pid}", a.inProject(needs{projectView}, a.getProject))
	// Every member may watch. What a member is sent depends on which
	// sessions it may read.
	mux.Handle("GET /v1/projects/{pid}/events", a.inProject(needs{projectView}, a.streamChanges))
	mux.Handle("GET /v1/projects/{pid}/members", a.inProject(needs{projectView}, a.listMembers))
	mux.Handle("POST /v1/projects/{pid}/members", a.administrator(a.addMember))
	mux.Handle("PUT /v1/members/{mid}", a.administrator(a.setMember))
	mux.Handle("DELETE /v1/members/{mid}", a.administrator(a.removeMember))
	mux.Handle("POST /v1/projects/{pid}/secret-stores", a.inProject(needs{connectionsManage}, a.createSecretStore))
	mux.Handle("GET /v1/projects/{pid}/secret-stores", a.inProject(needs{projectView}, a.listSecretStores))
	mux.Handle("GET /v1/secret-stores/{store}", a.ofProject(needs{projectView}, "store", a.ofSecretStore, a.getSecretStore))
	mux.Handle("PUT /v1/secret-stores/{store}", a.ofProject(needs{connectionsManage}, "store", a.ofSecretStore, a.replaceSecretStore))
	mux.Handle("POST /v1/projects/{pid}/providers", a.inProject(needs{connectionsManage}, a.createProvider))
	mux.Handle("POST /v1/projects/{pid}/providers/models", a.inProject(needs{connectionsManage}, a.endpointModels))
	mux.Handle("GET /v1/projects/{pid}/providers", a.inProject(needs{projectView}, a.listProviders))
	mux.Handle("GET /v1/providers/{provider}", a.ofProject(needs{projectView}, "provider", a.ofProvider, a.getProvider))
	mux.Handle("PUT /v1/providers/{provider}", a.ofProject(needs{connectionsManage}, "provider", a.ofProvider, a.replaceProvider))
	mux.Handle("GET /v1/providers/{provider}/models", a.ofProject(needs{agentsManage}, "provider", a.ofProvider, a.providerModels))
	mux.Handle("POST /v1/projects/{pid}/mcp-servers", a.inProject(needs{connectionsManage}, a.createMcpServer))
	mux.Handle("GET /v1/projects/{pid}/mcp-servers", a.inProject(needs{projectView}, a.listMcpServers))
	mux.Handle("POST /v1/projects/{pid}/mcp-servers/tools", a.inProject(needs{connectionsManage}, a.serverTools))
	mux.Handle("GET /v1/mcp-servers/{server}", a.ofProject(needs{projectView}, "server", a.ofMcpServer, a.getMcpServer))
	mux.Handle("PUT /v1/mcp-servers/{server}", a.ofProject(needs{connectionsManage}, "server", a.ofMcpServer, a.replaceMcpServer))
	mux.Handle("DELETE /v1/mcp-servers/{server}", a.ofProject(needs{connectionsManage}, "server", a.ofMcpServer, a.deleteMcpServer))
	mux.Handle("POST /v1/mcp-servers/{server}/tools", a.ofProject(needs{connectionsManage}, "server", a.ofMcpServer, a.refreshTools))
	mux.Handle("POST /v1/projects/{pid}/agents", a.inProject(needs{agentsManage}, a.createAgent))
	mux.Handle("GET /v1/projects/{pid}/agents", a.inProject(needs{projectView}, a.listAgents))
	mux.Handle("GET /v1/agents/{aid}", a.ofProject(needs{projectView}, "aid", a.ofAgent, a.getAgent))
	mux.Handle("PUT /v1/agents/{aid}", a.ofProject(needs{agentsManage}, "aid", a.ofAgent, a.replaceAgent))
	// Deleting an agent ends the sessions of every member, so it also needs
	// the permission to end them.
	mux.Handle("DELETE /v1/agents/{aid}", a.ofProject(needs{agentsManage, sessionsControlAll}, "aid", a.ofAgent, a.deleteAgent))
	mux.Handle("POST /v1/agents/{aid}/sessions", a.ofProject(needs{sessionsRun}, "aid", a.ofAgent, a.createSession))
	// Every member may list them. What is listed depends on which sessions
	// the member may read.
	mux.Handle("GET /v1/agents/{aid}/sessions", a.ofProject(needs{projectView}, "aid", a.ofAgent, a.listSessions))
	mux.Handle("GET /v1/sessions/{sid}", a.withSession(reads, a.getSession))
	mux.Handle("PATCH /v1/sessions/{sid}", a.withSession(retitles, a.updateSession))
	mux.Handle("DELETE /v1/sessions/{sid}", a.withSession(ends, a.endSession))
	mux.Handle("POST /v1/sessions/{sid}/messages", a.withSession(sends, a.createMessage))
	mux.Handle("POST /v1/sessions/{sid}/attachments", a.withSession(sends, a.uploadAttachment))
	mux.Handle("GET /v1/attachments/{attachment}", a.withAttachment(reads, a.downloadAttachment))
	mux.Handle("PUT /v1/sessions/{sid}/mcp-servers/{server}", a.withSession(sends, a.setSessionMcpServer))
	mux.Handle("DELETE /v1/sessions/{sid}/mcp-servers", a.withSession(sends, a.resetSessionMcpServers))
	mux.Handle("GET /v1/sessions/{sid}/turns", a.withSession(reads, a.listTurns))
	mux.Handle("GET /v1/turns/{tid}", a.withTurn(reads, a.getTurn))
	mux.Handle("POST /v1/turns/{tid}/cancel", a.withTurn(controls, a.cancelTurn))
	mux.Handle("POST /v1/turns/{tid}/approvals/{rid}", a.withTurn(controls, a.answerApproval))
	mux.Handle("GET /v1/turns/{tid}/events", a.withTurn(reads, a.streamEvents))
	// Health checks are not authenticated.
	all := http.NewServeMux()
	all.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	all.Handle("GET /v1/me", a.identified(handler(a.me)))
	all.Handle("/", a.identified(a.allowed(mux)))
	return all
}

func (a *api) createProject(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if err := checkSlug(request.Slug); err != nil {
		return err
	}
	project, err := a.store.CreateProject(r.Context(), store.Project{Slug: request.Slug, Name: request.Name})
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, project)
	return nil
}

// listProjects lists the projects of which the caller or one of its groups
// is a member, or all projects for an admin.
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

// listHarnesses lists the harnesses that agents can use.
func (a *api) listHarnesses(w http.ResponseWriter, _ *http.Request) error {
	type harness struct {
		Name string `json:"name"`
	}
	names := a.driver.Harnesses()
	harnesses := make([]harness, len(names))
	for i, name := range names {
		harnesses[i] = harness{name}
	}
	respond(w, http.StatusOK, struct {
		Harnesses []harness `json:"harnesses"`
	}{harnesses})
	return nil
}

// agentRequest is the body of a request that creates or replaces an agent.
type agentRequest struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Harness      string `json:"harness"`
	Instructions string `json:"instructions"`
	Approval     string `json:"approval"`
	Model        *struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
	} `json:"model"`
	McpServers []string `json:"mcp_servers"`
}

// agentFrom returns the agent that the request describes, without its slug
// and harness, which the caller checks. The agent is one of the project.
func (a *api) agentFrom(r *http.Request, projectID uuid.UUID, request agentRequest) (store.Agent, error) {
	if request.Approval != "" && request.Approval != driver.Ask {
		return store.Agent{}, invalid(`approval must be "ask" or empty`)
	}
	if request.Model == nil {
		return store.Agent{}, invalid("model is required")
	}
	// The provider must belong to the agent's project.
	providerID, err := uuid.Parse(request.Model.Provider)
	if err != nil {
		return store.Agent{}, invalid("model.provider must be the ID of a provider of the project")
	}
	provider, err := a.store.Provider(r.Context(), providerID)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && provider.ProjectID != projectID):
		return store.Agent{}, invalid("model.provider must be the ID of a provider of the project")
	case err != nil:
		return store.Agent{}, err
	case request.Model.Name == "":
		return store.Agent{}, invalid("model.name is required")
	}
	servers, err := a.mcpServersOf(r, projectID, request.McpServers)
	if err != nil {
		return store.Agent{}, err
	}
	return store.Agent{
		Name: request.Name, Description: request.Description, Instructions: request.Instructions, Approval: request.Approval,
		Model: store.Model{Provider: providerID, Name: request.Model.Name}, McpServers: servers,
	}, nil
}

// mcpServersOf validates the MCP servers that a request names in mcp_servers,
// which are servers of the project, and returns them without repeats.
func (a *api) mcpServersOf(r *http.Request, projectID uuid.UUID, named []string) ([]uuid.UUID, error) {
	if len(named) == 0 {
		return nil, nil
	}
	of, err := a.store.McpServers(r.Context(), projectID)
	if err != nil {
		return nil, err
	}
	var servers []uuid.UUID
	for i, given := range named {
		id, err := uuid.Parse(given)
		if err != nil || !slices.ContainsFunc(of, func(server store.McpServer) bool { return server.ID == id }) {
			return nil, invalid(fmt.Sprintf("mcp_servers[%d] must be the ID of an MCP server of the project", i))
		}
		if !slices.Contains(servers, id) {
			servers = append(servers, id)
		}
	}
	return servers, nil
}

func (a *api) createAgent(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request agentRequest
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if err := checkSlug(request.Slug); err != nil {
		return err
	}
	if !a.driver.HasHarness(request.Harness) {
		return invalid("harness must be one of the configured harnesses")
	}
	agent, err := a.agentFrom(r, projectID, request)
	if err != nil {
		return err
	}
	agent.Slug, agent.Harness = request.Slug, request.Harness
	created, err := a.store.CreateAgent(r.Context(), projectID, agent)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, created)
	return nil
}

// replaceAgent replaces what can be changed of an agent. Its slug names it,
// and only its harness can continue the conversations of its sessions, so
// the request leaves both out or as they are.
func (a *api) replaceAgent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	existing, err := a.store.Agent(r.Context(), id)
	if err != nil {
		return err
	}
	var request agentRequest
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Slug != "" && request.Slug != existing.Slug {
		return invalid("slug cannot be changed")
	}
	if request.Harness != "" && request.Harness != existing.Harness {
		return invalid("harness cannot be changed")
	}
	agent, err := a.agentFrom(r, existing.ProjectID, request)
	if err != nil {
		return err
	}
	agent.ID = id
	replaced, err := a.store.ReplaceAgent(r.Context(), agent)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, replaced)
	return nil
}

// deleteAgent deletes the agent: its sessions end, and their sandboxes are
// deleted. The agent and the conversations are kept.
func (a *api) deleteAgent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	agent, err := a.driver.DeleteAgent(r.Context(), id)
	if err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "agent deleted", "agent", id, "by", callerOf(r).Subject)
	respond(w, http.StatusOK, agent)
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
	// The body is optional.
	var request struct {
		Title      string    `json:"title"`
		McpServers *[]string `json:"mcp_servers"`
	}
	if err := decode(w, r, &request); err != nil && !errors.Is(err, errNoBody) {
		return err
	}
	// A session that is given its MCP servers keeps how they differ from its
	// agent's, so that it follows the agent in the rest.
	var mcp map[uuid.UUID]bool
	if request.McpServers != nil {
		agent, err := a.store.Agent(r.Context(), agentID)
		if err != nil {
			return err
		}
		on, err := a.mcpServersOf(r, agent.ProjectID, *request.McpServers)
		if err != nil {
			return err
		}
		mcp = map[uuid.UUID]bool{}
		for _, server := range on {
			if !slices.Contains(agent.McpServers, server) {
				mcp[server] = true
			}
		}
		for _, server := range agent.McpServers {
			if !slices.Contains(on, server) {
				mcp[server] = false
			}
		}
	}
	session, err := a.store.CreateSession(r.Context(), agentID, callerOf(r).id, request.Title, mcp)
	if err != nil {
		return err
	}
	records, err := a.sessionRecords(r.Context(), []store.Session{session})
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, records[0])
	return nil
}

// listSessions lists the sessions of the agent that the caller may read: its
// own with sessions.run, and everyone's with sessions.view_all or as an
// admin.
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
	// A caller reads its own sessions with sessions.run, and everyone's
	// with sessions.view_all.
	by, readable := uuid.Nil, true
	if !holds(r, sessionsViewAll) {
		by, readable = callerOf(r).id, holds(r, sessionsRun)
	}
	sessions := []store.Session{}
	if readable {
		if sessions, err = a.store.Sessions(r.Context(), agentID, by, before, limit); err != nil {
			return err
		}
	}
	records, err := a.sessionRecords(r.Context(), sessions)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, struct {
		Sessions []sessionRecord `json:"sessions"`
	}{records})
	return nil
}

// sessionRecord is a session with its newest turn, if it has one, and the
// MCP servers that its next turns have, while it has not ended.
type sessionRecord struct {
	store.Session
	LastTurn   *lastTurn   `json:"last_turn,omitempty"`
	McpServers []uuid.UUID `json:"mcp_servers,omitempty"`
}

// lastTurn is a turn as its session carries it: a turn record without the
// text and the items.
type lastTurn struct {
	store.Turn
	// Text is never set. It keeps the text of the turn out of the JSON.
	Text      string            `json:"text,omitempty"`
	Approvals []json.RawMessage `json:"approvals,omitempty"`
}

func (a *api) sessionRecords(ctx context.Context, sessions []store.Session) ([]sessionRecord, error) {
	ids := make([]uuid.UUID, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	last, err := a.store.LastTurns(ctx, ids)
	if err != nil {
		return nil, err
	}
	servers, err := a.store.SessionMcpServers(ctx, ids)
	if err != nil {
		return nil, err
	}
	records := make([]sessionRecord, len(sessions))
	for i, session := range sessions {
		records[i] = sessionRecord{Session: session, McpServers: servers[session.ID]}
		turn, found := last[session.ID]
		if !found {
			continue
		}
		records[i].LastTurn = &lastTurn{Turn: reported(turn)}
		if turn.EndedAt == nil {
			if records[i].LastTurn.Approvals, err = a.store.WaitingApprovals(ctx, turn.ID); err != nil {
				return nil, err
			}
		}
	}
	return records, nil
}

// respondSession responds with the session and its newest turn.
func (a *api) respondSession(w http.ResponseWriter, r *http.Request, session store.Session) error {
	records, err := a.sessionRecords(r.Context(), []store.Session{session})
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, records[0])
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
	return a.respondSession(w, r, session)
}

// updateSession changes the session's title, the one field that can be
// changed.
func (a *api) updateSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	var request struct {
		Title *string `json:"title"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Title == nil {
		return invalid("title is required")
	}
	session, err := a.store.SetSessionTitle(r.Context(), id, *request.Title)
	if err != nil {
		return err
	}
	return a.respondSession(w, r, session)
}

// setSessionMcpServer turns an MCP server of the project on or off for the
// next turns of the session.
func (a *api) setSessionMcpServer(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	serverID, err := pathID(r, "server")
	if err != nil {
		return err
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Enabled == nil {
		return invalid("enabled must be true or false")
	}
	session, err := a.store.Session(r.Context(), sessionID)
	if err != nil {
		return err
	}
	agent, err := a.store.Agent(r.Context(), session.AgentID)
	if err != nil {
		return err
	}
	server, err := a.store.McpServer(r.Context(), serverID)
	if err != nil {
		return err
	}
	if server.ProjectID != agent.ProjectID {
		return errNotFound
	}
	if session, err = a.store.SetSessionMcpServer(r.Context(), sessionID, serverID, *request.Enabled); err != nil {
		return err
	}
	return a.respondSession(w, r, session)
}

// resetSessionMcpServers gives the next turns of the session the MCP servers
// of its agent again.
func (a *api) resetSessionMcpServers(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	session, err := a.store.ResetSessionMcpServers(r.Context(), id)
	if err != nil {
		return err
	}
	return a.respondSession(w, r, session)
}

// endSession ends the session and deletes its sandbox. The conversation is
// kept.
func (a *api) endSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	session, err := a.driver.EndSession(r.Context(), id)
	if err != nil {
		return err
	}
	return a.respondSession(w, r, session)
}

// createMessage queues a message as the session's next turn, and responds
// 202 with the turn.
func (a *api) createMessage(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	var request struct {
		Text        string      `json:"text"`
		Attachments []uuid.UUID `json:"attachments"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Text == "" && len(request.Attachments) == 0 {
		return invalid("text is required, unless the message carries attachments")
	}
	turn, err := a.driver.Accept(r.Context(), sessionID, callerOf(r).id, request.Text, request.Attachments)
	if err != nil {
		return err
	}
	carried, err := a.store.TurnAttachments(r.Context(), []uuid.UUID{turn.ID})
	if err != nil {
		// The turn is accepted, so the answer names it also without them.
		slog.ErrorContext(r.Context(), "read attachments of turn", "turn", turn.ID, "error", err)
	}
	// The turn is accepted once stored, so failing to publish its position
	// does not fail the request. A turn dispatched in the meantime has no
	// position.
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
	respond(w, http.StatusAccepted, attached(turn, carried[turn.ID]))
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

// listTurns lists the turns of a session, newest first. The query parameter
// limit caps their number, and before, a sequence number, lists only the
// turns before it, for paging. Sequence numbers start at 1.
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

// reported returns the turn as the API reports it. A turn that has left the
// queue runs: when a Supervisor follows it is between the Supervisors.
func reported(turn store.Turn) store.Turn {
	if turn.State == store.TurnDispatching {
		turn.State = store.TurnRunning
	}
	return turn
}

// turnRecord is a turn with its items, which are stored when the turn ends;
// until then clients follow the turn's events. Approvals holds the pending
// approval requests of a turn that has not ended.
type turnRecord struct {
	store.Turn
	Attachments []store.Attachment `json:"attachments,omitempty"`
	Items       []json.RawMessage  `json:"items"`
	Approvals   []json.RawMessage  `json:"approvals,omitempty"`
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
	carried, err := a.store.TurnAttachments(ctx, ids)
	if err != nil {
		return nil, err
	}
	records := make([]turnRecord, len(turns))
	for i, turn := range turns {
		// Items is [], not null, for a turn without items.
		records[i] = turnRecord{Turn: reported(turn), Attachments: carried[turn.ID], Items: append([]json.RawMessage{}, items[turn.ID]...)}
		if turn.EndedAt == nil {
			if records[i].Approvals, err = a.store.WaitingApprovals(ctx, turn.ID); err != nil {
				return nil, err
			}
		}
	}
	return records, nil
}

// answerApproval records a person's answer to an approval request, which
// must be one of the request's options, and passes it on to the runner. The
// approval.resolved event reports when the harness has it. The first answer
// wins.
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
		return invalid("option_id must be the ID of one of the request's options")
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
	// If the announcement fails, the Supervisor that follows the turn finds
	// the answer later.
	if err := a.events.AnnounceAnswer(r.Context(), turnID); err != nil {
		slog.ErrorContext(r.Context(), "announce approval answer", "turn", turnID, "error", err)
	}
	respond(w, http.StatusAccepted, answer)
	return nil
}

// cancelTurn cancels a turn. A queued turn ends at once, with 200. A running
// turn ends when its harness has stopped, so the response is 202. Cancelling
// a cancelled turn succeeds.
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
		// If the announcement fails, the Supervisor that follows the turn
		// finds the cancellation later.
		if err := a.events.AnnounceCancel(r.Context(), turn.ID); err != nil {
			slog.ErrorContext(r.Context(), "announce turn cancellation", "turn", turn.ID, "error", err)
		}
		respond(w, http.StatusAccepted, reported(turn))
		return nil
	}
	respond(w, http.StatusOK, turn)
	return nil
}

// handler is an http.Handler that writes the error it returns as an error
// response.
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
	case errors.Is(err, store.ErrAgentDeleted):
		failure = errAgentGone
	case errors.Is(err, store.ErrQueueFull):
		failure = errQueueFull
	case errors.Is(err, store.ErrMemberExists):
		failure = errMemberExists
	case errors.Is(err, store.ErrSlugTaken):
		failure = errSlugTaken
	case errors.Is(err, store.ErrUnknownAttachment):
		failure = errUnknownAttachment
	case errors.Is(err, driver.ErrTooManyAttachments):
		failure = errTooManyAttachments
	case errors.Is(err, driver.ErrAttachmentTooLarge):
		failure = errAttachmentTooLarge
	case errors.Is(err, driver.ErrNoAttachments):
		failure = errNoAttachments
	case errors.Is(err, driver.ErrLengthRequired):
		failure = errLengthRequired
	case errors.Is(err, objects.ErrFull):
		failure = errStorageFull
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

// pathID returns the named path value as a UUID. A malformed ID is reported
// as not found.
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, errNotFound
	}
	return id, nil
}

// queryNumber returns the named query parameter, which must be a number from
// 1 to most, or absent if it is not set.
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

// queryID returns the named query parameter as a UUID, or absent if it is
// not set.
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

func checkSlug(slug string) error {
	switch {
	case slug == "":
		return invalid("slug is required")
	case !slugForm.MatchString(slug):
		return invalid("slug must be 1 to 63 lowercase letters, digits and hyphens, beginning with a letter and not ending with a hyphen")
	}
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, into any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	switch err := decoder.Decode(into); {
	case errors.Is(err, io.EOF):
		return errNoBody
	case err != nil:
		return invalid("request body is not valid JSON for this endpoint")
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
