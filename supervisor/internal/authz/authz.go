// Package authz answers the Egress Proxy. Before the proxy passes a request
// from a turn pod on, it asks which turn the request comes from. The proxy is
// Envoy, and it asks through its external authorization API.
package authz

import (
	"context"
	"errors"
	"log/slog"
	"strings"

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

// Check allows a request that carries the token of a running turn as its
// bearer token. The proxy gets the ID of the turn back as metadata, which it
// writes to its access log.
func (a *authorizer) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	token, found := strings.CutPrefix(headers["authorization"], "Bearer ")
	if !found || token == "" {
		return denied(), nil
	}
	turnID, err := a.store.RunningTurn(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return denied(), nil
	}
	if err != nil {
		// The proxy refuses a request that it gets no answer about.
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up the turn of a token", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	metadata, err := structpb.NewStruct(map[string]any{"turn": turnID.String()})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
	}, nil
}

func denied() *authv3.CheckResponse {
	return &authv3.CheckResponse{
		Status: &rpcstatus.Status{Code: int32(codes.Unauthenticated)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: &authv3.DeniedHttpResponse{
			Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
			Body:   "the token belongs to no running turn\n",
		}},
	}
}
