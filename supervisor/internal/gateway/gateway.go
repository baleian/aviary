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
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/store"
)

type Gateway struct {
	runnerv1.UnimplementedRunnerServiceServer
	store   *store.Store
	events  *events.Streams
	metrics *metrics.Metrics
	// connected holds, for every turn whose runner is connected, what that
	// runner is to be told.
	mu        sync.Mutex
	connected map[uuid.UUID]*orders
	sweep     time.Duration
}

// orders are what the runner of a turn is to be told: to stop the turn, and
// the answers to its requests for approval.
type orders struct {
	cancel  chan struct{}
	answers chan *runnerv1.Approval
}

// New returns a Gateway that looks every sweep for what it was to pass on
// and was not told of.
func New(st *store.Store, ev *events.Streams, m *metrics.Metrics, sweep time.Duration) *Gateway {
	return &Gateway{store: st, events: ev, metrics: m, connected: map[uuid.UUID]*orders{}, sweep: sweep}
}

// Sweep is how often the turns that were asked to stop and the answers to
// requests for approval are looked up, in case the announcement of one was
// missed.
const Sweep = 10 * time.Second

// Run passes requests to stop a turn, and the answers to requests for
// approval, on to the runners that are connected to this Supervisor, until
// ctx is done. Either reaches any Supervisor, which records it and announces
// it to all of them.
func (g *Gateway) Run(ctx context.Context) {
	go g.events.WatchCancels(ctx, g.Cancel)
	go g.events.WatchAnswers(ctx, func(turnID uuid.UUID) { g.answer(ctx, &turnID) })
	ticker := time.NewTicker(g.sweep)
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
		g.answer(ctx, nil)
	}
}

// answer gives the runners that are connected to this Supervisor the answers
// that their harnesses have not got yet: those of the turn, or of all turns
// if it is nil. A runner drops an answer that it has given its harness.
func (g *Gateway) answer(ctx context.Context, turnID *uuid.UUID) {
	if turnID != nil && !g.has(*turnID) {
		return
	}
	answers, err := g.store.Answers(ctx, turnID)
	if err != nil {
		slog.ErrorContext(ctx, "read the answers to requests for approval", "error", err)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, answer := range answers {
		if runner, connected := g.connected[answer.TurnID]; connected {
			select {
			case runner.answers <- &runnerv1.Approval{Id: answer.ID, OptionId: answer.OptionID}:
			default:
				// The runner is behind. It is given the answer the next time.
			}
		}
	}
}

// has tells whether the runner of the turn is connected to this Supervisor.
func (g *Gateway) has(turnID uuid.UUID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, connected := g.connected[turnID]
	return connected
}

// Cancel tells the runner of the turn to stop the turn, if the runner is
// connected to this Supervisor.
func (g *Gateway) Cancel(turnID uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if runner, connected := g.connected[turnID]; connected {
		select {
		case runner.cancel <- struct{}{}:
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
	runner, err := runnerID(ctx)
	if err != nil {
		return err
	}
	turn, err := g.store.TakeTurn(ctx, token, runner)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.Unauthenticated, "the token opens no turn")
	case errors.Is(err, store.ErrTurnTaken):
		return status.Error(codes.FailedPrecondition, "another runner took the turn")
	case errors.Is(err, store.ErrTurnEnded):
		return status.Error(codes.FailedPrecondition, "the turn has ended")
	case errors.Is(err, store.ErrTurnCancelled):
		return status.Error(codes.FailedPrecondition, "the turn was cancelled")
	case err != nil:
		return g.internal(ctx, "take turn", err)
	}
	var received uint64
	if turn.Again {
		if received, err = g.events.Received(ctx, turn.ID); err != nil {
			return g.internal(ctx, "read what the runner has reported", err, "turn", turn.ID)
		}
		slog.InfoContext(ctx, "runner came back for its turn", "turn", turn.ID, "received", received)
	} else {
		slog.InfoContext(ctx, "runner took its turn", "turn", turn.ID)
	}
	defer g.metrics.Connected(turn.Harness)()

	// The orders are sent from another goroutine than the rest, and a stream
	// takes one Send at a time.
	var sending sync.Mutex
	send := func(response *runnerv1.RunTurnResponse) error {
		sending.Lock()
		defer sending.Unlock()
		return stream.Send(response)
	}
	start := &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Start{
		Start: &runnerv1.Start{
			Text: turn.Text, HarnessSessionId: turn.HarnessSessionID, Instructions: turn.Instructions,
			Received: &received,
		},
	}}
	if err := send(start); err != nil {
		return err
	}

	told := &orders{cancel: make(chan struct{}, 1), answers: make(chan *runnerv1.Approval, 16)}
	g.mu.Lock()
	g.connected[turn.ID] = told
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		// The runner may have come back to this Supervisor while this call
		// was still open. The orders are then those of its new call.
		if g.connected[turn.ID] == told {
			delete(g.connected, turn.ID)
		}
		g.mu.Unlock()
	}()
	go func() {
		// The runner is told once to stop, however often it is asked for.
		cancel := told.cancel
		for {
			var order *runnerv1.RunTurnResponse
			select {
			case <-cancel:
				cancel = nil
				order = &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Cancel{Cancel: &runnerv1.Cancel{}}}
			case approval := <-told.answers:
				order = &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Approval{Approval: approval}}
			case <-ctx.Done():
				return
			}
			if err := send(order); err != nil {
				slog.WarnContext(ctx, "pass an order on to the runner", "turn", turn.ID, "error", err)
				return
			}
		}
	}()
	// A request to cancel, or an answer, that came after the runner took the
	// turn and before the lines above found no runner to tell.
	if requested, err := g.store.Turn(ctx, turn.ID); err != nil {
		return g.internal(ctx, "read turn", err, "turn", turn.ID)
	} else if requested.CancelRequestedAt != nil {
		g.Cancel(turn.ID)
	}
	g.answer(ctx, &turn.ID)

	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		err = g.receive(ctx, turn, request)
		if errors.Is(err, events.ErrGap) {
			slog.ErrorContext(ctx, "receive from runner", "turn", turn.ID, "seq", request.GetSeq(), "error", err)
			return status.Error(codes.DataLoss, "messages of the runner that were recorded are lost")
		}
		if err != nil {
			return g.internal(ctx, "receive from runner", err, "turn", turn.ID)
		}
		if seq := request.GetSeq(); seq > 0 {
			answer := &runnerv1.RunTurnResponse{Message: &runnerv1.RunTurnResponse_Received{
				Received: &runnerv1.Received{Seq: seq},
			}}
			if err := send(answer); err != nil {
				return err
			}
		}
	}
}

