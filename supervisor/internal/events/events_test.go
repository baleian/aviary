package events

import (
	"encoding/json"
	"strings"
	"testing"

	"aviary/supervisor/internal/store"
)

func TestLast(t *testing.T) {
	reason := "end_turn"
	tests := []struct {
		name      string
		turn      store.Turn
		eventType string
		data      string
	}{
		{
			name: "completed",
			turn: store.Turn{
				State: store.TurnCompleted, StopReason: &reason,
				Usage: &store.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7},
			},
			eventType: TurnCompleted,
			data:      `{"stop_reason":"end_turn","usage":{"total_tokens":18,"input_tokens":11,"output_tokens":7}}`,
		},
		{
			name:      "completed by a harness that reports no usage",
			turn:      store.Turn{State: store.TurnCompleted, StopReason: &reason},
			eventType: TurnCompleted,
			data:      `{"stop_reason":"end_turn"}`,
		},
		{
			name:      "cancelled",
			turn:      store.Turn{State: store.TurnCancelled, StopReason: &reason},
			eventType: TurnCancelled,
			data:      `{}`,
		},
		{
			name:      "expired",
			turn:      store.Turn{State: store.TurnExpired},
			eventType: TurnExpired,
			data:      `{}`,
		},
		{
			name: "failed",
			turn: store.Turn{
				State:   store.TurnFailed,
				Failure: &store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"},
			},
			eventType: TurnFailed,
			data:      `{"reason":"pod_lost","message":"the pod was stopped"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eventType, data := Last(test.turn)
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if eventType != test.eventType || string(encoded) != test.data {
				t.Errorf("the last event is %s %s, not %s %s", eventType, encoded, test.eventType, test.data)
			}
			if !IsLast(eventType) {
				t.Errorf("%s does not count as the last event of a turn", eventType)
			}
		})
	}
	if IsLast(TurnStarted) || IsLast(TurnQueued) || IsLast(messageDelta) {
		t.Error("an event before the end of a turn counts as its last")
	}
}

func TestIsID(t *testing.T) {
	for id, is := range map[string]bool{
		Start:             true,
		"1763280000000-3": true,
		"":                false,
		"1763280000000":   false,
		"1763280000000-":  false,
		"-3":              false,
		"1-2-3":           false,
		"$":               false,
	} {
		if IsID(id) != is {
			t.Errorf("IsID(%q) is %t", id, !is)
		}
	}
}

func TestOpenKeepsThePasswordToItself(t *testing.T) {
	_, err := Open("valkey://default:secret@valkey:port")
	if err == nil {
		t.Fatal("Open took a URL without a port number")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("the error tells the password: %v", err)
	}
}
