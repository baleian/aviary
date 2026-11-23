package api

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// mcpServer is the API representation of an MCP server. A stored secret is
// shown only as stored, and a secret in a secret store by its location. The
// tools of a server are there once it has listed them.
type mcpServer struct {
	store.McpServer
	Headers []header      `json:"headers,omitempty"`
	Tools   *[]store.Tool `json:"tools,omitempty"`
}

func mcpServerAnswered(from store.McpServer) mcpServer {
	answer := mcpServer{McpServer: from, Headers: headersAnswered(from.Headers)}
	if from.ToolsListedAt != nil {
		tools := from.Tools
		if tools == nil {
			tools = []store.Tool{}
		}
		answer.Tools = &tools
	}
	return answer
}

// mcpServerRequest is the body of a request that creates or replaces an MCP
// server.
type mcpServerRequest struct {
	Slug      string        `json:"slug"`
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	Transport string        `json:"transport"`
	Headers   []headerField `json:"headers"`
}

// mcpServerAt validates what the request sets of the MCP server with the ID,
// but its slug, and returns the server as it is kept. A secret given by value
// is sealed for its place in that server. An existing server keeps the
// secrets that it stores, and the tools that it listed, while its URL stays
// the same.
func (a *api) mcpServerAt(r *http.Request, id, projectID uuid.UUID, existing *store.McpServer, request mcpServerRequest) (store.McpServer, error) {
	if err := endpoint(request.URL, "set the server's secrets in its headers"); err != nil {
		return store.McpServer{}, err
	}
	if request.Transport != store.TransportHTTP && request.Transport != store.TransportSSE {
		return store.McpServer{}, invalid(`transport must be "http" for Streamable HTTP, or "sse"`)
	}
	var had store.McpServer
	if existing != nil {
		had = *existing
	}
	keeps := existing != nil && existing.URL == request.URL
	headers, err := a.headersAt(r, projectID, request.Headers, true, had.Headers, keeps, func(name string) string { return store.McpServerHeaderPlace(id, name) })
	if err != nil {
		return store.McpServer{}, err
	}
	read := store.McpServer{ID: id, Name: request.Name, URL: request.URL, Transport: request.Transport, Headers: headers}
	if keeps {
		read.Tools, read.ToolsListedAt = had.Tools, had.ToolsListedAt
	}
	return read, nil
}

func (a *api) createMcpServer(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request mcpServerRequest
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if err := checkSlug(request.Slug); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	read, err := a.mcpServerAt(r, id, projectID, nil, request)
	if err != nil {
		return err
	}
	read.ProjectID, read.Slug = projectID, request.Slug
	created, err := a.store.CreateMcpServer(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, mcpServerAnswered(created))
	return nil
}

func (a *api) getMcpServer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "server")
	if err != nil {
		return err
	}
	found, err := a.store.McpServer(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, mcpServerAnswered(found))
	return nil
}

func (a *api) listMcpServers(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	found, err := a.store.McpServers(r.Context(), projectID)
	if err != nil {
		return err
	}
	servers := make([]mcpServer, len(found))
	for i, from := range found {
		servers[i] = mcpServerAnswered(from)
	}
	respond(w, http.StatusOK, struct {
		McpServers []mcpServer `json:"mcp_servers"`
	}{servers})
	return nil
}

// replaceMcpServer replaces all of an MCP server but its slug, which names its
// tools. The secrets that it stores stay only with its URL, so that they
// cannot be redirected to another server.
func (a *api) replaceMcpServer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "server")
	if err != nil {
		return err
	}
	existing, err := a.store.McpServer(r.Context(), id)
	if err != nil {
		return err
	}
	var request mcpServerRequest
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Slug != "" && request.Slug != existing.Slug {
		return invalid("slug cannot be changed")
	}
	read, err := a.mcpServerAt(r, id, existing.ProjectID, &existing, request)
	if err != nil {
		return err
	}
	replaced, err := a.store.ReplaceMcpServer(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, mcpServerAnswered(replaced))
	return nil
}

func (a *api) deleteMcpServer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "server")
	if err != nil {
		return err
	}
	if err := a.store.DeleteMcpServer(r.Context(), id); err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "MCP server deleted", "server", id, "by", callerOf(r).Subject)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
