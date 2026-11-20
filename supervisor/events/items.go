package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strings"

	"github.com/google/uuid"
)

// Runner events that make up the items of a turn.
const (
	messageDelta      = "message.delta"
	thoughtDelta      = "thought.delta"
	toolCall          = "tool.call"
	toolUpdate        = "tool.update"
	planUpdate        = "plan.update"
	approvalRequested = "approval.requested"
	approvalResolved  = "approval.resolved"
)

// Item types.
const (
	itemMessage  = "message"
	itemThought  = "thought"
	itemToolCall = "tool_call"
	itemPlan     = "plan"
	itemApproval = "approval"
)

const itemsBatch = 1000

// Items assembles the items of the turn from its events, in the order in
// which each item began. An item is a JSON object with a type:
//
//   - message and thought hold the text of consecutive deltas.
//   - tool_call holds the fields of the tool.call event, as updated by
//     tool.update events.
//   - plan holds the entries of the last plan.update.
//   - approval holds the fields of approval.requested and, once the harness
//     has the answer, those of approval.resolved.
func (s *Streams) Items(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error) {
	turn := newRecord()
	after := Start
	for {
		batch, err := s.log.Read(ctx, turnID, after, 0)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return turn.encode()
		}
		for _, event := range batch {
			// Skip events that cannot be decoded, so that the turn can still
			// end.
			if err := turn.add(event.Type, event.Data); err != nil {
				slog.WarnContext(ctx, "skip invalid turn event", "turn", turnID, "event", event.ID, "error", err)
			}
		}
		after = batch[len(batch)-1].ID
	}
}

// record assembles the items of a turn from its events.
type record struct {
	items []*item
	// tools and approvals index their items by ID.
	tools     map[string]*item
	approvals map[string]*item
	plan      *item
}

func newRecord() *record {
	return &record{tools: map[string]*item{}, approvals: map[string]*item{}}
}

type item struct {
	kind string
	// messageID and text are set for messages and thoughts.
	messageID string
	text      strings.Builder
	// fields is set for tool calls, plans and approvals.
	fields map[string]json.RawMessage
}

// add adds the next event of the turn. Events that do not make up items are
// ignored.
func (r *record) add(eventType, data string) error {
	var kind string
	switch eventType {
	case messageDelta:
		kind = itemMessage
	case thoughtDelta:
		kind = itemThought
	case toolCall, toolUpdate:
		kind = itemToolCall
	case planUpdate:
		kind = itemPlan
	case approvalRequested, approvalResolved:
		kind = itemApproval
	default:
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		return err
	}
	switch kind {
	case itemToolCall:
		// An update holds only the fields that changed.
		id := string(fields["id"])
		call, found := r.tools[id]
		if !found {
			call = r.begin(itemToolCall)
			call.fields = map[string]json.RawMessage{}
			r.tools[id] = call
		}
		maps.Copy(call.fields, fields)
	case itemPlan:
		if r.plan == nil {
			r.plan = r.begin(itemPlan)
		}
		r.plan.fields = fields
	case itemApproval:
		// The resolution is merged into the item of the request.
		id := string(fields["id"])
		asked, found := r.approvals[id]
		if !found {
			asked = r.begin(itemApproval)
			asked.fields = map[string]json.RawMessage{}
			r.approvals[id] = asked
		}
		maps.Copy(asked.fields, fields)
	default:
		return r.say(kind, fields)
	}
	return nil
}

// say adds a message or thought delta. It extends the last item if that is
// the same message or thought, and starts a new item otherwise.
func (r *record) say(kind string, fields map[string]json.RawMessage) error {
	var text string
	if piece, found := fields["text"]; found {
		if err := json.Unmarshal(piece, &text); err != nil {
			return err
		}
	}
	if text == "" {
		return nil
	}
	messageID := string(fields["message_id"])
	var last *item
	if len(r.items) > 0 {
		last = r.items[len(r.items)-1]
	}
	if last == nil || last.kind != kind || last.messageID != messageID {
		last = r.begin(kind)
		last.messageID = messageID
	}
	last.text.WriteString(text)
	return nil
}

func (r *record) begin(kind string) *item {
	began := &item{kind: kind}
	r.items = append(r.items, began)
	return began
}

func (r *record) encode() ([]json.RawMessage, error) {
	encoded := make([]json.RawMessage, len(r.items))
	for i, item := range r.items {
		var object any
		switch item.kind {
		case itemMessage, itemThought:
			object = struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{item.kind, item.text.String()}
		default:
			fields := map[string]json.RawMessage{}
			maps.Copy(fields, item.fields)
			fields["type"] = json.RawMessage(`"` + item.kind + `"`)
			object = fields
		}
		var err error
		if encoded[i], err = json.Marshal(object); err != nil {
			return nil, err
		}
	}
	return encoded, nil
}
