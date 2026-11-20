package memory_test

import (
	"testing"

	"aviary/supervisor/events"
	"aviary/supervisor/events/eventstest"
	"aviary/supervisor/events/memory"
)

// The log in memory passes the conformance tests, so that the tests that use
// it are representative.
func TestLog(t *testing.T) {
	log := memory.New()
	eventstest.Run(t, func(*testing.T) events.Log { return log })
}
