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
			name: "message deltas are merged",
			events: []event{
				{messageDelta, `{"text": "Hel", "message_id": "m1"}`},
				{messageDelta, `{"text": "lo", "message_id": "m1"}`},
			},
			items: []string{`{"type":"message","text":"Hello"}`},
		},
		{
			name: "new message ID starts a new item",
			events: []event{
				{messageDelta, `{"text": "one", "message_id": "m1"}`},
				{messageDelta, `{"text": "two", "message_id": "m2"}`},
			},
			items: []string{`{"type":"message","text":"one"}`, `{"type":"message","text":"two"}`},
		},
		{
			name: "thoughts are separate from messages",
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
			name: "message continued after a tool call is a new item",
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
			name: "approval resolution is merged into its request",
			events: []event{
				{toolCall, `{"id": "t1", "title": "rm -r build", "status": "pending"}`},
				{approvalRequested, `{"id": "1", "tool_call": {"id": "t1"}, "options": [{"id": "once", "kind": "allow_once", "name": "Once"}]}`},
				{approvalRequested, `{"id": "2", "tool_call": {"id": "t2"}, "options": []}`},
				{approvalResolved, `{"id": "2", "outcome": "cancelled"}`},
				{approvalResolved, `{"id": "1", "outcome": "selected", "option_id": "once"}`},
				{messageDelta, `{"text": "done"}`},
			},
			items: []string{
				`{"id":"t1","status":"pending","title":"rm -r build","type":"tool_call"}`,
				`{"id":"1","option_id":"once","options":[{"id":"once","kind":"allow_once","name":"Once"}],"outcome":"selected","tool_call":{"id":"t1"},"type":"approval"}`,
				`{"id":"2","options":[],"outcome":"cancelled","tool_call":{"id":"t2"},"type":"approval"}`,
				`{"type":"message","text":"done"}`,
			},
		},
		{
			name: "unresolved approval request",
			events: []event{
				{approvalRequested, `{"id": "1", "tool_call": {"id": "t1"}, "options": []}`},
			},
			items: []string{`{"id":"1","options":[],"tool_call":{"id":"t1"},"type":"approval"}`},
		},
		{
			name: "tool update is merged into its call",
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
			name:   "tool update without a call starts one",
			events: []event{{toolUpdate, `{"id": "t1", "status": "failed"}`}},
			items:  []string{`{"id":"t1","status":"failed","type":"tool_call"}`},
		},
		{
			name: "plan keeps its first position and last entries",
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
			name: "other events are ignored",
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
			turn := newRecord()
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
				t.Errorf("items =\n%q\nwant\n%q", items, test.items)
			}
		})
	}
}

func TestRecordLeavesOutWhatItCannotRead(t *testing.T) {
	turn := newRecord()
	if err := turn.add(messageDelta, `{"text": "kept"}`); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"text": `, `{"text": 1}`, `[]`} {
		if err := turn.add(messageDelta, data); err == nil {
			t.Errorf("add(%s) succeeded, want an error", data)
		}
	}
	encoded, err := turn.encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 1 || string(encoded[0]) != `{"type":"message","text":"kept"}` {
		t.Errorf("items = %q", encoded)
	}
}
