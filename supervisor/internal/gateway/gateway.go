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
	"sync"
	"time"

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

type Gateway struct {
	runnerv1.UnimplementedRunnerServiceServer
	store  *store.Store
	events *events.Streams
	// cancels holds, for every turn whose runner is connected, the channel
	// that tells that runner to stop the turn.
	mu      sync.Mutex
	cancels map[uuid.UUID]chan struct{}
}

func New(st *store.Store, ev *events.Streams) *Gateway {
	return &Gateway{store: st, events: ev, cancels: map[uuid.UUID]chan struct{}{}}
}

// sweep is how often the turns that were asked to stop are looked up, in case
// the announcement of one was missed.
const sweep = 10 * time.Second

// Run passes requests to stop a turn on to the runners that are connected to
// this Supervisor, until ctx is done. A request reaches any Supervisor, which
// records it and announces it to all of them.
func (g *Gateway) Run(ctx context.Context) {
	go g.events.WatchCancels(ctx, g.Cancel)
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
		turns, err := g.store.CancellingTurns(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "read the turns to cancel", "error", err)
			continue
		}
		for _, turnID := range turns {
			g.Cancel(turnID)
		}
	}
}

// Cancel tells the runner of the turn to stop the turn, if the runner is
// connected to this Supervisor.
func (g *Gateway) Cancel(turnID uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cancel, connected := g.cancels[turnID]; connected {
		select {
		case cancel <- struct{}{}:
		default:
			// The runner has not yet been told of an earlier call.
		}
	}
}

func (g *Gateway) RunTurn(stream grpc.BidiStreamingServer[runnerv1.RunTurnRequest, runnerv1.RunTurnResponse]) error {
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
	case errors.Is(err, store.ErrTurnCancelled):
		return status.Error(codes.Canceled, "the turn was cancelled")
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

	// From here on only the goroutine below sends to the runner.
	cancel := make(chan struct{}, 1)
	g.mu.Lock()
	g.cancels[turn.ID] = cancel
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.cancels, turn.ID)
		g.mu.Unlock()
	}()
	go func() {
		select {
		case <-cancel:
			order := &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Cancel{Cancel: &runnerv1.Cancel{}}}
			if err := stream.Send(order); err != nil {
				slog.WarnContext(ctx, "tell the runner to cancel", "turn", turn.ID, "error", err)
			}
		case <-ctx.Done():
		}
	}()
	// A request to cancel that came after the runner took the turn and
	// before the lines above found no runner to tell.
	if requested, err := g.store.Turn(ctx, turn.ID); err != nil {
		return g.internal(ctx, "read turn", err, "turn", turn.ID)
	} else if requested.CancelRequestedAt != nil {
		g.Cancel(turn.ID)
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
func (g *Gateway) receive(ctx context.Context, turnID uuid.UUID, request *runnerv1.RunTurnRequest) error {
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
		return g.store.SetFinished(ctx, turnID, message.Finished.GetStopReason(), usage(message.Finished.GetUsage()))
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

// usage is the token usage that the runner passed on from the harness, if
// the harness reported it.
func usage(reported *runnerv1.Usage) *store.Usage {
	if reported == nil {
		return nil
	}
	return &store.Usage{
		TotalTokens:       reported.GetTotalTokens(),
		InputTokens:       reported.GetInputTokens(),
		OutputTokens:      reported.GetOutputTokens(),
		ThoughtTokens:     reported.ThoughtTokens,
		CachedReadTokens:  reported.CachedReadTokens,
		CachedWriteTokens: reported.CachedWriteTokens,
	}
}

// eventJSON writes an event with the field names of the .proto file.
var eventJSON = protojson.MarshalOptions{UseProtoNames: true}

// internal logs a failure that the runner is not told the details of.
func (g *Gateway) internal(ctx context.Context, doing string, err error, attributes ...any) error {
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
