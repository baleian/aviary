// Package authz implements the Envoy external authorization service of the
// Egress Proxy. It identifies the turn that a request from a sandbox belongs
// to by the session token that the request carries.
package authz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// Headers that tell the Egress Proxy's configuration where to route a model
// request: the provider's host, and "true" if it uses TLS.
const (
	upstreamHeader    = "x-aviary-upstream"
	upstreamTLSHeader = "x-aviary-upstream-tls"
)

// mcpHeader names, by its ID, the MCP server that a request of a harness is
// for. It goes no further than the proxy.
const mcpHeader = "x-aviary-mcp"

// place is what session tokens are sealed for. A value sealed for another
// place does not open as a token.
const place = "the token of a session"

// Token returns the session's token: its ID, encrypted with the keys of the
// installation. The sandbox sends it with every outbound request. Only
// Aviary can create or read it.
func Token(keys *secret.Keys, session uuid.UUID) (string, error) {
	sealed, err := keys.Seal(session.String(), place)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Listing is the model request with which the API asks a model provider for
// its models. It goes through the Egress Proxy as the model request of a turn
// does.
type Listing struct {
	// Provider is the provider that is asked: its ID, project, URL, headers
	// and key. It need not be stored.
	Provider store.Provider
	// UserID and Subject identify the caller who asks. They are zero if
	// requests to the API are not authenticated.
	UserID  uuid.UUID
	Subject string
	// Until is when the listing's token stops being accepted.
	Until time.Time
}

// listingPlace is what the tokens of listings are sealed for.
const listingPlace = "the listing of a provider's models"

// sealedListing is the form in which a listing is sealed into its token.
type sealedListing struct {
	Project  uuid.UUID
	Provider uuid.UUID
	URL      string
	Headers  []store.Header         `json:",omitempty"`
	Key      []byte                 `json:",omitempty"`
	KeyAt    *store.SecretReference `json:",omitempty"`
	User     uuid.UUID
	Subject  string
	Until    time.Time
}

// ListingToken returns the token of a listing, which its model request
// carries in place of the token of a session: the listing, encrypted with
// the keys of the installation.
func ListingToken(keys *secret.Keys, listing Listing) (string, error) {
	encoded, err := json.Marshal(sealedListing{
		Project: listing.Provider.ProjectID, Provider: listing.Provider.ID, URL: listing.Provider.URL,
		Headers: listing.Provider.Headers, Key: listing.Provider.APIKey, KeyAt: listing.Provider.APIKeyAt,
		User: listing.UserID, Subject: listing.Subject, Until: listing.Until,
	})
	if err != nil {
		return "", err
	}
	token, err := keys.Seal(string(encoded), listingPlace)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// ToolListing is the requests with which the API asks an MCP server for its
// tools. They go through the Egress Proxy as the requests of a harness to the
// server do.
type ToolListing struct {
	// Server is the server that is asked: its ID, project, URL, transport
	// and headers. It need not be stored.
	Server store.McpServer
	// UserID and Subject identify the caller who asks. They are zero if
	// requests to the API are not authenticated.
	UserID  uuid.UUID
	Subject string
	// Until is when the listing's token stops being accepted.
	Until time.Time
}

// toolListingPlace is what the tokens of tool listings are sealed for.
const toolListingPlace = "the listing of an MCP server's tools"

// sealedToolListing is the form in which a tool listing is sealed into its
// token.
type sealedToolListing struct {
	Project   uuid.UUID
	Server    uuid.UUID
	URL       string
	Transport string
	Headers   []store.Header `json:",omitempty"`
	User      uuid.UUID
	Subject   string
	Until     time.Time
}

// ToolListingToken returns the token of a tool listing, which its requests
// carry: the listing, encrypted with the keys of the installation.
func ToolListingToken(keys *secret.Keys, listing ToolListing) (string, error) {
	server := listing.Server
	encoded, err := json.Marshal(sealedToolListing{
		Project: server.ProjectID, Server: server.ID, URL: server.URL, Transport: server.Transport, Headers: server.Headers,
		User: listing.UserID, Subject: listing.Subject, Until: listing.Until,
	})
	if err != nil {
		return "", err
	}
	token, err := keys.Seal(string(encoded), toolListingPlace)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

type authorizer struct {
	authv3.UnimplementedAuthorizationServer
	store store.Store
	// keys decrypt session tokens and stored API keys.
	keys *secret.Keys
	// stores reads API keys kept in the projects' secret stores.
	stores *secret.Stores
	// localhost, if not empty, is the host that the proxy reaches in place
	// of the local host of a provider's URL.
	localhost string
}

// New returns the authorization service. The local host of a provider's URL
// would be the proxy itself, so localhost names the host that stands in for
// it. If it is empty, none does.
func New(st store.Store, keys *secret.Keys, stores *secret.Stores, localhost string) authv3.AuthorizationServer {
	return &authorizer{store: st, keys: keys, stores: stores, localhost: localhost}
}

// Check allows a request that carries the token of a session with a running
// turn. It returns, as dynamic metadata for the proxy's access log, the IDs of
// the turn, session, agent and project, the provider of a model request or
// the MCP server of a request to one, and, if requests to the API are
// authenticated, the ID and subject of the user who sent the turn's message.
// The subject identifies the user without Aviary's records.
//
// A model request carries the token as its bearer token, in place of an API
// key. A forward proxy request carries it as the proxy password. The context
// extension "listener" tells them apart.
//
// A model request is routed to the endpoint of the turn's provider, with the
// provider's headers and API key. A request of the model listener that names
// an MCP server in mcpHeader is routed to that server instead, if the session
// has it on now. A forward proxy request goes where the pod sends it, and
// never gets the headers or the secrets of a provider or a server.
//
// A model request may carry the token of a listing instead. It is routed to
// the endpoint of the listing's provider, and its metadata names the project
// and the caller of the listing. A request of a tool listing is routed to the
// listing's MCP server in the same way.
func (a *authorizer) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	forward := request.GetAttributes().GetContextExtensions()["listener"] == "forward"
	path := request.GetAttributes().GetRequest().GetHttp().GetPath()
	token := bearerToken(headers["authorization"])
	if forward {
		token = proxyPassword(headers["proxy-authorization"])
	}
	if listing, known := a.toolListing(token); known && !forward {
		return a.checkToolListing(ctx, listing, path)
	}
	origin, found, err := a.origin(ctx, token, forward)
	if err != nil {
		return nil, failed(ctx, "look up the origin of a request", err)
	}
	if !found {
		return denied(forward), nil
	}
	from := map[string]any{"project": origin.ProjectID.String()}
	if origin.TurnID != uuid.Nil {
		from["turn"], from["session"], from["agent"] = origin.TurnID.String(), origin.SessionID.String(), origin.AgentID.String()
	}
	if origin.UserID != uuid.Nil {
		from["user"], from["subject"] = origin.UserID.String(), origin.Subject
	}
	var endpoint *authv3.OkHttpResponse
	switch named := headers[mcpHeader]; {
	case forward:
	case named != "" && origin.TurnID != uuid.Nil:
		server, on, err := a.mcpServerOf(ctx, origin, named)
		if err != nil {
			return nil, failed(ctx, "look up the MCP server of a request", err)
		}
		if !on {
			return refused(typev3.StatusCode_Forbidden, "the MCP server is not on in the session\n"), nil
		}
		from["mcp_server"] = server.ID.String()
		stores, err := a.storesOf(ctx, server.ProjectID, nil, server.Headers)
		if err != nil {
			return nil, failed(ctx, "look up the secret stores of an MCP server", err)
		}
		var refusal *authv3.CheckResponse
		if endpoint, refusal = a.mcpEndpoint(ctx, server, stores, path); refusal != nil {
			return refusal, nil
		}
	default:
		if origin.TurnID != uuid.Nil {
			from["provider"] = origin.Provider.ID.String()
		}
		stores, err := a.storesOf(ctx, origin.ProjectID, origin.SecretStore, origin.Provider.Headers)
		if err != nil {
			return nil, failed(ctx, "look up the secret stores of a provider", err)
		}
		var refusal *authv3.CheckResponse
		if endpoint, refusal = a.endpoint(ctx, origin, stores, path); refusal != nil {
			return refusal, nil
		}
	}
	metadata, err := structpb.NewStruct(from)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	allowed := &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
	}
	if endpoint != nil {
		allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: endpoint}
	}
	return allowed, nil
}

