// Package credentials puts the secrets of model providers and MCP servers
// into the requests of sandboxes, which never hold them. A sandbox sends a
// placeholder in place of each secret. The egress gateway of Agent Substrate
// asks the Supervisor for the secret, as the credential provider of the class
// aviary.io, and puts it in the placeholder's place.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	credprovider "go.ate.dev/substrate/api/credprovider/v1"
	"golang.org/x/net/http/httpguts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// Class names the Supervisor as a credential provider of the egress gateway.
// It is the host of the URIs of the secrets.
const Class = "aviary.io"

// Placeholder is what a sandbox sends in place of a secret.
const Placeholder = "aviary"

// What the URIs of secrets name.
const (
	providers  = "providers"
	mcpServers = "mcp-servers"
	keyPart    = "key"
	headerPart = "headers"
)

// ProviderKey returns the URI of the API key of a model provider.
func ProviderKey(provider uuid.UUID) string {
	return uri(providers, provider, keyPart)
}

// ProviderHeader returns the URI of a header of a model provider.
func ProviderHeader(provider uuid.UUID, name string) string {
	return uri(providers, provider, headerPart, strings.ToLower(name))
}

// McpServerHeader returns the URI of a header of an MCP server.
func McpServerHeader(server uuid.UUID, name string) string {
	return uri(mcpServers, server, headerPart, strings.ToLower(name))
}

// uri returns the URI of a secret. The name of a header may have characters
// that a URI escapes, such as "#".
func uri(kind string, id uuid.UUID, parts ...string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = url.PathEscape(part)
	}
	return "ate-secret://" + Class + "/default/" + kind + "/" + id.String() + "/" + strings.Join(escaped, "/")
}

// Secret reports whether the header has a secret value, which the sandbox
// does not get: a value stored sealed, or kept in a secret store.
func Secret(header store.Header) bool {
	return header.Sealed != nil || header.At != nil
}

// asked is what the URI of a secret names.
type asked struct {
	kind string
	id   uuid.UUID
	// header is the header's name, lowercase, or empty for an API key.
	header string
}

func parse(address string) (asked, error) {
	at, err := url.Parse(address)
	if err != nil || at.Scheme != "ate-secret" || at.Host != Class {
		return asked{}, fmt.Errorf("%q is not the URI of a secret of %s", address, Class)
	}
	parts := strings.Split(strings.TrimPrefix(at.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "default" {
		return asked{}, fmt.Errorf("%q is not the URI of a secret of %s", address, Class)
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		return asked{}, fmt.Errorf("%q names no ID", address)
	}
	switch {
	case parts[1] == providers && len(parts) == 4 && parts[3] == keyPart:
		return asked{kind: providers, id: id}, nil
	case (parts[1] == providers || parts[1] == mcpServers) && len(parts) == 5 && parts[3] == headerPart && parts[4] != "":
		return asked{kind: parts[1], id: id, header: parts[4]}, nil
	}
	return asked{}, fmt.Errorf("%q is not the URI of a secret of %s", address, Class)
}

// Resolver reads the secrets of model providers and MCP servers: those that
// Aviary stores, sealed with the keys of the installation, and those that
// projects keep in their secret stores.
type Resolver struct {
	Store  store.Store
	Keys   *secret.Keys
	Stores *secret.Stores
}

// ErrUnreadable wraps an error that reading a secret met, such as one of a
// secret store that cannot be reached. Its message says which secret, and
// holds nothing of it.
var ErrUnreadable = errors.New("the secret cannot be read")

// ProviderKey returns the provider's API key, or "" if it has none. in is
// the secret store of the key, if the provider keeps it in one.
func (r *Resolver) ProviderKey(ctx context.Context, provider store.Provider, in *store.SecretStore) (string, error) {
	switch {
	case provider.APIKeyAt != nil && in != nil:
		return r.read(ctx, *in, *provider.APIKeyAt, "the model provider's API key")
	case provider.APIKeyAt != nil:
		return "", fmt.Errorf("%w: the secret store of the model provider's API key is not in the project", ErrUnreadable)
	case provider.APIKey != nil:
		key, err := r.Keys.Open(provider.APIKey, store.ProviderKeyPlace(provider.ID))
		if err != nil {
			slog.ErrorContext(ctx, "decrypt provider API key", "provider", provider.ID, "error", err)
			return "", fmt.Errorf("%w: cannot decrypt the model provider's API key", ErrUnreadable)
		}
		return key, nil
	}
	return "", nil
}

// Header returns the value of a header of an endpoint of the project: the
// value that it has, its stored secret, which is sealed for place, or its
// secret from a secret store of the project. what names the header in errors.
func (r *Resolver) Header(ctx context.Context, projectID uuid.UUID, header store.Header, place, what string) (string, error) {
	switch {
	case header.At != nil:
		in, err := r.Store.SecretStore(ctx, header.At.Store)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != projectID):
			return "", fmt.Errorf("%w: the secret store of %s is not in the project", ErrUnreadable, what)
		case err != nil:
			return "", err
		}
		value, err := r.read(ctx, in, *header.At, what)
		if err != nil {
			return "", err
		}
		// A secret that a store holds may end with a line break.
		if value = strings.TrimSpace(value); value == "" || !httpguts.ValidHeaderFieldValue(value) {
			return "", fmt.Errorf("%w: the secret of %s in its secret store is not the value of an HTTP header", ErrUnreadable, what)
		}
		return value, nil
	case header.Sealed != nil:
		value, err := r.Keys.Open(header.Sealed, place)
		if err != nil {
			slog.ErrorContext(ctx, "decrypt the secret of a header", "place", place, "error", err)
			return "", fmt.Errorf("%w: cannot decrypt %s", ErrUnreadable, what)
		}
		return value, nil
	}
	return header.Value, nil
}

