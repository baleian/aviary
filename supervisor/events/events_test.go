package events

import (
	"encoding/json"
	"testing"

	"aviary/supervisor/store"
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
			name:      "completed without usage",
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
				Failure: &store.Failure{Reason: store.FailureSandboxLost, Message: "the session's sandbox is gone"},
			},
			eventType: TurnFailed,
			data:      `{"reason":"sandbox_lost","message":"the session's sandbox is gone"}`,
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
				t.Errorf("Last = %s %s, want %s %s", eventType, encoded, test.eventType, test.data)
			}
			if !IsLast(eventType) {
				t.Errorf("IsLast(%s) = false, want true", eventType)
			}
		})
	}
	if IsLast(TurnStarted) || IsLast(TurnQueued) || IsLast(messageDelta) {
		t.Error("IsLast = true for an event before the end of a turn")
	}
}
