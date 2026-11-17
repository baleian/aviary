package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strings"

	"github.com/google/uuid"
)

// The events of a runner that tell what the agent says and does.
const (
	messageDelta = "message.delta"
	thoughtDelta = "thought.delta"
	toolCall     = "tool.call"
	toolUpdate   = "tool.update"
	planUpdate   = "plan.update"
	// The harness asked before it went on, and how that was answered to it.
	approvalRequested = "approval.requested"
	approvalResolved  = "approval.resolved"
)

// The types of an item.
const (
	itemMessage  = "message"
	itemThought  = "thought"
	itemToolCall = "tool_call"
	itemPlan     = "plan"
	itemApproval = "approval"
)

// itemsBatch is the most events that Items reads at once.
const itemsBatch = 1000

// Items returns what the agent said and did in the turn, put together from
// the turn's events, in the order in which each item began. An item is a JSON
// object with a type:
//
//   - message and thought have the text of the pieces that followed one
//     another.
//   - tool_call has the fields of the tool.call event, as the updates of the
//     call left them.
//   - plan has the entries of the plan as it last stood.
//   - approval has the fields of the approval.requested event and, once the
//     harness had its answer, those of approval.resolved.
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
			// An event that cannot be read is left out: a turn must end also
			// then.
			if err := turn.add(event.Type, event.Data); err != nil {
				slog.WarnContext(ctx, "read turn event", "turn", turnID, "event", event.ID, "error", err)
			}
		}
		after = batch[len(batch)-1].ID
	}
}

// record puts the events of a turn together into its items.
type record struct {
	items []*item
	// tools holds the tool calls by their IDs, approvals the requests for
	// approval by theirs, and plan is the plan.
	tools     map[string]*item
	approvals map[string]*item
	plan      *item
}

func newRecord() *record {
	return &record{tools: map[string]*item{}, approvals: map[string]*item{}}
}

type item struct {
	kind string
	// A message or a thought has the ID of its pieces and their text.
	messageID string
	text      strings.Builder
	// A tool call or a plan has the fields of its events.
	fields map[string]json.RawMessage
}

// add takes in the next event of the turn. Events that tell nothing of what
// the agent said or did are passed over.
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
		// An update sets only what changed of its call.
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
		// The answer is told where the request was.
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

// say adds a piece of a message or of a thought. The piece continues the
// last item if it is of the same message or thought, and begins an item if
// something else came in between.
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