// read returns a secret from a secret store. what names the secret.
func (r *Resolver) read(ctx context.Context, in store.SecretStore, at store.SecretReference, what string) (string, error) {
	credential, err := r.Keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
	if err != nil {
		slog.ErrorContext(ctx, "decrypt secret store credential", "store", in.ID, "error", err)
		return "", fmt.Errorf("%w: cannot decrypt the credential of the secret store of %s", ErrUnreadable, what)
	}
	value, err := r.Stores.Read(ctx, in, credential, at.Path, at.Key)
	if err != nil {
		slog.WarnContext(ctx, "read a secret from its secret store", "secret", what, "store", in.ID, "error", err)
		return "", fmt.Errorf("%w: cannot read %s from its secret store: %w", ErrUnreadable, what, err)
	}
	return value, nil
}

// Service gives the egress gateway the secrets that the requests of
// sandboxes ask for. It gives a secret only to the sandbox of a session in
// which a turn runs, and only one of the turn's model provider or of an MCP
// server that the session has on.
type Service struct {
	credprovider.UnimplementedCredentialProviderServer
	resolver *Resolver
	// atespace holds the sandboxes.
	atespace string
}

// New returns the service for the sandboxes of the atespace.
func New(resolver *Resolver, atespace string) *Service {
	return &Service{resolver: resolver, atespace: atespace}
}

// actorDomain is the trust domain of the SPIFFE IDs of actors.
const actorDomain = "substrate-actor.local"

// session returns the session of the sandbox of the actor's SPIFFE ID,
// spiffe://substrate-actor.local/actor/<atespace>/session-<ID>.
func (s *Service) session(actor string) (uuid.UUID, bool) {
	at, err := url.Parse(actor)
	if err != nil || at.Scheme != "spiffe" || at.Host != actorDomain {
		return uuid.Nil, false
	}
	name, found := strings.CutPrefix(at.Path, "/actor/"+s.atespace+"/session-")
	if !found {
		return uuid.Nil, false
	}
	session, err := uuid.Parse(name)
	return session, err == nil
}

