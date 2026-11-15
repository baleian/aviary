package events

import (
	"slices"
	"testing"
)

func TestRecord(t *testing.T) {
	type event struct{ kind, data string }
	tests := []struct {
		name   string
		events []event
		items  []string
	}{
		{
			name: "the pieces of a message are one item",
			events: []event{
				{messageDelta, `{"text": "Hel", "message_id": "m1"}`},
				{messageDelta, `{"text": "lo", "message_id": "m1"}`},
			},
			items: []string{`{"type":"message","text":"Hello"}`},
		},
		{
			name: "another message is another item",
			events: []event{
				{messageDelta, `{"text": "one", "message_id": "m1"}`},
				{messageDelta, `{"text": "two", "message_id": "m2"}`},
			},
			items: []string{`{"type":"message","text":"one"}`, `{"type":"message","text":"two"}`},
		},
		{
			name: "a thought is not part of a message",
			events: []event{
				{thoughtDelta, `{"text": "hm"}`},
				{messageDelta, `{"text": "yes"}`},
				{thoughtDelta, `{"text": "hm"}`},
			},
			items: []string{
				`{"type":"thought","text":"hm"}`, `{"type":"message","text":"yes"}`, `{"type":"thought","text":"hm"}`,
			},
		},
		{
			name: "a message that goes on after a tool call is an item after it",
			events: []event{
				{messageDelta, `{"text": "before", "message_id": "m1"}`},
				{toolCall, `{"id": "t1", "title": "ls"}`},
				{messageDelta, `{"text": "after", "message_id": "m1"}`},
			},
			items: []string{
				`{"type":"message","text":"before"}`,
				`{"id":"t1","title":"ls","type":"tool_call"}`,
				`{"type":"message","text":"after"}`,
			},
		},
		{
			name: "an update changes its tool call where the call began",
			events: []event{
				{toolCall, `{"id": "t1", "title": "ls", "status": "pending"}`},
				{toolCall, `{"id": "t2", "title": "pwd", "status": "pending"}`},
				{messageDelta, `{"text": "done"}`},
				{toolUpdate, `{"id": "t1", "status": "completed", "output": "a.txt"}`},
			},
			items: []string{
				`{"id":"t1","output":"a.txt","status":"completed","title":"ls","type":"tool_call"}`,
				`{"id":"t2","status":"pending","title":"pwd","type":"tool_call"}`,
				`{"type":"message","text":"done"}`,
			},
		},
		{
			name:   "an update without its call begins the tool call",
			events: []event{{toolUpdate, `{"id": "t1", "status": "failed"}`}},
			items:  []string{`{"id":"t1","status":"failed","type":"tool_call"}`},
		},
		{
			name: "the plan is as it last stood, where it began",
			events: []event{
				{planUpdate, `{"entries": [{"content": "a", "status": "pending"}]}`},
				{messageDelta, `{"text": "working"}`},
				{planUpdate, `{"entries": [{"content": "a", "status": "completed"}]}`},
			},
			items: []string{
				`{"entries":[{"content":"a","status":"completed"}],"type":"plan"}`,
				`{"type":"message","text":"working"}`,
			},
		},
		{
			name: "events that tell nothing of the agent are passed over",
			events: []event{
				{TurnQueued, `{"position": 1}`},
				{TurnStarted, `{}`},
				{messageDelta, `{"text": ""}`},
				{messageDelta, `{"message_id": "m1"}`},
				{"an.event.of.a.newer.runner", `{"text": "hello"}`},
				{TurnCompleted, `{"stop_reason": "end_turn"}`},
			},
			items: []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turn := record{tools: map[string]*item{}}
			for _, event := range test.events {
				if err := turn.add(event.kind, event.data); err != nil {
					t.Fatalf("add %s %s: %v", event.kind, event.data, err)
				}
			}
			encoded, err := turn.encode()
			if err != nil {
				t.Fatal(err)
			}
			items := []string{}
			for _, item := range encoded {
				items = append(items, string(item))
			}
			if !slices.Equal(items, test.items) {
				t.Errorf("the items are\n%q, not\n%q", items, test.items)
			}
		})
	}
}

func TestRecordLeavesOutWhatItCannotRead(t *testing.T) {
	turn := record{tools: map[string]*item{}}
	if err := turn.add(messageDelta, `{"text": "kept"}`); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"text": `, `{"text": 1}`, `[]`} {
		if err := turn.add(messageDelta, data); err == nil {
			t.Errorf("add took the event %s", data)
		}
	}
	encoded, err := turn.encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 1 || string(encoded[0]) != `{"type":"message","text":"kept"}` {
		t.Errorf("the items are %q", encoded)
	}
}
