// Package authz answers the Egress Proxy. Before the proxy passes a request
// from a turn pod on, it asks which turn the request comes from. The proxy is
// Envoy, and it asks through its external authorization API.
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
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// The headers in which the proxy is told the model endpoint of a request:
// the host of the endpoint, and "true" if it is reached with TLS. The
// configuration of the proxy reads them.
const (
	upstreamHeader    = "x-aviary-upstream"
	upstreamTLSHeader = "x-aviary-upstream-tls"
)

type authorizer struct {
	authv3.UnimplementedAuthorizationServer
	store store.Store
	// keys open the API keys of the providers.
	keys *secret.Keys
}

func New(st store.Store, keys *secret.Keys) authv3.AuthorizationServer {
	return &authorizer{store: st, keys: keys}
}

// Check allows a request that carries the token of a running turn. The
// proxy gets back, as metadata, the ID of the turn and those of the session,
// the agent and the project that the turn belongs to. It writes them to its
// access log, so that a line of the log tells by itself where a request
// came from.
//
// A request to the model endpoint carries the token as its bearer token, in
// place of the API key. A request to the forward proxy carries it as the
// password of the proxy. The proxy says which of the two a request is with
// the context extension "listener".
//
// For a model request the proxy is also told where the request goes and
// with which key: to the endpoint of the provider that the turn's agent
// names. Of a request to the forward proxy it is told nothing of the kind.
// That request goes where the pod says, and the key of a provider is for its
// endpoint alone.
func (a *authorizer) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	forward := request.GetAttributes().GetContextExtensions()["listener"] == "forward"
	token := bearerToken(headers["authorization"])
	if forward {
		token = proxyPassword(headers["proxy-authorization"])
	}
	if token == "" {
		return denied(forward), nil
	}
	origin, err := a.store.RunningTurn(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return denied(forward), nil
	}
	if err != nil {
		// The proxy refuses a request that it gets no answer about.
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up the turn of a token", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	metadata, err := structpb.NewStruct(map[string]any{
		"turn":    origin.TurnID.String(),
		"session": origin.SessionID.String(),
		"agent":   origin.AgentID.String(),
		"project": origin.ProjectID.String(),
	})
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
	if origin.Provider == nil {
		// A turn of an agent from before agents named their models.
		return refused(typev3.StatusCode_NotFound, "the agent of the turn names no model\n"), nil
	}
	endpoint, refusal := a.endpoint(ctx, origin.Provider, request.GetAttributes().GetRequest().GetHttp().GetPath())
	if refusal != nil {
		return refusal, nil
	}
	allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: endpoint}
	return allowed, nil
}

// endpoint returns what the proxy is to make of a model request for the
// path: pass it on to the endpoint of the provider, with the key of the
// provider where the pod sent its turn token. An endpoint that takes no key
// does not get the token either. A request for a path beside that of the
// endpoint is refused, and so is one whose key cannot be read.
func (a *authorizer) endpoint(ctx context.Context, provider *store.Provider, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
	address, err := url.Parse(provider.URL)
	if err != nil {
		slog.ErrorContext(ctx, "read the URL of a provider", "provider", provider.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the URL of the model provider cannot be read\n")
	}
	if !under(path, address.Path) {
		return nil, refused(typev3.StatusCode_NotFound, "the model endpoint of the turn is at the path "+address.Path+"\n")
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
	if provider.APIKey == nil {
		endpoint.HeadersToRemove = []string{"authorization"}
		return endpoint, nil
	}
	key, err := a.keys.Open(provider.APIKey, store.ProviderKeyPlace(provider.ID))
	if err != nil {
		slog.ErrorContext(ctx, "open the API key of a provider", "provider", provider.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the API key of the model provider cannot be read\n")
	}
	endpoint.Headers = append(endpoint.Headers, set("authorization", "Bearer "+key))
	return endpoint, nil
}

// under tells whether the path of a request, which may end with a query, is
// the path of an endpoint or below it. A path with a step back up is not:
// where it leads is for the endpoint to say.
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

// refused refuses a request with the status and tells the pod why.
func refused(status typev3.StatusCode, why string) *authv3.CheckResponse {
	return &authv3.CheckResponse{
		Status: &rpcstatus.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: &authv3.DeniedHttpResponse{
			Status: &typev3.HttpStatus{Code: status},
			Body:   why,
		}},
	}
}

// bearerToken reads the token from an Authorization header.
func bearerToken(header string) string {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return ""
	}
	return token
}

// proxyPassword reads the password from a Proxy-Authorization header. The
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

// denied refuses a request. The forward proxy asks for its password: some
// clients, git among them, send none before they are asked.
func denied(forward bool) *authv3.CheckResponse {
	response := &authv3.DeniedHttpResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
		Body:   "the token belongs to no running turn\n",
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
