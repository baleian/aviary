package attribution

import (
	"testing"

	"github.com/google/uuid"
)

func TestSessionOfAnActor(t *testing.T) {
	s := New(nil, "aviary")
	id := uuid.New()
	for _, actor := range []string{
		"spiffe://substrate-actor.local/worker-for-actor/aviary/session-" + id.String(),
		"spiffe://substrate-actor.local/actor/aviary/session-" + id.String(),
	} {
		if got, known := s.session(actor); !known || got != id {
			t.Errorf("session of %q = %s, %t; want %s", actor, got, known, id)
		}
	}
	for _, wrong := range []string{
		"spiffe://substrate-actor.local/worker-for-actor/other/session-" + id.String(),
		"spiffe://cluster.local/worker-for-actor/aviary/session-" + id.String(),
		"spiffe://substrate-actor.local/worker/aviary/session-" + id.String(),
		"spiffe://substrate-actor.local/worker-for-actor/aviary/" + id.String(),
		"spiffe://substrate-actor.local/worker-for-actor/aviary/session-x",
		"https://substrate-actor.local/worker-for-actor/aviary/session-" + id.String(),
		"",
	} {
		if got, known := s.session(wrong); known {
			t.Errorf("session of %q = %s, want none", wrong, got)
		}
	}
}