// mcpServerOf returns the MCP server that a request of the origin's turn
// names by its ID, and whether the session has it on.
func (a *authorizer) mcpServerOf(ctx context.Context, origin store.Origin, named string) (store.McpServer, bool, error) {
	id, err := uuid.Parse(named)
	if err != nil {
		return store.McpServer{}, false, nil
	}
	on, err := a.store.SessionMcpServers(ctx, []uuid.UUID{origin.SessionID})
	if err != nil || !slices.Contains(on[origin.SessionID], id) {
		return store.McpServer{}, false, err
	}
	server, err := a.store.McpServer(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.McpServer{}, false, nil
	}
	return server, err == nil, err
}

// failed returns the error with which a check fails, which makes the proxy
// deny the request.
func failed(ctx context.Context, doing string, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	slog.ErrorContext(ctx, doing, "error", err)
	return status.Error(codes.Internal, "internal error")
}

// checkToolListing allows a request of a tool listing to its MCP server. Its
// metadata names the project and the caller of the listing.
func (a *authorizer) checkToolListing(ctx context.Context, listing ToolListing, path string) (*authv3.CheckResponse, error) {
	server := listing.Server
	from := map[string]any{"project": server.ProjectID.String()}
	if listing.UserID != uuid.Nil {
		from["user"], from["subject"] = listing.UserID.String(), listing.Subject
	}
	metadata, err := structpb.NewStruct(from)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	stores, err := a.storesOf(ctx, server.ProjectID, nil, server.Headers)
	if err != nil {
		return nil, failed(ctx, "look up the secret stores of an MCP server", err)
	}
	endpoint, refusal := a.mcpEndpoint(ctx, server, stores, path)
	if refusal != nil {
		return refusal, nil
	}
	return &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
		HttpResponse:    &authv3.CheckResponse_OkResponse{OkResponse: endpoint},
	}, nil
}