// FetchSecret returns the secret that the URI names, for the sandbox of the
// actor. It logs every secret that it gives, with the turn and the user that
// the request comes from.
func (s *Service) FetchSecret(ctx context.Context, request *credprovider.FetchSecretRequest) (*credprovider.FetchSecretResponse, error) {
	sessionID, known := s.session(request.GetActorSpiffeId())
	if !known {
		return nil, status.Error(codes.PermissionDenied, "the actor is not the sandbox of a session")
	}
	what, err := parse(request.GetUri())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	origin, err := s.resolver.Store.SessionTurn(ctx, sessionID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.PermissionDenied, "no turn runs in the session")
	case err != nil:
		return nil, failed(ctx, "look up the running turn of a session", err)
	}
	log := slog.With("turn", origin.TurnID, "session", origin.SessionID, "agent", origin.AgentID, "project", origin.ProjectID)
	if origin.UserID != uuid.Nil {
		log = log.With("user", origin.UserID, "subject", origin.Subject)
		if origin.Username != "" {
			log = log.With("username", origin.Username)
		}
	}
	var value string
	switch what.kind {
	case providers:
		if what.id != origin.Provider.ID {
			return nil, status.Error(codes.PermissionDenied, "the model provider is not that of the turn's agent")
		}
		log = log.With("provider", what.id)
		value, err = s.providerSecret(ctx, origin, what.header)
	case mcpServers:
		log = log.With("mcp_server", what.id)
		value, err = s.mcpSecret(ctx, origin, what)
	}
	if err != nil {
		return nil, err
	}
	header := what.header
	if header == "" {
		header = "authorization"
	}
	log.InfoContext(ctx, "secret put into a request of a sandbox", "header", header)
	return &credprovider.FetchSecretResponse{OpaqueBytes: []byte(value)}, nil
}

// providerSecret returns the API key of the turn's provider, or the value of
// the provider's header of the name.
func (s *Service) providerSecret(ctx context.Context, origin store.Origin, name string) (string, error) {
	provider := origin.Provider
	if name == "" {
		key, err := s.resolver.ProviderKey(ctx, provider, origin.SecretStore)
		if err != nil {
			return "", unreadable(ctx, err)
		}
		if key == "" {
			return "", status.Error(codes.NotFound, "the model provider has no API key")
		}
		return key, nil
	}
	i := slices.IndexFunc(provider.Headers, func(header store.Header) bool { return strings.EqualFold(header.Name, name) })
	if i < 0 {
		return "", status.Error(codes.NotFound, "the model provider has no such header")
	}
	header := provider.Headers[i]
	value, err := s.resolver.Header(ctx, provider.ProjectID, header, store.ProviderHeaderPlace(provider.ID, header.Name), "the model provider's header "+header.Name)
	if err != nil {
		return "", unreadable(ctx, err)
	}
	return value, nil
}

// mcpSecret returns the value of a header of an MCP server that the session
// has on.
func (s *Service) mcpSecret(ctx context.Context, origin store.Origin, what asked) (string, error) {
	on, err := s.resolver.Store.SessionMcpServers(ctx, []uuid.UUID{origin.SessionID})
	if err != nil {
		return "", failed(ctx, "look up the MCP servers of a session", err)
	}
	if !slices.Contains(on[origin.SessionID], what.id) {
		return "", status.Error(codes.PermissionDenied, "the MCP server is not on in the session")
	}
	server, err := s.resolver.Store.McpServer(ctx, what.id)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && server.ProjectID != origin.ProjectID):
		return "", status.Error(codes.NotFound, "no such MCP server")
	case err != nil:
		return "", failed(ctx, "look up an MCP server", err)
	}
	i := slices.IndexFunc(server.Headers, func(header store.Header) bool { return strings.EqualFold(header.Name, what.header) })
	if i < 0 {
		return "", status.Error(codes.NotFound, "the MCP server has no such header")
	}
	header := server.Headers[i]
	value, err := s.resolver.Header(ctx, server.ProjectID, header, store.McpServerHeaderPlace(server.ID, header.Name), "the MCP server's header "+header.Name)
	if err != nil {
		return "", unreadable(ctx, err)
	}
	return value, nil
}

// unreadable returns the error of a secret that cannot be read now, which the
// gateway may ask for again.
func unreadable(ctx context.Context, err error) error {
	if errors.Is(err, ErrUnreadable) {
		return status.Error(codes.Unavailable, err.Error())
	}
	return failed(ctx, "read a secret", err)
}

// failed returns the error of a call that failed on the Supervisor's side.
func failed(ctx context.Context, doing string, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	slog.ErrorContext(ctx, doing, "error", err)
	return status.Error(codes.Unavailable, "internal error")
}
