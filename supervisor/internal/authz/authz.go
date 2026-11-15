// Package authz answers the Egress Proxy. Before the proxy passes a request
// from a turn pod on, it asks which turn the request comes from. The proxy is
// Envoy, and it asks through its external authorization API.
package authz

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"aviary/supervisor/internal/store"
)

type authorizer struct {
	authv3.UnimplementedAuthorizationServer
	store *store.Store
}

func New(st *store.Store) authv3.AuthorizationServer {
	return &authorizer{store: st}
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
	return &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
	}, nil
}

// bearerToken reads the token from an Authorization header.
func bearerToken(header string) string {
	token, _ := strings.CutPrefix(header, "Bearer ")
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