// origin returns where the request with the token comes from: the running
// turn of the session of a session token or, for a model request, the
// listing of a listing token. It returns false for any other token.
func (a *authorizer) origin(ctx context.Context, token string, forward bool) (store.Origin, bool, error) {
	if session, known := a.session(token); known {
		origin, err := a.store.SessionTurn(ctx, session)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return store.Origin{}, false, nil
		case err != nil:
			return store.Origin{}, false, fmt.Errorf("running turn of session %s: %w", session, err)
		}
		return origin, true, nil
	}
	listing, known := a.listing(token)
	if !known || forward {
		return store.Origin{}, false, nil
	}
	origin := store.Origin{ProjectID: listing.Provider.ProjectID, UserID: listing.UserID, Subject: listing.Subject, Provider: listing.Provider}
	if at := listing.Provider.APIKeyAt; at != nil {
		in, err := a.store.SecretStore(ctx, at.Store)
		if errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != origin.ProjectID) {
			return store.Origin{}, false, nil
		}
		if err != nil {
			return store.Origin{}, false, fmt.Errorf("secret store %s of a listing: %w", at.Store, err)
		}
		origin.SecretStore = &in
	}
	return origin, true, nil
}

// storesOf returns the secret stores of the project that keep the secrets of
// an endpoint, by ID: the store of its key, if it has one there, and those of
// its headers.
func (a *authorizer) storesOf(ctx context.Context, projectID uuid.UUID, key *store.SecretStore, headers []store.Header) (map[uuid.UUID]store.SecretStore, error) {
	stores := map[uuid.UUID]store.SecretStore{}
	if key != nil {
		stores[key.ID] = *key
	}
	for _, header := range headers {
		if header.At == nil {
			continue
		}
		if _, known := stores[header.At.Store]; known {
			continue
		}
		in, err := a.store.SecretStore(ctx, header.At.Store)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue
		case err != nil:
			return nil, fmt.Errorf("secret store %s of a header: %w", header.At.Store, err)
		}
		if in.ProjectID == projectID {
			stores[in.ID] = in
		}
	}
	return stores, nil
}

