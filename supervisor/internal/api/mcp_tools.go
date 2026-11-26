package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/outbound"
	"aviary/supervisor/store"
)

// A server lists at most maxTools tools, and the description of a tool is
// kept up to maxDescription bytes: every member reads them with the servers
// of the project.
const (
	maxTools       = 1000
	maxDescription = 1000
)

// toolsListed is what came of asking an MCP server for its tools.
type toolsListed struct {
	// Reached reports whether the server answered. Status is the status of
	// the answer that was not a success, or 200, and Reason is why there was
	// no answer, or why the answers did not list tools.
	Reached bool   `json:"reached"`
	Status  int    `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Tools are the tools that the server listed, if it listed them.
	Tools      *[]store.Tool `json:"tools,omitempty"`
	DurationMS int64         `json:"duration_ms"`
}

// headed sends the requests of an MCP client to the server with the
// server's headers, and keeps the status of the first answer that was not a
// success.
type headed struct {
	headers http.Header
	next    http.RoundTripper

	mu sync.Mutex
	// answered reports whether the server answered, and status is the
	// status of its first answer that was not a success.
	answered bool
	status   int
}

func (h *headed) RoundTrip(request *http.Request) (*http.Response, error) {
	onward := request.Clone(request.Context())
	for name, values := range h.headers {
		onward.Header[name] = values
	}
	answer, err := h.next.RoundTrip(onward)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answered = true
	if (answer.StatusCode < 200 || answer.StatusCode > 299) && h.status == 0 {
		h.status = answer.StatusCode
	}
	return answer, nil
}

// listTools asks the MCP server for its tools, with the server's headers, as
// a harness reaches the server. It returns the tools only if the server
// listed them.
func (a *api) listTools(ctx context.Context, server store.McpServer) (toolsListed, error) {
	headers := http.Header{}
	for _, header := range server.Headers {
		value, err := a.resolver.Header(ctx, server.ProjectID, header, store.McpServerHeaderPlace(server.ID, header.Name), "the MCP server's header "+header.Name)
		if errors.Is(err, credentials.ErrUnreadable) {
			return toolsListed{Reason: err.Error()}, nil
		}
		if err != nil {
			return toolsListed{}, err
		}
		headers.Set(header.Name, value)
	}
	asked, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	through := &headed{headers: headers, next: a.outbound.Transport}
	client := &http.Client{Transport: through, CheckRedirect: a.outbound.CheckRedirect}
	var transport mcp.Transport = &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}
	if server.Transport == store.TransportSSE {
		transport = &mcp.SSEClientTransport{Endpoint: server.URL, HTTPClient: client}
	}
	began := time.Now()
	tools, err := toolsAt(asked, transport)
	took := time.Since(began).Milliseconds()
	through.mu.Lock()
	defer through.mu.Unlock()
	switch {
	case ctx.Err() != nil:
		return toolsListed{}, ctx.Err()
	case err == nil:
		// What the server answers once it has listed its tools, as when it
		// does not end sessions, does not matter.
		return toolsListed{Reached: true, Status: http.StatusOK, Tools: &tools, DurationMS: took}, nil
	case errors.Is(asked.Err(), context.DeadlineExceeded):
		return toolsListed{Reason: "the server did not answer in " + listTimeout.String(), DurationMS: took}, nil
	case outbound.Refused(err):
		return toolsListed{Reason: "the server's address is not reached", DurationMS: took}, nil
	case through.status != 0:
		return toolsListed{Reached: true, Status: through.status, DurationMS: took}, nil
	case !through.answered:
		slog.InfoContext(ctx, "ask an MCP server for its tools", "url", server.URL, "error", err)
		return toolsListed{Reason: "the server cannot be reached", DurationMS: took}, nil
	}
	slog.InfoContext(ctx, "an MCP server did not list its tools", "url", server.URL, "error", err)
	return toolsListed{Reached: true, Status: http.StatusOK, Reason: "the server does not answer as an MCP server does", DurationMS: took}, nil
}

// toolsAt connects to an MCP server with the transport, and returns the tools
// that it lists.
func toolsAt(ctx context.Context, transport mcp.Transport) ([]store.Tool, error) {
	session, err := mcp.NewClient(&mcp.Implementation{Name: "aviary"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	tools := []store.Tool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if len(tools) == maxTools {
			break
		}
		tools = append(tools, store.Tool{Name: tool.Name, Description: cut(tool.Description, maxDescription)})
	}
	return tools, nil
}

// cut returns the text, cut to at most size bytes at the start of a
// character.
func cut(text string, size int) string {
	if len(text) <= size {
		return text
	}
	for size > 0 && !utf8.RuneStart(text[size]) {
		size--
	}
	return text[:size]
}

// serverTools lists the tools of an MCP server that need not be registered:
// the request gives the URL, the transport and the headers of a server,
// which is not stored. A server that the request names lends the secrets
// that it stores, as it would keep them if it were replaced.
func (a *api) serverTools(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request struct {
		URL       string        `json:"url"`
		Transport string        `json:"transport"`
		Headers   []headerField `json:"headers"`
		Server    string        `json:"server"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var existing *store.McpServer
	if request.Server != "" {
		named, err := uuid.Parse(request.Server)
		if err != nil {
			return invalid("server must be the ID of an MCP server of the project")
		}
		found, err := a.store.McpServer(r.Context(), named)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && found.ProjectID != projectID):
			return invalid("server must be the ID of an MCP server of the project")
		case err != nil:
			return err
		}
		existing, id = &found, found.ID
	}
	server, err := a.mcpServerAt(r, id, projectID, existing, mcpServerRequest{URL: request.URL, Transport: request.Transport, Headers: request.Headers})
	if err != nil {
		return err
	}
	server.ProjectID = projectID
	listed, err := a.listTools(r.Context(), server)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, listed)
	return nil
}

// refreshTools asks an MCP server for its tools again, with what it stores,
// and keeps them if it lists them.
func (a *api) refreshTools(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "server")
	if err != nil {
		return err
	}
	server, err := a.store.McpServer(r.Context(), id)
	if err != nil {
		return err
	}
	listed, err := a.listTools(r.Context(), server)
	if err != nil {
		return err
	}
	if listed.Tools != nil {
		if err := a.store.SetMcpServerTools(r.Context(), id, *listed.Tools, time.Now()); err != nil {
			return err
		}
	}
	respond(w, http.StatusOK, listed)
	return nil
}

// withTools returns the server with the tools that it lists now, if it lists
// them, and otherwise as it is.
func (a *api) withTools(r *http.Request, server store.McpServer) (store.McpServer, error) {
	listed, err := a.listTools(r.Context(), server)
	if err != nil || listed.Tools == nil {
		return server, err
	}
	now := time.Now()
	server.Tools, server.ToolsListedAt = *listed.Tools, &now
	return server, nil
}
