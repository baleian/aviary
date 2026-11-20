// Package authz implements the Envoy external authorization service of the
// Egress Proxy. It identifies the turn that a request from a sandbox belongs
// to by the session token that the request carries.
package authz

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/uuid"
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

type authorizer struct {
	authv3.UnimplementedAuthorizationServer
	store store.Store
	// keys decrypt session tokens and stored API keys.
	keys *secret.Keys
	// stores reads API keys kept in the projects' secret stores.
	stores *secret.Stores
}

func New(st store.Store, keys *secret.Keys, stores *secret.Stores) authv3.AuthorizationServer {
	return &authorizer{store: st, keys: keys, stores: stores}
}

// Check allows a request that carries the token of a session with a running
// turn. It returns, as dynamic metadata for the proxy's access log, the IDs of
// the turn, session, agent and project, the provider of a model request, and,
// if requests to the API are authenticated, the ID and subject of the user
// who sent the turn's message. The subject identifies the user without
// Aviary's records.
//
// A model request carries the token as its bearer token, in place of an API
// key. A forward proxy request carries it as the proxy password. The context
// extension "listener" tells them apart.
//
// A model request is routed to the endpoint of the turn's provider, with the
// provider's API key. A forward proxy request goes where the pod sends it,
// and never gets a provider's key.
func (a *authorizer) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	forward := request.GetAttributes().GetContextExtensions()["listener"] == "forward"
	token := bearerToken(headers["authorization"])
	if forward {
		token = proxyPassword(headers["proxy-authorization"])
	}
	session, known := a.session(token)
	if !known {
		return denied(forward), nil
	}
	origin, err := a.store.SessionTurn(ctx, session)
	if errors.Is(err, store.ErrNotFound) {
		return denied(forward), nil
	}
	if err != nil {
		// The proxy denies a request whose check fails.
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up running turn", "session", session, "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	from := map[string]any{
		"turn":    origin.TurnID.String(),
		"session": origin.SessionID.String(),
		"agent":   origin.AgentID.String(),
		"project": origin.ProjectID.String(),
	}
	if origin.UserID != uuid.Nil {
		from["user"], from["subject"] = origin.UserID.String(), origin.Subject
	}
	if !forward {
		from["provider"] = origin.Provider.ID.String()
	}
	metadata, err := structpb.NewStruct(from)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	allowed := &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
	}
	if forward {
		return allowed, nil
	}
	endpoint, refusal := a.endpoint(ctx, origin, request.GetAttributes().GetRequest().GetHttp().GetPath())
	if refusal != nil {
		return refusal, nil
	}
	allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: endpoint}
	return allowed, nil
}

// endpoint returns how the proxy forwards a model request for path: to the
// endpoint of the turn's provider, with the provider's API key in place of
// the session token. A provider without a key gets no Authorization header.
// A path outside the path of the provider's URL, or a key that cannot be
// read, is refused.
func (a *authorizer) endpoint(ctx context.Context, origin store.Origin, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
	provider := origin.Provider
	address, err := url.Parse(provider.URL)
	if err != nil {
		slog.ErrorContext(ctx, "parse provider URL", "provider", provider.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the model provider's URL is invalid\n")
	}
	if !under(path, address.Path) {
		return nil, refused(typev3.StatusCode_NotFound, "model requests must be under the path "+address.Path+"\n")
	}
	set := func(name, value string) *corev3.HeaderValueOption {
		return &corev3.HeaderValueOption{
			Header:       &corev3.HeaderValue{Key: name, Value: value},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		}
	}
	endpoint := &authv3.OkHttpResponse{Headers: []*corev3.HeaderValueOption{
		set(upstreamHeader, address.Host),
		set(upstreamTLSHeader, strconv.FormatBool(address.Scheme == "https")),
	}}
	var key string
	switch in := origin.SecretStore; {
	case provider.APIKeyAt != nil && in != nil:
		credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
		if err != nil {
			slog.ErrorContext(ctx, "decrypt secret store credential", "store", in.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable, "cannot read the credential of the model provider's secret store\n")
		}
		// The pod is told why the project's own store cannot be read. The
		// error holds nothing of the key.
		if key, err = a.stores.Read(ctx, *in, credential, provider.APIKeyAt.Path, provider.APIKeyAt.Key); err != nil {
			slog.WarnContext(ctx, "read provider API key from secret store", "provider", provider.ID, "store", in.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable,
				"cannot read the model provider's API key from its secret store: "+err.Error()+"\n")
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