// endpoint returns how the proxy forwards a model request for path: to the
// endpoint of the turn's provider, with the provider's headers, and with its
// API key in place of the session token. A header of the provider replaces
// one that the request has by the same name. A provider without a key gets no
// Authorization header. A path outside the path of the provider's URL, or a
// secret that cannot be read, is refused. stores are the secret stores of
// the provider's secrets.
func (a *authorizer) endpoint(ctx context.Context, origin store.Origin, stores map[uuid.UUID]store.SecretStore, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
	provider := origin.Provider
	address, err := url.Parse(provider.URL)
	if err != nil {
		slog.ErrorContext(ctx, "parse provider URL", "provider", provider.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the model provider's URL is invalid\n")
	}
	if !under(path, address.Path) {
		return nil, refused(typev3.StatusCode_NotFound, "model requests must be under the path "+address.Path+"\n")
	}
	endpoint := a.to(address)
	for _, header := range provider.Headers {
		value, refusal := a.valueOf(ctx, stores, header, store.ProviderHeaderPlace(provider.ID, header.Name), "the model provider's header "+header.Name)
		if refusal != nil {
			return nil, refusal
		}
		endpoint.Headers = append(endpoint.Headers, set(header.Name, value))
	}
	var key string
	switch in := origin.SecretStore; {
	case provider.APIKeyAt != nil && in != nil:
		var refusal *authv3.CheckResponse
		if key, refusal = a.read(ctx, *in, *provider.APIKeyAt, "the model provider's API key"); refusal != nil {
			return nil, refusal
		}
	case provider.APIKey != nil:
		if key, err = a.keys.Open(provider.APIKey, store.ProviderKeyPlace(provider.ID)); err != nil {
			slog.ErrorContext(ctx, "decrypt provider API key", "provider", provider.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable, "cannot read the model provider's API key\n")
		}
	default:
		endpoint.HeadersToRemove = []string{"authorization"}
		return endpoint, nil
	}
	endpoint.Headers = append(endpoint.Headers, set("authorization", "Bearer "+key))
	return endpoint, nil
}

// mcpEndpoint returns how the proxy forwards a request for path to an MCP
// server: to the server's host, with the server's headers in place of those
// of the request by the same names. The token of the request goes no further:
// the server's own authorization takes its place, if it has one. A server of
// Streamable HTTP is reached under the path of its URL. A server of SSE says
// where it takes messages, and is reached at any path of its host.
func (a *authorizer) mcpEndpoint(ctx context.Context, server store.McpServer, stores map[uuid.UUID]store.SecretStore, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
	address, err := url.Parse(server.URL)
	if err != nil {
		slog.ErrorContext(ctx, "parse MCP server URL", "server", server.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the MCP server's URL is invalid\n")
	}
	root := address.Path
	if server.Transport == store.TransportSSE {
		root = ""
	}
	if !under(path, root) {
		return nil, refused(typev3.StatusCode_NotFound, "requests to the MCP server must be under the path "+address.Path+"\n")
	}
	endpoint := a.to(address)
	authorized := false
	for _, header := range server.Headers {
		value, refusal := a.valueOf(ctx, stores, header, store.McpServerHeaderPlace(server.ID, header.Name), "the MCP server's header "+header.Name)
		if refusal != nil {
			return nil, refusal
		}
		endpoint.Headers = append(endpoint.Headers, set(header.Name, value))
		authorized = authorized || header.Name == "authorization"
	}
	endpoint.HeadersToRemove = []string{mcpHeader}
	if !authorized {
		endpoint.HeadersToRemove = append(endpoint.HeadersToRemove, "authorization")
	}
	return endpoint, nil
}

// to returns how the proxy forwards a request to the host of the address.
func (a *authorizer) to(address *url.URL) *authv3.OkHttpResponse {
	return &authv3.OkHttpResponse{Headers: []*corev3.HeaderValueOption{
		set(upstreamHeader, a.upstream(address)),
		set(upstreamTLSHeader, strconv.FormatBool(address.Scheme == "https")),
	}}
}

// set sets a header of a request in place of what the request has by its
// name.
func set(name, value string) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header:       &corev3.HeaderValue{Key: name, Value: value},
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
	}
}