// receive handles one message from the runner of the turn. The runner may
// have sent the message before, to this Supervisor or to another.
func (g *Gateway) receive(ctx context.Context, turn store.TakenTurn, request *runnerv1.RunTurnRequest) error {
	seq := request.GetSeq()
	switch message := request.Message.(type) {
	case *runnerv1.RunTurnRequest_Started:
		harnessSessionID := message.Started.GetHarnessSessionId()
		if harnessSessionID == "" {
			return errors.New("runner started without a harness session")
		}
		startup, first, err := g.store.StartTurn(ctx, turn.ID, harnessSessionID)
		if err != nil {
			return err
		}
		if first {
			g.metrics.Started(turn.Harness, startup)
		}
		return g.record(ctx, turn.ID, seq, events.TurnStarted, struct{}{})
	case *runnerv1.RunTurnRequest_Finished:
		err := g.store.SetFinished(ctx, turn.ID, message.Finished.GetStopReason(), usage(message.Finished.GetUsage()))
		if err != nil {
			return err
		}
		return g.record(ctx, turn.ID, seq, "", nil)
	case *runnerv1.RunTurnRequest_ApprovalRequested:
		// The request is kept for the person who answers it. It is also an
		// event of the turn, like the messages below.
		asked, err := eventJSON.Marshal(message.ApprovalRequested)
		if err != nil {
			return err
		}
		if err := g.store.AddApproval(ctx, turn.ID, message.ApprovalRequested.GetId(), asked); err != nil {
			return err
		}
	case *runnerv1.RunTurnRequest_ApprovalResolved:
		resolved := message.ApprovalResolved
		if _, _, err := g.store.ResolveApproval(ctx, turn.ID, resolved.GetId(), resolved.GetOutcome()); err != nil {
			return err
		}
	}
	// The rest of the messages are the events of the turn. A runner newer
	// than the Supervisor may send one that the Supervisor does not know.
	fields := request.ProtoReflect()
	field := fields.WhichOneof(fields.Descriptor().Oneofs().ByName("message"))
	if field == nil {
		return g.record(ctx, turn.ID, seq, "", nil)
	}
	data, err := eventJSON.Marshal(fields.Get(field).Message().Interface())
	if err != nil {
		return err
	}
	name := strings.ReplaceAll(string(field.Name()), "_", ".")
	return g.record(ctx, turn.ID, seq, name, json.RawMessage(data))
}

// record records the runner's message with the number seq, and adds its
// event to the turn's stream. A message without an event has no eventType. A
// runner of an older release does not number its messages, and seq is 0.
func (g *Gateway) record(ctx context.Context, turnID uuid.UUID, seq uint64, eventType string, data any) error {
	switch {
	case seq > 0:
		return g.events.Receive(ctx, turnID, seq, eventType, data)
	case eventType != "":
		return g.events.Append(ctx, turnID, eventType, data)
	}
	return nil
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

// runnerID reads the UUID that the runner names itself with from the
// metadata of a call. A runner of an older release sends none.
func runnerID(ctx context.Context) (*uuid.UUID, error) {
	values := metadata.ValueFromIncomingContext(ctx, "aviary-runner")
	if len(values) == 0 {
		return nil, nil
	}
	id, err := uuid.Parse(values[0])
	if err != nil || len(values) != 1 {
		return nil, status.Error(codes.InvalidArgument, "the aviary-runner metadata must be a UUID")
	}
	return &id, nil
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
