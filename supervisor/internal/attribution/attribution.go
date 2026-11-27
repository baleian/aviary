// Package attribution tells the egress gateway of Agent Substrate whom the
// connections and requests of a sandbox are for: the turn, the session, its
// agent and project, and the user who sent the turn's message. The gateway
// asks as an external processor when a sandbox opens a connection, and again
// for each request that it reads, and writes the answer into the lines of its
// access log.
package attribution

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"aviary/supervisor/store"
)

// Namespace is the namespace of the dynamic metadata that answers the
// gateway.
const Namespace = "aviary"

// actorAttribute is the request attribute that carries the SPIFFE ID with
// which the gateway authenticated the sandbox.
const actorAttribute = "filter_state['dev.ate.actor.identity']"

// Service answers the egress gateway for the sandboxes of an atespace.
type Service struct {
	extprocv3.UnimplementedExternalProcessorServer
	store    store.Store
	atespace string
}

// New returns the service for the sandboxes of the atespace.
func New(st store.Store, atespace string) *Service {
	return &Service{store: st, atespace: atespace}
}

// Process answers the headers of each request with its origin as dynamic
// metadata, and lets the request go on unchanged. The origin is that of the
// turn that runs in the sandbox's session or, between turns, of the last one
// that ran. An error refuses the request.
func (s *Service) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if request.GetRequestHeaders() == nil {
			return status.Error(codes.InvalidArgument, "only the headers of a request are processed")
		}
		metadata, err := s.origin(stream.Context(), actor(request))
		if err != nil {
			return err
		}
		answer := &extprocv3.ProcessingResponse{
			Response:        &extprocv3.ProcessingResponse_RequestHeaders{RequestHeaders: &extprocv3.HeadersResponse{}},
			DynamicMetadata: metadata,
		}
		if err := stream.Send(answer); err != nil {
			return err
		}
	}
}

// origin returns the dynamic metadata of a request of the actor.
func (s *Service) origin(ctx context.Context, actor string) (*structpb.Struct, error) {
	sessionID, known := s.session(actor)
	if !known {
		return nil, status.Error(codes.PermissionDenied, "the actor is not the sandbox of a session")
	}
	fields := map[string]any{"session": sessionID.String()}
	origin, err := s.store.LastTurn(ctx, sessionID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// A sandbox runs only for a dispatched turn, so this does not happen
		// unless the session's records are gone.
	case err != nil:
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up the last turn of a session", "session", sessionID, "error", err)
		return nil, status.Error(codes.Unavailable, "internal error")
	default:
		fields["turn"] = origin.TurnID.String()
		fields["agent"] = origin.AgentID.String()
		fields["project"] = origin.ProjectID.String()
		if origin.UserID != uuid.Nil {
			fields["user"] = origin.UserID.String()
			fields["subject"] = origin.Subject
			if origin.Username != "" {
				fields["username"] = origin.Username
			}
		}
	}
	values, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, err
	}
	return &structpb.Struct{Fields: map[string]*structpb.Value{Namespace: structpb.NewStructValue(values)}}, nil
}

// actorDomain is the trust domain of the SPIFFE IDs of actors.
const actorDomain = "substrate-actor.local"

// session returns the session of the sandbox of the actor's SPIFFE ID, which
// is spiffe://substrate-actor.local/<kind>/<atespace>/session-<ID>: the kind
// is worker-for-actor for the certificate of the actor's tunnel, and actor
// otherwise.
func (s *Service) session(actor string) (uuid.UUID, bool) {
	at, err := url.Parse(actor)
	if err != nil || at.Scheme != "spiffe" || at.Host != actorDomain {
		return uuid.Nil, false
	}
	for _, kind := range []string{"worker-for-actor", "actor"} {
		if name, found := strings.CutPrefix(at.Path, "/"+kind+"/"+s.atespace+"/session-"); found {
			session, err := uuid.Parse(name)
			return session, err == nil
		}
	}
	return uuid.Nil, false
}

// actor returns the SPIFFE ID of the actor of the request, or "" if the
// gateway did not send it.
func actor(request *extprocv3.ProcessingRequest) string {
	for _, attributes := range request.GetAttributes() {
		if value, found := attributes.GetFields()[actorAttribute]; found {
			return value.GetStringValue()
		}
	}
	return ""
}