// valueOf returns the value of a header: the value that it has, its stored
// secret, which is sealed for place, or its secret from one of the stores. A
// secret that cannot be read refuses the request, and what names the header
// in why.
func (a *authorizer) valueOf(ctx context.Context, stores map[uuid.UUID]store.SecretStore, header store.Header, place, what string) (string, *authv3.CheckResponse) {
	switch {
	case header.At != nil:
		in, found := stores[header.At.Store]
		if !found {
			return "", refused(typev3.StatusCode_ServiceUnavailable, "the secret store of "+what+" is not in the project\n")
		}
		value, refusal := a.read(ctx, in, *header.At, what)
		if refusal != nil {
			return "", refusal
		}
		// A secret that a store holds may end with a line break.
		if value = strings.TrimSpace(value); value == "" || !httpguts.ValidHeaderFieldValue(value) {
			return "", refused(typev3.StatusCode_ServiceUnavailable, "the secret of "+what+" in its secret store is not the value of an HTTP header\n")
		}
		return value, nil
	case header.Sealed != nil:
		value, err := a.keys.Open(header.Sealed, place)
		if err != nil {
			slog.ErrorContext(ctx, "decrypt the secret of a header", "place", place, "error", err)
			return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read "+what+"\n")
		}
		return value, nil
	}
	return header.Value, nil
}

// read returns a secret from a secret store, or the refusal of the request
// that needs it, which says why: the pod is told why the project's own store
// cannot be read, and the error holds nothing of the secret. what names the
// secret.
func (a *authorizer) read(ctx context.Context, in store.SecretStore, at store.SecretReference, what string) (string, *authv3.CheckResponse) {
	credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
	if err != nil {
		slog.ErrorContext(ctx, "decrypt secret store credential", "store", in.ID, "error", err)
		return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read the credential of the secret store of "+what+"\n")
	}
	value, err := a.stores.Read(ctx, in, credential, at.Path, at.Key)
	if err != nil {
		slog.WarnContext(ctx, "read a secret from its secret store", "secret", what, "store", in.ID, "error", err)
		return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read "+what+" from its secret store: "+err.Error()+"\n")
	}
	return value, nil
}

// upstream returns the host that the proxy connects to for a provider at the
// address, with the port of the address if it has one: the host of the
// address, or what stands in for it if it is the local host.
func (a *authorizer) upstream(address *url.URL) string {
	if a.localhost == "" || !local(address.Hostname()) {
		return address.Host
	}
	host := a.localhost
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := address.Port(); port != "" {
		host += ":" + port
	}
	return host
}

