// Package gateway serves the runners. The runner of a turn connects to it,
// takes the turn and reports what the agent does.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"aviary/supervisor/internal/events"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
	"aviary/supervisor/internal/store"
)

type gateway struct {
	runnerv1.UnimplementedRunnerServiceServer
	store  *store.Store
	events *events.Streams
}

func New(st *store.Store, ev *events.Streams) runnerv1.RunnerServiceServer {
	return &gateway{store: st, events: ev}
}

func (g *gateway) RunTurn(stream grpc.BidiStreamingServer[runnerv1.RunTurnRequest, runnerv1.RunTurnResponse]) error {
	ctx := stream.Context()
	token, err := bearerToken(ctx)
	if err != nil {
		return err
	}
	turn, err := g.store.TakeTurn(ctx, token)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.Unauthenticated, "the token opens no turn")
	case errors.Is(err, store.ErrTurnTaken):
		return status.Error(codes.FailedPrecondition, "the turn was already taken")
	case err != nil:
		return g.internal(ctx, "take turn", err)
	}
	slog.InfoContext(ctx, "runner took its turn", "turn", turn.ID)

	start := &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Start{
		Start: &runnerv1.Start{Text: turn.Text, HarnessSessionId: turn.HarnessSessionID},
	}}
	if err := stream.Send(start); err != nil {
		return err
	}
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := g.receive(ctx, turn.ID, request); err != nil {
			return g.internal(ctx, "receive from runner", err, "turn", turn.ID)
		}
	}
}

// receive handles one message from the runner of the turn.
func (g *gateway) receive(ctx context.Context, turnID uuid.UUID, request *runnerv1.RunTurnRequest) error {
	switch message := request.Message.(type) {
	case *runnerv1.RunTurnRequest_Started:
		harnessSessionID := message.Started.GetHarnessSessionId()
		if harnessSessionID == "" {
			return errors.New("runner started without a harness session")
		}
		if err := g.store.SetHarnessSession(ctx, turnID, harnessSessionID); err != nil {
			return err
		}
		return g.events.Append(ctx, turnID, events.TurnStarted, struct{}{})
	case *runnerv1.RunTurnRequest_Finished:
		return g.store.SetStopReason(ctx, turnID, message.Finished.GetStopReason())
	}
	// The rest of the messages are the events of the turn. A runner newer
	// than the Supervisor may send one that the Supervisor does not know.
	fields := request.ProtoReflect()
	field := fields.WhichOneof(fields.Descriptor().Oneofs().ByName("message"))
	if field == nil {
		return nil
	}
	data, err := eventJSON.Marshal(fields.Get(field).Message().Interface())
	if err != nil {
		return err
	}
	name := strings.ReplaceAll(string(field.Name()), "_", ".")
	return g.events.Append(ctx, turnID, name, json.RawMessage(data))
}

// eventJSON writes an event with the field names of the .proto file.
var eventJSON = protojson.MarshalOptions{UseProtoNames: true}

// internal logs a failure that the runner is not told the details of.
func (g *gateway) internal(ctx context.Context, doing string, err error, attributes ...any) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	slog.ErrorContext(ctx, doing, append(attributes, "error", err)...)
	return status.Error(codes.Internal, "internal error")
}

// bearerToken reads the token from the authorization metadata of a call.
func bearerToken(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) == 1 {
		if token, found := strings.CutPrefix(values[0], "Bearer "); found && token != "" {
			return token, nil
		}
	}
	return "", status.Error(codes.Unauthenticated, `the authorization metadata must be "Bearer <token>"`)
}