// local reports whether the host is the local host: by that name, or as a
// loopback address.
func local(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// under reports whether the request path, which may include a query, is the
// endpoint path or below it. A path with dot segments is not, since how it
// resolves is up to the endpoint.
func under(path, endpoint string) bool {
	path, _, _ = strings.Cut(path, "?")
	for _, step := range strings.Split(path, "/") {
		if plain, err := url.PathUnescape(step); err != nil || plain == "." || plain == ".." {
			return false
		}
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	return path == endpoint || strings.HasPrefix(path, endpoint+"/")
}

// refused denies a request with the status, and with why as the body.
func refused(status typev3.StatusCode, why string) *authv3.CheckResponse {
	return &authv3.CheckResponse{
		Status: &rpcstatus.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: &authv3.DeniedHttpResponse{
			Status: &typev3.HttpStatus{Code: status},
			Body:   why,
		}},
	}
}

// session returns the session of the token, or false if it is not a session
// token.
func (a *authorizer) session(token string) (uuid.UUID, bool) {
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return uuid.Nil, false
	}
	opened, err := a.keys.Open(sealed, place)
	if err != nil {
		return uuid.Nil, false
	}
	session, err := uuid.Parse(opened)
	return session, err == nil
}

// toolListing returns the tool listing of the token, or false if it is not
// the token of a tool listing that is still accepted.
func (a *authorizer) toolListing(token string) (ToolListing, bool) {
	encrypted, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return ToolListing{}, false
	}
	opened, err := a.keys.Open(encrypted, toolListingPlace)
	if err != nil {
		return ToolListing{}, false
	}
	var of sealedToolListing
	if err := json.Unmarshal([]byte(opened), &of); err != nil || !time.Now().Before(of.Until) {
		return ToolListing{}, false
	}
	return ToolListing{
		Server: store.McpServer{ID: of.Server, ProjectID: of.Project, URL: of.URL, Transport: of.Transport, Headers: of.Headers},
		UserID: of.User, Subject: of.Subject, Until: of.Until,
	}, true
}

// listing returns the listing of the token, or false if it is not the token
// of a listing that is still accepted.
func (a *authorizer) listing(token string) (Listing, bool) {
	encrypted, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return Listing{}, false
	}
	opened, err := a.keys.Open(encrypted, listingPlace)
	if err != nil {
		return Listing{}, false
	}
	var of sealedListing
	if err := json.Unmarshal([]byte(opened), &of); err != nil || !time.Now().Before(of.Until) {
		return Listing{}, false
	}
	return Listing{
		Provider: store.Provider{
			ID: of.Provider, ProjectID: of.Project, URL: of.URL, Headers: of.Headers, APIKey: of.Key, APIKeyAt: of.KeyAt,
		},
		UserID: of.User, Subject: of.Subject, Until: of.Until,
	}, true
}

// bearerToken returns the token of an Authorization header.
func bearerToken(header string) string {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return ""
	}
	return token
}

// proxyPassword returns the password of a Proxy-Authorization header. The
// user name can be anything.
func proxyPassword(header string) string {
	encoded, found := strings.CutPrefix(header, "Basic ")
	if !found {
		return ""
	}
	credentials, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	_, password, _ := strings.Cut(string(credentials), ":")
	return password
}

// denied denies a request. For the forward proxy it asks for credentials,
// since some clients, such as git, send them only when asked.
func denied(forward bool) *authv3.CheckResponse {
	response := &authv3.DeniedHttpResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
		Body:   "unknown token, or no turn is running in its session\n",
	}
	if forward {
		response.Status.Code = typev3.StatusCode_ProxyAuthenticationRequired
		response.Headers = []*corev3.HeaderValueOption{{
			Header: &corev3.HeaderValue{Key: "Proxy-Authenticate", Value: `Basic realm="aviary"`},
		}}
	}
	return &authv3.CheckResponse{
		Status:       &rpcstatus.Status{Code: int32(codes.Unauthenticated)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: response},
	}
}
